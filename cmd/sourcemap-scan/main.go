package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"sourcemap-scan/internal/app"
	"sourcemap-scan/internal/console"
	"sourcemap-scan/internal/pipeline"
	"sourcemap-scan/internal/process"
	"sourcemap-scan/internal/scan"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	args := os.Args[1:]
	if len(args) > 0 && args[0] == "pipeline" {
		cfg, err := pipeline.ParseConfig(args[1:])
		if err != nil {
			console.Errorf("%v", err)
			os.Exit(2)
		}

		svc, err := pipeline.NewService(cfg)
		if err != nil {
			console.Errorf("%v", err)
			os.Exit(1)
		}

		if err := svc.Run(ctx); err != nil {
			console.Errorf("%v", err)
			os.Exit(1)
		}
		return
	}

	if len(args) > 0 && args[0] == "process" {
		cfg, err := process.ParseConfig(args[1:])
		if err != nil {
			console.Errorf("%v", err)
			os.Exit(2)
		}

		svc, err := process.NewService(cfg)
		if err != nil {
			console.Errorf("%v", err)
			os.Exit(1)
		}

		if err := svc.Run(ctx); err != nil {
			console.Errorf("%v", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := app.ParseConfig(args)
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
