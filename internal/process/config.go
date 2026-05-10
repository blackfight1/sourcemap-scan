package process

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	InputPath              string
	BaseDir                string
	ShujiBin               string
	TruffleHogBin          string
	TruffleHogExtraArgs    []string
	KeepRestored           bool
	OnlyWithSourcesContent bool
	FeishuWebhook          string
}

func ParseConfig(args []string) (Config, error) {
	fs := flag.NewFlagSet("sourcemap-scan process", flag.ContinueOnError)

	cfg := Config{}
	var extraArgs string

	fs.StringVar(&cfg.InputPath, "i", "", "input findings JSONL file")
	fs.StringVar(&cfg.BaseDir, "base-dir", ".sourcemap-process", "base directory for work, state, and logs")
	fs.StringVar(&cfg.ShujiBin, "shuji-bin", "shuji", "path to shuji binary")
	fs.StringVar(&cfg.TruffleHogBin, "trufflehog-bin", "trufflehog", "path to trufflehog binary")
	fs.StringVar(&extraArgs, "trufflehog-extra-args", "", "additional trufflehog arguments, split on spaces")
	fs.BoolVar(&cfg.KeepRestored, "keep-restored", false, "keep restored source directories after processing")
	fs.BoolVar(&cfg.OnlyWithSourcesContent, "only-with-sources-content", true, "process only findings with sourcesContent")
	fs.StringVar(&cfg.FeishuWebhook, "feishu-webhook", "", "Feishu bot webhook for notifications")

	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintln(out, "Usage: sourcemap-scan process -i findings.jsonl [options]")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Main options:")
		fmt.Fprintln(out, "  -i string")
		fmt.Fprintln(out, "        input findings JSONL file")
		fmt.Fprintln(out, "  -base-dir string")
		fmt.Fprintln(out, "        base directory for work, state, and logs")
		fmt.Fprintln(out, "  -shuji-bin string")
		fmt.Fprintln(out, "        path to shuji binary")
		fmt.Fprintln(out, "  -trufflehog-bin string")
		fmt.Fprintln(out, "        path to trufflehog binary")
		fmt.Fprintln(out, "  -feishu-webhook string")
		fmt.Fprintln(out, "        Feishu bot webhook for notifications")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Example:")
		fmt.Fprintln(out, "  sourcemap-scan process -i findings.jsonl -base-dir /opt/sourcemap/run")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Advanced flags are still supported but intentionally omitted from this help.")
	}

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	cfg.InputPath = strings.TrimSpace(cfg.InputPath)
	if cfg.InputPath == "" {
		return Config{}, errors.New("input findings file is required via -i")
	}
	if _, err := os.Stat(cfg.InputPath); err != nil {
		return Config{}, fmt.Errorf("input findings file: %w", err)
	}

	cfg.BaseDir = strings.TrimSpace(cfg.BaseDir)
	if cfg.BaseDir == "" {
		return Config{}, errors.New("base-dir must not be empty")
	}
	cfg.BaseDir = filepath.Clean(cfg.BaseDir)

	if extraArgs != "" {
		cfg.TruffleHogExtraArgs = strings.Fields(extraArgs)
	}

	cfg.FeishuWebhook = strings.TrimSpace(cfg.FeishuWebhook)

	return cfg, nil
}
