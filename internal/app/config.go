package app

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

	"sourcemap-scan/internal/console"
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
	DisableKatana     bool
	WaymoreBin        string
	WaymoreNoSubs     bool
	WaymoreTimeout    time.Duration
	WaymoreExtraArgs  []string
	DisableWaymore    bool
	ScanWorkers       int
	HTTPTimeout       time.Duration
	TailBytes         int64
	MaxJSBytes        int64
	MaxMapBytes       int64
	UserAgent         string
}

func ParseConfig(args []string) (Config, error) {
	fs := flag.NewFlagSet("sourcemap-scan", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	cfg := Config{}
	var katanaExtraArgs string
	var waymoreExtraArgs string
	var singleTarget string
	var targetFile string

	// Keep the common surface tiny.
	fs.StringVar(&singleTarget, "u", "", "single target (URL or domain)")
	fs.StringVar(&targetFile, "l", "", "target list file")
	fs.StringVar(&cfg.OutputPath, "o", "findings.jsonl", "output file (`-` = stdout)")
	fs.BoolVar(&cfg.Verbose, "v", false, "verbose logs")
	fs.BoolVar(&cfg.Verbose, "verbose", false, "verbose logs")
	fs.IntVar(&cfg.TargetWorkers, "c", 3, "target concurrency")
	fs.IntVar(&cfg.TargetWorkers, "target-workers", 3, "target concurrency")
	fs.IntVar(&cfg.ScanWorkers, "w", 10, "per-target asset workers")
	fs.IntVar(&cfg.ScanWorkers, "scan-workers", 10, "per-target asset workers")

	fs.BoolVar(&cfg.DisableKatana, "no-katana", false, "disable katana")
	fs.BoolVar(&cfg.DisableWaymore, "no-waymore", false, "disable waymore")

	// Advanced (still available, hidden from short help)
	fs.StringVar(&cfg.KatanaBin, "katana-bin", "katana", "katana binary")
	fs.IntVar(&cfg.KatanaDepth, "katana-depth", 3, "katana depth")
	fs.IntVar(&cfg.KatanaConcurrency, "katana-concurrency", 10, "katana concurrency")
	fs.IntVar(&cfg.KatanaParallelism, "katana-parallelism", 3, "katana parallelism")
	fs.IntVar(&cfg.KatanaRateLimit, "katana-rate-limit", 30, "katana rate limit")
	fs.StringVar(&katanaExtraArgs, "katana-extra-args", "", "extra katana args")
	fs.StringVar(&cfg.WaymoreBin, "waymore-bin", "waymore", "waymore binary")
	fs.BoolVar(&cfg.WaymoreNoSubs, "waymore-no-subs", true, "waymore -n (no extra subs)")
	fs.DurationVar(&cfg.WaymoreTimeout, "waymore-timeout", 20*time.Minute, "waymore timeout")
	fs.StringVar(&waymoreExtraArgs, "waymore-extra-args", "", "extra waymore args")
	fs.DurationVar(&cfg.HTTPTimeout, "http-timeout", 15*time.Second, "HTTP timeout")
	fs.Int64Var(&cfg.TailBytes, "tail-bytes", 8192, "JS tail bytes")
	fs.Int64Var(&cfg.MaxJSBytes, "max-js-bytes", 16*1024*1024, "max JS bytes")
	fs.Int64Var(&cfg.MaxMapBytes, "max-map-bytes", 10*1024*1024, "max map bytes")
	fs.StringVar(&cfg.UserAgent, "user-agent", "sourcemap-scan/0.1", "User-Agent")

	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `sourcemap-scan — collect JS (waymore+katana) and find sourcemaps

Usage:
  sourcemap-scan <targets.txt>
  sourcemap-scan <url-or-domain>
  sourcemap-scan -l targets.txt
  sourcemap-scan -u https://a.com

Common flags:
  -o file     output JSONL (default: findings.jsonl, use - for stdout)
  -c N        target concurrency (default: 3)
  -w N        asset workers per target (default: 10)
  -v          verbose
  -no-katana  only waymore
  -no-waymore only katana

Examples:
  sourcemap-scan targets.txt
  sourcemap-scan https://app.example.com
  sourcemap-scan example.com -no-waymore
  sourcemap-scan targets.txt -c 4 -o maps.jsonl
`)
	}

	// std flag stops at first positional; reorder so flags after targets still work.
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return Config{}, err
	}

	singleTarget = strings.TrimSpace(singleTarget)
	targetFile = strings.TrimSpace(targetFile)
	rest := fs.Args()

	// Positional: file or one/more targets
	if singleTarget == "" && targetFile == "" {
		if len(rest) == 0 {
			return Config{}, errors.New("need a target file or URL\n\n  sourcemap-scan targets.txt\n  sourcemap-scan https://example.com")
		}
		if len(rest) == 1 && looksLikeListFile(rest[0]) {
			targetFile = rest[0]
		} else {
			// treat all positionals as targets
			var err error
			cfg.Targets, err = collectTargets(rest)
			if err != nil {
				return Config{}, err
			}
		}
	} else if len(rest) > 0 {
		return Config{}, errors.New("do not mix positional targets with -u/-l")
	}

	if singleTarget != "" && targetFile != "" {
		return Config{}, errors.New("use only one of -u or -l")
	}

	var err error
	switch {
	case len(cfg.Targets) > 0:
		// already filled from positionals
	case singleTarget != "":
		cfg.Targets, err = collectTargets([]string{singleTarget})
	case targetFile != "":
		cfg.Targets, err = readTargetsFromFile(targetFile)
	}
	if err != nil {
		return Config{}, err
	}

	// Normalize output: empty or default path; "-" means stdout
	cfg.OutputPath = strings.TrimSpace(cfg.OutputPath)
	if cfg.OutputPath == "-" {
		cfg.OutputPath = ""
	}

	if katanaExtraArgs != "" {
		cfg.KatanaExtraArgs = strings.Fields(katanaExtraArgs)
	}
	if waymoreExtraArgs != "" {
		cfg.WaymoreExtraArgs = strings.Fields(waymoreExtraArgs)
	}

	if err := ValidateCollectorConfig(&cfg); err != nil {
		return Config{}, err
	}

	outLabel := cfg.OutputPath
	if outLabel == "" {
		outLabel = "stdout"
	}
	console.Scanf("targets=%d output=%s concurrency=%d", len(cfg.Targets), outLabel, cfg.TargetWorkers)

	return cfg, nil
}

