package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"sourcemap-scan/internal/app"
	"sourcemap-scan/internal/console"
	"sourcemap-scan/internal/scan"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := app.ParseConfig(os.Args[1:])
	if err != nil {
		console.Errorf("%v", err)
		os.Exit(2)
	}

	svc, err := scan.NewService(cfg)
	if err != nil {
		console.Errorf("%v", err)
		os.Exit(1)
	}

	if err := svc.Run(ctx); err != nil {
		console.Errorf("%v", err)
		os.Exit(1)
	}
}
