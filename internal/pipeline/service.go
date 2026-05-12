package pipeline

import (
	"context"
	"encoding/json"
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

type runStats struct {
	Targets      int
	Discovered   int64
	Processed    int64
	Skipped      int64
	Failed       int64
	Hits         int64
	Verified     int64
	Notified     int64
	FindingsPath string
	BaseDir      string
	BatchIndex   int
	BatchTotal   int
	BatchFrom    int
	BatchTo      int
}

type batchSummary struct {
	BatchIndex   int    `json:"batch_index"`
	BatchTotal   int    `json:"batch_total"`
	TargetFrom   int    `json:"target_from"`
	TargetTo     int    `json:"target_to"`
	Targets      int    `json:"targets"`
	Discovered   int64  `json:"discovered"`
	Processed    int64  `json:"processed"`
	Skipped      int64  `json:"skipped"`
	Failed       int64  `json:"failed"`
	Hits         int64  `json:"hits"`
	Verified     int64  `json:"verified"`
	Notified     int64  `json:"notified"`
	FinishedAt   string `json:"finished_at"`
	BaseDir      string `json:"base_dir"`
	FindingsPath string `json:"findings_path"`
}

func NewService(cfg Config) (*Service, error) {
	return &Service{cfg: cfg}, nil
}

func (s *Service) Run(ctx context.Context) error {
	batches := splitTargets(s.cfg.Scan.Targets, s.cfg.BatchSize)
	totalBatches := len(batches)
	if totalBatches == 0 {
		return errors.New("no targets to scan")
	}

	var overall runStats
	var failedBatches int

	for idx, targets := range batches {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		batchNumber := idx + 1
		targetFrom := idx*s.cfg.BatchSize + 1
		targetTo := targetFrom + len(targets) - 1
		batchBaseDir := s.cfg.Process.BaseDir
		batchFindingsPath := s.cfg.FindingsPath
		if totalBatches > 1 {
			batchBaseDir = filepath.Join(s.cfg.Process.BaseDir, "batches", fmt.Sprintf("batch-%05d", idx))
			if s.cfg.AutoFindingsPath {
				batchFindingsPath = ""
			} else if batchFindingsPath != "" {
				ext := filepath.Ext(batchFindingsPath)
				name := batchFindingsPath[:len(batchFindingsPath)-len(ext)]
				batchFindingsPath = fmt.Sprintf("%s-batch-%05d%s", name, idx, ext)
			}
		}

		console.Batchf(
			"%d/%d start targets=%d range=%d-%d dir=%s",
			batchNumber,
			totalBatches,
			len(targets),
			targetFrom,
			targetTo,
			console.Highlight(batchBaseDir),
		)

		runCfg := s.cfg
		runCfg.Scan.Targets = append([]string(nil), targets...)
		runCfg.Process.BaseDir = batchBaseDir
		runCfg.FindingsPath = batchFindingsPath
		runCfg.AutoFindingsPath = batchFindingsPath == ""

		stats, err := s.runSingle(ctx, runCfg)
		stats.BatchIndex = batchNumber
		stats.BatchTotal = totalBatches
		stats.BatchFrom = targetFrom
		stats.BatchTo = targetTo

		overall.Targets += stats.Targets
		overall.Discovered += stats.Discovered
		overall.Processed += stats.Processed
		overall.Skipped += stats.Skipped
		overall.Failed += stats.Failed
		overall.Hits += stats.Hits
		overall.Verified += stats.Verified
		overall.Notified += stats.Notified

		if writeErr := writeBatchSummary(batchBaseDir, stats); writeErr != nil {
			console.Warnf("batch %d/%d summary write failed: %v", batchNumber, totalBatches, writeErr)
		}

		if err != nil {
			failedBatches++
			console.Warnf(
				"batch %d/%d failed range=%d-%d err=%v %s",
				batchNumber,
				totalBatches,
				targetFrom,
				targetTo,
				err,
				console.Dim(fmt.Sprintf("(hits=%d verified=%d notified=%d)", stats.Hits, stats.Verified, stats.Notified)),
			)
		} else {
			console.Batchf(
				"%d/%d done targets=%d findings=%d processed=%d skipped=%d failed=%d hits=%d verified=%d notified=%d",
				batchNumber,
				totalBatches,
				len(targets),
				stats.Discovered,
				stats.Processed,
				stats.Skipped,
				stats.Failed,
				stats.Hits,
				stats.Verified,
				stats.Notified,
			)
		}

		console.Batchf(
			"overall %d/%d done targets=%d findings=%d processed=%d skipped=%d map_failed=%d batch_failed=%d hits=%d verified=%d notified=%d",
			batchNumber,
			totalBatches,
			overall.Targets,
			overall.Discovered,
			overall.Processed,
			overall.Skipped,
			overall.Failed,
			failedBatches,
			overall.Hits,
			overall.Verified,
			overall.Notified,
		)
	}

	if failedBatches > 0 {
		return fmt.Errorf("pipeline finished with %d failed batch(es)", failedBatches)
	}

	console.Successf(
		"pipeline all batches done batches=%d targets=%d findings=%d processed=%d skipped=%d map_failed=%d batch_failed=%d hits=%d verified=%d notified=%d base_dir=%s",
		totalBatches,
		overall.Targets,
		overall.Discovered,
		overall.Processed,
		overall.Skipped,
		overall.Failed,
		failedBatches,
		overall.Hits,
		overall.Verified,
		overall.Notified,
		s.cfg.Process.BaseDir,
	)

	return nil
}

func (s *Service) runSingle(ctx context.Context, cfg Config) (runStats, error) {
	findingsPath, err := resolveFindingsPath(cfg)
	if err != nil {
		return runStats{}, err
	}

	if err := os.MkdirAll(filepath.Dir(findingsPath), 0o755); err != nil {
		return runStats{}, err
	}

	scanCfg := cfg.Scan
	scanCfg.OutputPath = findingsPath
	processCfg := cfg.Process
	processCfg.InputPath = findingsPath

	findingsCh := make(chan model.Finding, max(32, processCfg.ProcessWorkers*8))
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var discoveredCount atomic.Int64
	processErrCh := make(chan error, 1)

	processSvc, err := process.NewService(processCfg)
	if err != nil {
		return runStats{}, err
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
		return runStats{}, err
	}

	console.Pipelinef(
		"stream start targets=%d findings=%s base_dir=%s process_workers=%d",
		len(cfg.Scan.Targets),
		console.Highlight(findingsPath),
		console.Highlight(cfg.Process.BaseDir),
		cfg.Process.ProcessWorkers,
	)

	scanErr := scanSvc.Run(streamCtx)
	close(findingsCh)
	processErr := <-processErrCh

	if scanErr != nil {
		cancel()
	}

	processStats := processSvc.Stats()
	stats := runStats{
		Targets:      len(cfg.Scan.Targets),
		Discovered:   discoveredCount.Load(),
		Processed:    processStats.SuccessFindings,
		Skipped:      processStats.SkippedFindings,
		Failed:       processStats.FailedFindings,
		Hits:         processStats.HitsTotal,
		Verified:     processStats.VerifiedHits,
		Notified:     processStats.Notified,
		FindingsPath: findingsPath,
		BaseDir:      cfg.Process.BaseDir,
	}

	if err := firstMeaningfulError(scanErr, processErr); err != nil {
		return stats, err
	}

	console.Successf(
		"pipeline done targets=%d findings=%d processed=%d skipped=%d map_failed=%d hits=%d verified=%d notified=%d path=%s base_dir=%s",
		len(cfg.Scan.Targets),
		discoveredCount.Load(),
		processStats.SuccessFindings,
		processStats.SkippedFindings,
		processStats.FailedFindings,
		processStats.HitsTotal,
		processStats.VerifiedHits,
		processStats.Notified,
		findingsPath,
		cfg.Process.BaseDir,
	)

	return stats, nil
}

func resolveFindingsPath(cfg Config) (string, error) {
	if cfg.FindingsPath != "" {
		return filepath.Clean(cfg.FindingsPath), nil
	}

	name := fmt.Sprintf("findings-%s.jsonl", time.Now().UTC().Format("20060102-150405"))
	return filepath.Join(cfg.Process.BaseDir, "findings", name), nil
}

func writeBatchSummary(baseDir string, stats runStats) error {
	if err := os.MkdirAll(filepath.Join(baseDir, "results"), 0o755); err != nil {
		return err
	}

	summary := batchSummary{
		BatchIndex:   stats.BatchIndex,
		BatchTotal:   stats.BatchTotal,
		TargetFrom:   stats.BatchFrom,
		TargetTo:     stats.BatchTo,
		Targets:      stats.Targets,
		Discovered:   stats.Discovered,
		Processed:    stats.Processed,
		Skipped:      stats.Skipped,
		Failed:       stats.Failed,
		Hits:         stats.Hits,
		Verified:     stats.Verified,
		Notified:     stats.Notified,
		FinishedAt:   time.Now().UTC().Format(time.RFC3339),
		BaseDir:      stats.BaseDir,
		FindingsPath: stats.FindingsPath,
	}

	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(baseDir, "results", "batch-summary.json")
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func splitTargets(targets []string, batchSize int) [][]string {
	if len(targets) == 0 {
		return nil
	}
	if batchSize <= 0 || len(targets) <= batchSize {
		return [][]string{append([]string(nil), targets...)}
	}

	batches := make([][]string, 0, (len(targets)+batchSize-1)/batchSize)
	for start := 0; start < len(targets); start += batchSize {
		end := start + batchSize
		if end > len(targets) {
			end = len(targets)
		}
		batches = append(batches, append([]string(nil), targets[start:end]...))
	}
	return batches
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
