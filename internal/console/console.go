package console

import (
	"fmt"
	"os"
)

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
)

func Errorf(format string, args ...any) {
	write(os.Stderr, red, "error", format, args...)
}

func Scanf(format string, args ...any) {
	write(os.Stderr, cyan, "scan", format, args...)
}

func Pipelinef(format string, args ...any) {
	// Used for periodic progress summaries while scanning many targets.
	write(os.Stderr, green, "progress", format, args...)
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
