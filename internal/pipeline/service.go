package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"sourcemap-scan/internal/console"
	"sourcemap-scan/internal/model"
	"sourcemap-scan/internal/process"
	"sourcemap-scan/internal/scan"
)

type Service struct {
	cfg Config
}

func NewService(cfg Config) (*Service, error) {
	return &Service{cfg: cfg}, nil
}

func (s *Service) Run(ctx context.Context) error {
	findingsPath, err := s.resolveFindingsPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(findingsPath), 0o755); err != nil {
		return err
	}

	scanCfg := s.cfg.Scan
	scanCfg.OutputPath = findingsPath
	processCfg := s.cfg.Process
	processCfg.InputPath = findingsPath

	findingsCh := make(chan model.Finding, max(32, processCfg.ProcessWorkers*8))
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var discoveredCount atomic.Int64
	processErrCh := make(chan error, 1)

	processSvc, err := process.NewService(processCfg)
	if err != nil {
		return err
	}

	go func() {
		processErrCh <- processSvc.RunStream(streamCtx, findingsCh)
	}()

	scanSvc, err := scan.NewServiceWithEmitter(scanCfg, func(ctx context.Context, finding model.Finding) error {
		discoveredCount.Add(1)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-streamCtx.Done():
			return streamCtx.Err()
		case findingsCh <- finding:
			queued := discoveredCount.Load()
			if queued <= 5 || queued%25 == 0 {
				console.Pipelinef(
					"findings discovered=%d queued_for_process=%d last_map=%s",
					queued,
					queued,
					finding.MapURL,
				)
			}
			return nil
		}
	})
	if err != nil {
		close(findingsCh)
		<-processErrCh
		return err
	}

	console.Pipelinef(
		"stream start targets=%d findings=%s base_dir=%s process_workers=%d",
		len(s.cfg.Scan.Targets),
		console.Highlight(findingsPath),
		console.Highlight(s.cfg.Process.BaseDir),
		s.cfg.Process.ProcessWorkers,
	)

	scanErr := scanSvc.Run(streamCtx)
	close(findingsCh)
	processErr := <-processErrCh

	if scanErr != nil {
		cancel()
	}

	if err := firstMeaningfulError(scanErr, processErr); err != nil {
		return err
	}

	processStats := processSvc.Stats()
	console.Successf(
		"pipeline done targets=%d findings=%d processed=%d skipped=%d failed=%d hits=%d verified=%d notified=%d path=%s base_dir=%s",
		len(s.cfg.Scan.Targets),
		discoveredCount.Load(),
		processStats.SuccessFindings,
		processStats.SkippedFindings,
		processStats.FailedFindings,
		processStats.HitsTotal,
		processStats.VerifiedHits,
		processStats.Notified,
		findingsPath,
		s.cfg.Process.BaseDir,
	)

	return nil
}

func (s *Service) resolveFindingsPath() (string, error) {
	if s.cfg.FindingsPath != "" {
		return filepath.Clean(s.cfg.FindingsPath), nil
	}

	name := fmt.Sprintf("findings-%s.jsonl", time.Now().UTC().Format("20060102-150405"))
	return filepath.Join(s.cfg.Process.BaseDir, "findings", name), nil
}

func firstMeaningfulError(errs ...error) error {
	for _, err := range errs {
		if err == nil {
			continue
		}
		if errors.Is(err, context.Canceled) {
			continue
		}
		return err
	}
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func max(values ...int) int {
	current := 0
	for _, value := range values {
		if value > current {
			current = value
		}
	}
	return current
}
