package scan

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"sourcemap-scan/internal/app"
	"sourcemap-scan/internal/console"
	"sourcemap-scan/internal/httpx"
	"sourcemap-scan/internal/jscollect"
	"sourcemap-scan/internal/model"
	"sourcemap-scan/internal/output"
	"sourcemap-scan/internal/sourcemap"
)

type Service struct {
	cfg        app.Config
	httpClient *httpx.Client
	onFinding  func(context.Context, model.Finding) error
}

func NewService(cfg app.Config) (*Service, error) {
	return NewServiceWithEmitter(cfg, nil)
}

func NewServiceWithEmitter(cfg app.Config, onFinding func(context.Context, model.Finding) error) (*Service, error) {
	return &Service{
		cfg:        cfg,
		httpClient: httpx.New(cfg.HTTPTimeout, cfg.UserAgent),
		onFinding:  onFinding,
	}, nil
}

func (s *Service) Run(ctx context.Context) error {
	writer, err := output.NewJSONLWriter(s.cfg.OutputPath)
	if err != nil {
		return err
	}
	defer writer.Close()

	jobs := make(chan string)
	var workerWG sync.WaitGroup
	var successCount atomic.Int64
	var failureCount atomic.Int64
	var startedCount atomic.Int64
	var activeCount atomic.Int64

	doneSummary := make(chan struct{})
	go func() {
		if s.cfg.Verbose || len(s.cfg.Targets) <= 1 {
			return
		}
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-doneSummary:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				done := successCount.Load() + failureCount.Load()
				total := int64(len(s.cfg.Targets))
				left := total - done - activeCount.Load()
				if left < 0 {
					left = 0
				}
				console.Pipelinef(
					"summary targets done=%d/%d running=%d left=%d failed=%d",
					done,
					total,
					activeCount.Load(),
					left,
					failureCount.Load(),
				)
			}
		}
	}()
	defer close(doneSummary)

	for i := 0; i < s.cfg.TargetWorkers; i++ {
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			for target := range jobs {
				if err := ctx.Err(); err != nil {
					return
				}

				current := startedCount.Add(1)
				total := int64(len(s.cfg.Targets))
				activeCount.Add(1)
				console.Scanf(
					"target %d/%d start %s %s",
					current,
					total,
					console.Highlight(target),
					console.Dim(fmt.Sprintf("(remaining=%d)", total-current)),
				)

				if err := s.runTarget(ctx, writer, target); err != nil {
					activeCount.Add(-1)
					failureCount.Add(1)
					completed := successCount.Load() + failureCount.Load()
					console.Failf(
						"target %s failed: %v %s",
						target,
						err,
						console.Dim(fmt.Sprintf("(completed=%d/%d success=%d failed=%d)", completed, total, successCount.Load(), failureCount.Load())),
					)
					continue
				}

				activeCount.Add(-1)
				successCount.Add(1)
				completed := successCount.Load() + failureCount.Load()
				console.Successf(
					"target %s done %s",
					target,
					console.Dim(fmt.Sprintf("(completed=%d/%d success=%d failed=%d)", completed, total, successCount.Load(), failureCount.Load())),
				)
			}
		}()
	}

	for _, target := range s.cfg.Targets {
		select {
		case <-ctx.Done():
			close(jobs)
			workerWG.Wait()
			return ctx.Err()
		case jobs <- target:
		}
	}
	close(jobs)
	workerWG.Wait()

	if err := ctx.Err(); err != nil {
		return err
	}

	if successCount.Load() == 0 && failureCount.Load() > 0 {
		return fmt.Errorf("all targets failed: %d", failureCount.Load())
	}

	if failureCount.Load() > 0 {
		console.Warnf(
			"targets total=%d success=%d failed=%d",
			len(s.cfg.Targets),
			successCount.Load(),
			failureCount.Load(),
		)
	} else {
		console.Successf(
			"targets total=%d success=%d failed=%d",
			len(s.cfg.Targets),
			successCount.Load(),
			failureCount.Load(),
		)
	}

	return nil
}

