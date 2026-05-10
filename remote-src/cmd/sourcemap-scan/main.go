package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"sourcemap-scan/internal/app"
	"sourcemap-scan/internal/scan"
)

func main() {
	cfg, err := app.ParseConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	svc, err := scan.NewService(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := svc.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
