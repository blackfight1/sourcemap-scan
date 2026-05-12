package console

import (
	"fmt"
	"os"
	"strings"
)

const (
	reset   = "\033[0m"
	bold    = "\033[1m"
	dim     = "\033[2m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
)

func Errorf(format string, args ...any) {
	write(os.Stderr, red, "error", format, args...)
}

func Scanf(format string, args ...any) {
	write(os.Stderr, cyan, "scan", format, args...)
}

func Processf(format string, args ...any) {
	write(os.Stderr, blue, "process", format, args...)
}

func Pipelinef(format string, args ...any) {
	write(os.Stderr, green, "pipeline", format, args...)
}

func Batchf(format string, args ...any) {
	write(os.Stderr, magenta, "batch", format, args...)
}

func Warnf(format string, args ...any) {
	write(os.Stderr, yellow, "warn", format, args...)
}

func Successf(format string, args ...any) {
	write(os.Stderr, green, "ok", format, args...)
}

func Failf(format string, args ...any) {
	write(os.Stderr, red, "fail", format, args...)
}

func Skipf(format string, args ...any) {
	write(os.Stderr, yellow, "skip", format, args...)
}

func Hitf(format string, args ...any) {
	write(os.Stderr, magenta, "hit", format, args...)
}

func Stagef(scope string, subject string, stage string, format string, args ...any) {
	prefix := fmt.Sprintf(
		"%s[%s]%s %s%s%s %sstage=%s%s",
		dim,
		scope,
		reset,
		bold,
		subject,
		reset,
		stageColor(stage),
		stage,
		reset,
	)
	if strings.TrimSpace(format) == "" {
		fmt.Fprintf(os.Stderr, "%s\n", prefix)
		return
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", prefix, fmt.Sprintf(format, args...))
}

func Dim(text string) string {
	return dim + text + reset
}

func Highlight(text string) string {
	return bold + text + reset
}

func write(file *os.File, color string, label string, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	fmt.Fprintf(file, "%s[%s]%s %s\n", color, label, reset, message)
}

func stageColor(stage string) string {
	switch strings.TrimSpace(stage) {
	case "prepare":
		return cyan
	case "download":
		return yellow
	case "shuji":
		return blue
	case "trufflehog":
		return magenta
	case "analyze":
		return green
	case "cleanup":
		return dim
	default:
		return yellow
	}
}