// reorderArgs moves flags before positionals so `tool targets.txt -o out` works
// with the standard library flag parser.
func reorderArgs(args []string) []string {
	boolFlags := map[string]struct{}{
		"-v": {}, "--v": {},
		"-verbose": {}, "--verbose": {},
		"-no-katana": {}, "--no-katana": {},
		"-no-waymore": {}, "--no-waymore": {},
		"-waymore-no-subs": {}, "--waymore-no-subs": {},
		"-h": {}, "--h": {}, "-help": {}, "--help": {},
	}

	var flags []string
	var positionals []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}

		// -flag=value
		if strings.Contains(arg, "=") {
			flags = append(flags, arg)
			name := strings.SplitN(arg, "=", 2)[0]
			// bool with explicit value still one token
			_ = name
			continue
		}

		flags = append(flags, arg)
		name := arg
		if _, isBool := boolFlags[name]; isBool {
			continue
		}
		// Non-bool flag may take the next token as its value.
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			i++
			flags = append(flags, args[i])
		}
	}

	return append(flags, positionals...)
}

// ValidateCollectorConfig checks shared scan/collector settings.
func ValidateCollectorConfig(cfg *Config) error {
	if cfg.DisableKatana && cfg.DisableWaymore {
		return errors.New("enable at least one of katana/waymore (remove -no-katana/-no-waymore)")
	}
	if !cfg.DisableKatana {
		if cfg.KatanaDepth < 1 {
			return errors.New("katana-depth must be >= 1")
		}
		if cfg.KatanaConcurrency < 1 {
			return errors.New("katana-concurrency must be >= 1")
		}
		if cfg.KatanaParallelism < 1 {
			return errors.New("katana-parallelism must be >= 1")
		}
		if cfg.KatanaRateLimit < 1 {
			return errors.New("katana-rate-limit must be >= 1")
		}
		if strings.TrimSpace(cfg.KatanaBin) == "" {
			return errors.New("katana-bin must not be empty")
		}
	}
	if !cfg.DisableWaymore {
		if strings.TrimSpace(cfg.WaymoreBin) == "" {
			return errors.New("waymore-bin must not be empty")
		}
		if cfg.WaymoreTimeout < 0 {
			return errors.New("waymore-timeout must be >= 0")
		}
		if cfg.WaymoreTimeout == 0 {
			cfg.WaymoreTimeout = 20 * time.Minute
		}
	}
	if cfg.TargetWorkers < 1 {
		return errors.New("concurrency (-c) must be >= 1")
	}
	if cfg.ScanWorkers < 1 {
		return errors.New("workers (-w) must be >= 1")
	}
	if cfg.TailBytes < 512 {
		return errors.New("tail-bytes must be >= 512")
	}
	if cfg.MaxJSBytes < cfg.TailBytes {
		return errors.New("max-js-bytes must be >= tail-bytes")
	}
	if cfg.MaxMapBytes < 1024 {
		return errors.New("max-map-bytes must be >= 1024")
	}
	if cfg.HTTPTimeout <= 0 {
		return errors.New("http-timeout must be > 0")
	}
	return nil
}

func looksLikeListFile(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	// URL-like is not a file list
	if strings.Contains(path, "://") {
		return false
	}
	// domain.tld without path separators → target, not file
	if !strings.ContainsAny(path, `/\`) {
		// if file exists, treat as list; else domain
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return true
		}
		return false
	}
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		return true
	}
	// path with separator that doesn't exist — still try as file later
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".txt", ".list", ".lst", ".csv":
		return true
	}
	return false
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
		return nil, errors.New("target file is empty")
	}

	return collectTargets(rawTargets)
}

func collectTargets(items []string) ([]string, error) {
	seen := make(map[string]struct{}, len(items))
	targets := make([]string, 0, len(items))

	for _, item := range items {
		target, err := normalizeTarget(item)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}

	if len(targets) == 0 {
		return nil, errors.New("no valid targets")
	}

	return targets, nil
}

// normalizeTarget accepts https://host, http://host, or bare domain → https://domain
func normalizeTarget(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty target")
	}

	// strip accidental quotes
	raw = strings.Trim(raw, `"'`)

	if !strings.Contains(raw, "://") {
		// host or host/path
		raw = "https://" + raw
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid target: %q", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme in target: %q", raw)
	}

	return parsed.String(), nil
}
