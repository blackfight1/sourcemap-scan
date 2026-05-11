package pipeline

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sourcemap-scan/internal/app"
	"sourcemap-scan/internal/process"
)

type Config struct {
	Scan             app.Config
	Process          process.Config
	FindingsPath     string
	AutoFindingsPath bool
}

func ParseConfig(args []string) (Config, error) {
	fs := flag.NewFlagSet("sourcemap-scan pipeline", flag.ContinueOnError)

	var cfg Config
	var singleTarget string
	var targetFile string
	var katanaExtraArgs string
	var trufflehogExtraArgs string

	scanCfg := app.Config{}
	processCfg := process.Config{
		BaseDir:                ".sourcemap-pipeline",
		Verbose:                false,
		ShujiBin:               "shuji",
		TruffleHogBin:          "trufflehog",
		ProcessWorkers:         1,
		OnlyWithSourcesContent: true,
		FeishuWebhook:          process.DefaultFeishuWebhook,
	}

	fs.StringVar(&singleTarget, "u", "", "single target URL")
	fs.StringVar(&targetFile, "l", "", "file with target URLs, one per line")
	fs.StringVar(&cfg.FindingsPath, "o", "", "write findings JSONL here (default: auto path under base-dir)")
	fs.BoolVar(&scanCfg.Verbose, "verbose", false, "print detailed stage-level logs")
	fs.IntVar(&scanCfg.TargetWorkers, "target-workers", 2, "number of targets to scan in parallel")
	fs.StringVar(&scanCfg.KatanaBin, "katana-bin", "katana", "path to katana binary")
	fs.IntVar(&scanCfg.KatanaDepth, "katana-depth", 3, "katana crawl depth")
	fs.IntVar(&scanCfg.KatanaConcurrency, "katana-concurrency", 10, "katana fetch concurrency")
	fs.IntVar(&scanCfg.KatanaParallelism, "katana-parallelism", 3, "katana input parallelism")
	fs.IntVar(&scanCfg.KatanaRateLimit, "katana-rate-limit", 30, "katana request rate limit per second")
	fs.StringVar(&katanaExtraArgs, "katana-extra-args", "", "additional katana arguments, split on spaces")
	fs.IntVar(&scanCfg.ScanWorkers, "scan-workers", 10, "number of JS scanning workers per target")
	fs.DurationVar(&scanCfg.HTTPTimeout, "http-timeout", 15*time.Second, "HTTP timeout for JS and map fetches")
	fs.Int64Var(&scanCfg.TailBytes, "tail-bytes", 8192, "number of bytes requested from the end of each JS asset")
	fs.Int64Var(&scanCfg.MaxJSBytes, "max-js-bytes", 16*1024*1024, "maximum JS response bytes to read before aborting")
	fs.Int64Var(&scanCfg.MaxMapBytes, "max-map-bytes", 10*1024*1024, "maximum map response bytes to read before aborting")
	fs.StringVar(&scanCfg.UserAgent, "user-agent", "sourcemap-scan/0.1", "user agent used for JS and map requests")

	fs.StringVar(&processCfg.BaseDir, "base-dir", ".sourcemap-pipeline", "base directory for compact outputs (findings/results/state)")
	fs.StringVar(&processCfg.ShujiBin, "shuji-bin", "shuji", "path to shuji binary")
	fs.StringVar(&processCfg.TruffleHogBin, "trufflehog-bin", "trufflehog", "path to trufflehog binary")
	fs.IntVar(&processCfg.ProcessWorkers, "process-workers", 1, "number of sourcemaps to process in parallel")
	fs.StringVar(&trufflehogExtraArgs, "trufflehog-extra-args", "", "additional trufflehog arguments, split on spaces")
	fs.BoolVar(&processCfg.KeepRestored, "keep-restored", false, "keep restored source directories after processing")
	fs.BoolVar(&processCfg.KeepArtifacts, "keep-artifacts", false, "keep per-map artifacts such as summary.json and raw TruffleHog output")
	fs.BoolVar(&processCfg.OnlyWithSourcesContent, "only-with-sources-content", true, "process only findings with sourcesContent")
	fs.StringVar(&processCfg.FeishuWebhook, "feishu-webhook", process.DefaultFeishuWebhook, "Feishu bot webhook for notifications")

	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintln(out, "Usage: sourcemap-scan pipeline (-u https://target.tld | -l targets.txt) [options]")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Main options:")
		fmt.Fprintln(out, "  -u string")
		fmt.Fprintln(out, "        single target URL")
		fmt.Fprintln(out, "  -l string")
		fmt.Fprintln(out, "        file with target URLs, one per line")
		fmt.Fprintln(out, "  -verbose")
		fmt.Fprintln(out, "        print detailed stage-level logs")
		fmt.Fprintln(out, "  -base-dir string")
		fmt.Fprintln(out, "        base directory for compact outputs (findings/results/state)")
		fmt.Fprintf(out, "  -target-workers int\n        number of targets to scan in parallel (default %d)\n", scanCfg.TargetWorkers)
		fmt.Fprintf(out, "  -katana-bin string\n        path to katana binary (default %q)\n", scanCfg.KatanaBin)
		fmt.Fprintf(out, "  -shuji-bin string\n        path to shuji binary (default %q)\n", processCfg.ShujiBin)
		fmt.Fprintf(out, "  -trufflehog-bin string\n        path to trufflehog binary (default %q)\n", processCfg.TruffleHogBin)
		fmt.Fprintf(out, "  -process-workers int\n        number of sourcemaps to process in parallel (default %d)\n", processCfg.ProcessWorkers)
		fmt.Fprintln(out, "  -keep-artifacts")
		fmt.Fprintln(out, "        keep per-map artifacts under base-dir/work")
		fmt.Fprintln(out, "  -feishu-webhook string")
		fmt.Fprintln(out, "        Feishu bot webhook for verified hits (default: built-in webhook)")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Examples:")
		fmt.Fprintln(out, "  sourcemap-scan pipeline -u https://target.tld -base-dir /opt/sourcemap/run1")
		fmt.Fprintln(out, "  sourcemap-scan pipeline -l targets.txt -base-dir /opt/sourcemap/batch")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Advanced flags are still supported but intentionally omitted from this help.")
	}

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	targets, err := parseTargets(singleTarget, targetFile)
	if err != nil {
		return Config{}, err
	}
	scanCfg.Targets = targets

	if scanCfg.KatanaDepth < 1 {
		return Config{}, errors.New("katana-depth must be >= 1")
	}
	if scanCfg.TargetWorkers < 1 {
		return Config{}, errors.New("target-workers must be >= 1")
	}
	if scanCfg.KatanaConcurrency < 1 {
		return Config{}, errors.New("katana-concurrency must be >= 1")
	}
	if scanCfg.KatanaParallelism < 1 {
		return Config{}, errors.New("katana-parallelism must be >= 1")
	}
	if scanCfg.KatanaRateLimit < 1 {
		return Config{}, errors.New("katana-rate-limit must be >= 1")
	}
	if scanCfg.ScanWorkers < 1 {
		return Config{}, errors.New("scan-workers must be >= 1")
	}
	if scanCfg.TailBytes < 512 {
		return Config{}, errors.New("tail-bytes must be >= 512")
	}
	if scanCfg.MaxJSBytes < scanCfg.TailBytes {
		return Config{}, errors.New("max-js-bytes must be >= tail-bytes")
	}
	if scanCfg.MaxMapBytes < 1024 {
		return Config{}, errors.New("max-map-bytes must be >= 1024")
	}
	if scanCfg.HTTPTimeout <= 0 {
		return Config{}, errors.New("http-timeout must be > 0")
	}

	processCfg.BaseDir = strings.TrimSpace(processCfg.BaseDir)
	if processCfg.BaseDir == "" {
		return Config{}, errors.New("base-dir must not be empty")
	}
	processCfg.Verbose = scanCfg.Verbose
	processCfg.BaseDir = filepath.Clean(processCfg.BaseDir)
	if processCfg.ProcessWorkers < 1 {
		return Config{}, errors.New("process-workers must be >= 1")
	}
	processCfg.FeishuWebhook = strings.TrimSpace(processCfg.FeishuWebhook)

	if katanaExtraArgs != "" {
		scanCfg.KatanaExtraArgs = strings.Fields(katanaExtraArgs)
	}
	if trufflehogExtraArgs != "" {
		processCfg.TruffleHogExtraArgs = strings.Fields(trufflehogExtraArgs)
	}

	cfg.FindingsPath = strings.TrimSpace(cfg.FindingsPath)
	cfg.AutoFindingsPath = cfg.FindingsPath == ""
	cfg.Scan = scanCfg
	cfg.Process = processCfg

	return cfg, nil
}

func parseTargets(singleTarget string, targetFile string) ([]string, error) {
	singleTarget = strings.TrimSpace(singleTarget)
	targetFile = strings.TrimSpace(targetFile)

	if (singleTarget == "" && targetFile == "") || (singleTarget != "" && targetFile != "") {
		return nil, errors.New("use exactly one of -u or -l")
	}

	if singleTarget != "" {
		return collectTargets([]string{singleTarget})
	}

	return readTargetsFromFile(targetFile)
}

func readTargetsFromFile(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var rawTargets []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rawTargets = append(rawTargets, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(rawTargets) == 0 {
		return nil, errors.New("target file did not contain any usable URLs")
	}

	return collectTargets(rawTargets)
}

func collectTargets(items []string) ([]string, error) {
	seen := make(map[string]struct{}, len(items))
	targets := make([]string, 0, len(items))

	for _, item := range items {
		target := strings.TrimSpace(item)
		if target == "" {
			continue
		}

		parsed, err := url.Parse(target)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("invalid target URL: %q", target)
		}

		normalized := parsed.String()
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		targets = append(targets, normalized)
	}

	if len(targets) == 0 {
		return nil, errors.New("no valid targets provided")
	}

	return targets, nil
}
