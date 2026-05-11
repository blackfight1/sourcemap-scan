package app

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	Targets           []string
	OutputPath        string
	Verbose           bool
	TargetWorkers     int
	KatanaBin         string
	KatanaDepth       int
	KatanaConcurrency int
	KatanaParallelism int
	KatanaRateLimit   int
	KatanaExtraArgs   []string
	ScanWorkers       int
	HTTPTimeout       time.Duration
	TailBytes         int64
	MaxJSBytes        int64
	MaxMapBytes       int64
	UserAgent         string
}

func ParseConfig(args []string) (Config, error) {
	fs := flag.NewFlagSet("sourcemap-scan", flag.ContinueOnError)

	cfg := Config{}
	var extraArgs string
	var singleTarget string
	var targetFile string

	fs.StringVar(&singleTarget, "u", "", "single target URL")
	fs.StringVar(&targetFile, "l", "", "file with target URLs, one per line")
	fs.StringVar(&cfg.OutputPath, "o", "", "write findings as JSONL to this file (default: stdout)")
	fs.BoolVar(&cfg.Verbose, "verbose", false, "print detailed stage-level logs")
	fs.IntVar(&cfg.TargetWorkers, "target-workers", 2, "number of targets to scan in parallel")
	fs.StringVar(&cfg.KatanaBin, "katana-bin", "katana", "path to katana binary")
	fs.IntVar(&cfg.KatanaDepth, "katana-depth", 3, "katana crawl depth")
	fs.IntVar(&cfg.KatanaConcurrency, "katana-concurrency", 10, "katana fetch concurrency")
	fs.IntVar(&cfg.KatanaParallelism, "katana-parallelism", 3, "katana input parallelism")
	fs.IntVar(&cfg.KatanaRateLimit, "katana-rate-limit", 30, "katana request rate limit per second")
	fs.StringVar(&extraArgs, "katana-extra-args", "", "additional katana arguments, split on spaces")
	fs.IntVar(&cfg.ScanWorkers, "scan-workers", 10, "number of JS scanning workers")
	fs.DurationVar(&cfg.HTTPTimeout, "http-timeout", 15*time.Second, "HTTP timeout for JS and map fetches")
	fs.Int64Var(&cfg.TailBytes, "tail-bytes", 8192, "number of bytes requested from the end of each JS asset")
	fs.Int64Var(&cfg.MaxJSBytes, "max-js-bytes", 16*1024*1024, "maximum JS response bytes to read before aborting")
	fs.Int64Var(&cfg.MaxMapBytes, "max-map-bytes", 10*1024*1024, "maximum map response bytes to read before aborting")
	fs.StringVar(&cfg.UserAgent, "user-agent", "sourcemap-scan/0.1", "user agent used for JS and map requests")

	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintf(out, "Usage: sourcemap-scan (-u https://target.tld | -l targets.txt) [options]\n\n")
		fmt.Fprintln(out, "Main options:")
		fmt.Fprintln(out, "  -u string")
		fmt.Fprintln(out, "        single target URL")
		fmt.Fprintln(out, "  -l string")
		fmt.Fprintln(out, "        file with target URLs, one per line")
		fmt.Fprintln(out, "  -o string")
		fmt.Fprintln(out, "        write findings as JSONL to this file (default: stdout)")
		fmt.Fprintln(out, "  -verbose")
		fmt.Fprintln(out, "        print detailed stage-level logs")
		fmt.Fprintf(out, "  -target-workers int\n        number of targets to scan in parallel (default %d)\n", cfg.TargetWorkers)
		fmt.Fprintf(out, "  -katana-bin string\n        path to katana binary (default %q)\n", cfg.KatanaBin)
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Examples:")
		fmt.Fprintln(out, "  sourcemap-scan -u https://target.tld")
		fmt.Fprintln(out, "  sourcemap-scan -l targets.txt -o findings.jsonl")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Advanced flags are still supported but intentionally omitted from this help.")
	}

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	singleTarget = strings.TrimSpace(singleTarget)
	targetFile = strings.TrimSpace(targetFile)

	if (singleTarget == "" && targetFile == "") || (singleTarget != "" && targetFile != "") {
		return Config{}, errors.New("use exactly one of -u or -l")
	}

	var err error
	if singleTarget != "" {
		cfg.Targets, err = collectTargets([]string{singleTarget})
	} else {
		cfg.Targets, err = readTargetsFromFile(targetFile)
	}
	if err != nil {
		return Config{}, err
	}

	if cfg.KatanaDepth < 1 {
		return Config{}, errors.New("katana-depth must be >= 1")
	}
	if cfg.TargetWorkers < 1 {
		return Config{}, errors.New("target-workers must be >= 1")
	}
	if cfg.KatanaConcurrency < 1 {
		return Config{}, errors.New("katana-concurrency must be >= 1")
	}
	if cfg.KatanaParallelism < 1 {
		return Config{}, errors.New("katana-parallelism must be >= 1")
	}
	if cfg.KatanaRateLimit < 1 {
		return Config{}, errors.New("katana-rate-limit must be >= 1")
	}
	if cfg.ScanWorkers < 1 {
		return Config{}, errors.New("scan-workers must be >= 1")
	}
	if cfg.TailBytes < 512 {
		return Config{}, errors.New("tail-bytes must be >= 512")
	}
	if cfg.MaxJSBytes < cfg.TailBytes {
		return Config{}, errors.New("max-js-bytes must be >= tail-bytes")
	}
	if cfg.MaxMapBytes < 1024 {
		return Config{}, errors.New("max-map-bytes must be >= 1024")
	}
	if cfg.HTTPTimeout <= 0 {
		return Config{}, errors.New("http-timeout must be > 0")
	}

	if extraArgs != "" {
		cfg.KatanaExtraArgs = strings.Fields(extraArgs)
	}

	return cfg, nil
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