func (s *Service) runTarget(ctx context.Context, writer *output.JSONLWriter, target string) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic while scanning target: %v", recovered)
			console.Failf("%s panic recovered:\n%s", target, debug.Stack())
		}
	}()

	targetCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var firstErr error
	var firstErrOnce sync.Once
	setTargetErr := func(err error) {
		if err == nil {
			return
		}
		firstErrOnce.Do(func() {
			firstErr = err
			cancel()
		})
	}

	assets, err := jscollect.Collect(targetCtx, s.cfg, target)
	if err != nil {
		return err
	}

	console.Scanf(
		"%s collected %d unique assets (katana/waymore merged)",
		console.Highlight(target),
		len(assets),
	)

	jobs := make(chan jscollect.Asset)
	findings := make(chan model.Finding)

	var workerWG sync.WaitGroup
	var scannedCount atomic.Int64
	var findingCount atomic.Int64

	for i := 0; i < s.cfg.ScanWorkers; i++ {
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			for asset := range jobs {
				func(asset jscollect.Asset) {
					defer func() {
						if recovered := recover(); recovered != nil {
							setTargetErr(fmt.Errorf("panic while scanning asset %s: %v", asset.URL, recovered))
							console.Failf("%s asset panic recovered for %s:\n%s", target, asset.URL, debug.Stack())
						}
					}()

					select {
					case <-targetCtx.Done():
						return
					default:
					}

					scannedCount.Add(1)
					assetFindings, err := s.scanAsset(targetCtx, target, asset)
					if err != nil {
						// Network/validation failures on single assets are non-fatal.
						if s.cfg.Verbose {
							console.Warnf("%s asset skip %s: %v", target, asset.URL, err)
						}
						return
					}

					for _, finding := range assetFindings {
						select {
						case <-targetCtx.Done():
							return
						case findings <- finding:
						}
					}
				}(asset)
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, asset := range assets {
			select {
			case <-targetCtx.Done():
				return
			case jobs <- asset:
			}
		}
	}()

	go func() {
		workerWG.Wait()
		close(findings)
	}()

	for finding := range findings {
		if err := s.emitFinding(targetCtx, writer, finding); err != nil {
			setTargetErr(fmt.Errorf("writing finding failed: %w", err))
			continue
		}
		findingCount.Add(1)
	}

	if firstErr != nil {
		return firstErr
	}

	if findingCount.Load() > 0 {
		console.Successf(
			"%s scanned %d assets, found %d valid source maps",
			target,
			scannedCount.Load(),
			findingCount.Load(),
		)
	} else {
		console.Scanf(
			"%s scanned %d assets, found %d valid source maps",
			target,
			scannedCount.Load(),
			findingCount.Load(),
		)
	}

	return nil
}

func (s *Service) emitFinding(ctx context.Context, writer *output.JSONLWriter, finding model.Finding) error {
	if err := writer.WriteFinding(finding); err != nil {
		return err
	}
	if s.onFinding == nil {
		return nil
	}
	return s.onFinding(ctx, finding)
}

func (s *Service) scanAsset(ctx context.Context, target string, asset jscollect.Asset) ([]model.Finding, error) {
	if asset.Kind == "map" {
		return s.validateMapFinding(ctx, target, asset.URL, asset.URL, "direct_map", "", asset.Source)
	}

	jsResp, err := s.httpClient.FetchTail(ctx, asset.URL, s.cfg.TailBytes, s.cfg.MaxJSBytes)
	if err != nil {
		return nil, err
	}

	candidates := sourcemap.DiscoverExplicitCandidates(jsResp.URL, jsResp.Header, jsResp.Body)
	if len(candidates) == 0 {
		if guessed, ok := sourcemap.GuessAdjacentCandidate(jsResp.URL); ok {
			candidates = append(candidates, guessed)
		}
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{})
	findings := make([]model.Finding, 0, len(candidates))

	for _, candidate := range candidates {
		if _, ok := seen[candidate.URL]; ok {
			continue
		}
		seen[candidate.URL] = struct{}{}

		items, err := s.validateMapFinding(
			ctx,
			target,
			jsResp.URL,
			candidate.URL,
			candidate.Method,
			candidate.SourceMappingValue,
			asset.Source,
		)
		if err != nil {
			continue
		}
		findings = append(findings, items...)
	}

	return findings, nil
}

func (s *Service) validateMapFinding(
	ctx context.Context,
	target string,
	assetURL string,
	mapURL string,
	discoveredBy string,
	sourceMappingRaw string,
	jsSource string,
) ([]model.Finding, error) {
	validation, err := sourcemap.ValidateRemoteMap(ctx, s.httpClient, mapURL, s.cfg.MaxMapBytes)
	if err != nil {
		return nil, err
	}

	return []model.Finding{{
		Target:              target,
		AssetURL:            assetURL,
		MapURL:              validation.FinalURL,
		RequestedMapURL:     mapURL,
		DiscoveredBy:        discoveredBy,
		JSSource:            jsSource,
		SourceMappingURLRaw: sourceMappingRaw,
		StatusCode:          validation.StatusCode,
		ContentType:         validation.ContentType,
		SourcesCount:        validation.SourcesCount,
		NamesCount:          validation.NamesCount,
		HasSourcesContent:   validation.HasSourcesContent,
		File:                validation.File,
	}}, nil
}
