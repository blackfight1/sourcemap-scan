package process

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sourcemap-scan/internal/console"
	"sourcemap-scan/internal/model"
)

type Service struct {
	cfg        Config
	httpClient *http.Client
	stateMu    sync.Mutex
	processed  map[string]struct{}
	inFlight   map[string]struct{}
	summaryMu  sync.Mutex
	statsMu    sync.Mutex
	stats      Stats
}

type Stats struct {
	TotalFindings   int64
	SuccessFindings int64
	SkippedFindings int64
	FailedFindings  int64
	HitsTotal       int64
	VerifiedHits    int64
	Notified        int64
}

type processResult struct {
	Outcome      string
	HitsTotal    int
	VerifiedHits int
	Notified     int
}

func NewService(cfg Config) (*Service, error) {
	return &Service{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		processed: make(map[string]struct{}),
		inFlight:  make(map[string]struct{}),
	}, nil
}

func (s *Service) Run(ctx context.Context) error {
	if err := s.ensureLayout(); err != nil {
		return err
	}

	findings, err := s.loadFindings()
	if err != nil {
		return err
	}

	total := len(findings)
	if total == 0 {
		console.Skipf("process no findings to process")
		return nil
	}

	inputCh := make(chan model.Finding)
	go func() {
		defer close(inputCh)
		for _, finding := range findings {
			select {
			case <-ctx.Done():
				return
			case inputCh <- finding:
			}
		}
	}()

	return s.runQueue(ctx, inputCh, total)
}

func (s *Service) RunStream(ctx context.Context, findings <-chan model.Finding) error {
	if err := s.ensureLayout(); err != nil {
		return err
	}
	return s.runQueue(ctx, findings, 0)
}

func (s *Service) Stats() Stats {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	return s.stats
}

func (s *Service) runQueue(ctx context.Context, findings <-chan model.Finding, total int) error {
	if total == 0 {
		console.Processf(
			"stream start workers=%d base_dir=%s",
			s.cfg.ProcessWorkers,
			s.cfg.BaseDir,
		)
	}

	var startedCount atomic.Int64
	var completedCount atomic.Int64
	var successCount atomic.Int64
	var skippedCount atomic.Int64
	var failureCount atomic.Int64
	var hitsTotalCount atomic.Int64
	var verifiedHitsCount atomic.Int64
	var notifiedCount atomic.Int64

	var workerWG sync.WaitGroup
	for workerID := 0; workerID < s.cfg.ProcessWorkers; workerID++ {
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			for finding := range findings {
				select {
				case <-ctx.Done():
					return
				default:
				}

				current := startedCount.Add(1)
				s.logFindingStart(current, total, finding)

				result, err := s.processFinding(ctx, finding)
				completed := completedCount.Add(1)
				if err != nil {
					failed := failureCount.Add(1)
					s.logFindingFailure(current, total, completed, successCount.Load(), skippedCount.Load(), failed, finding, err)
					continue
				}

				switch result.Outcome {
				case "skipped":
					skipped := skippedCount.Add(1)
					s.logFindingSkipped(current, total, completed, successCount.Load(), skipped, failureCount.Load(), finding)
				default:
					hitsTotal := hitsTotalCount.Add(int64(result.HitsTotal))
					verifiedHits := verifiedHitsCount.Add(int64(result.VerifiedHits))
					notified := notifiedCount.Add(int64(result.Notified))
					success := successCount.Add(1)
					s.logFindingDone(current, total, completed, success, skippedCount.Load(), failureCount.Load(), finding, result, hitsTotal, verifiedHits, notified)
				}
			}
		}()
	}

	workerWG.Wait()

	if err := ctx.Err(); err != nil && total == 0 && startedCount.Load() == 0 {
		return err
	}

	if total == 0 && startedCount.Load() == 0 {
		console.Skipf("process no findings to process")
		return nil
	}

	s.storeStats(Stats{
		TotalFindings:   startedCount.Load(),
		SuccessFindings: successCount.Load(),
		SkippedFindings: skippedCount.Load(),
		FailedFindings:  failureCount.Load(),
		HitsTotal:       hitsTotalCount.Load(),
		VerifiedHits:    verifiedHitsCount.Load(),
		Notified:        notifiedCount.Load(),
	})

	switch {
	case failureCount.Load() > 0:
		console.Warnf(
			"process findings total=%d success=%d skipped=%d failed=%d hits=%d verified=%d notified=%d",
			startedCount.Load(),
			successCount.Load(),
			skippedCount.Load(),
			failureCount.Load(),
			hitsTotalCount.Load(),
			verifiedHitsCount.Load(),
			notifiedCount.Load(),
		)
	case successCount.Load() > 0:
		console.Successf(
			"process findings total=%d success=%d skipped=%d failed=%d hits=%d verified=%d notified=%d",
			startedCount.Load(),
			successCount.Load(),
			skippedCount.Load(),
			failureCount.Load(),
			hitsTotalCount.Load(),
			verifiedHitsCount.Load(),
			notifiedCount.Load(),
		)
	default:
		console.Processf(
			"process findings total=%d success=%d skipped=%d failed=%d hits=%d verified=%d notified=%d",
			startedCount.Load(),
			successCount.Load(),
			skippedCount.Load(),
			failureCount.Load(),
			hitsTotalCount.Load(),
			verifiedHitsCount.Load(),
			notifiedCount.Load(),
		)
	}

	if failureCount.Load() > 0 {
		return fmt.Errorf("processing failed for %d finding(s)", failureCount.Load())
	}

	return ctx.Err()
}

func (s *Service) loadFindings() ([]model.Finding, error) {
	file, err := os.Open(s.cfg.InputPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	findings := make([]model.Finding, 0, 64)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var finding model.Finding
		if err := json.Unmarshal([]byte(line), &finding); err != nil {
			console.Skipf("process skip invalid finding JSON: %v", err)
			continue
		}
		findings = append(findings, finding)
	}

	return findings, scanner.Err()
}

func (s *Service) ensureLayout() error {
	dirs := []string{
		filepath.Join(s.cfg.BaseDir, "findings"),
		filepath.Join(s.cfg.BaseDir, "results"),
		filepath.Join(s.cfg.BaseDir, "state"),
	}
	if s.cfg.KeepArtifacts {
		dirs = append(dirs, filepath.Join(s.cfg.BaseDir, "work"))
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	statePath := filepath.Join(s.cfg.BaseDir, "state", "processed-maps.txt")
	if err := touchFile(statePath); err != nil {
		return err
	}
	return s.loadProcessedState(statePath)
}

func (s *Service) processFinding(ctx context.Context, finding model.Finding) (processResult, error) {
	if strings.TrimSpace(finding.Target) == "" || strings.TrimSpace(finding.MapURL) == "" {
		return processResult{}, errors.New("finding missing target or map_url")
	}

	claimed, err := s.beginMap(finding.MapURL)
	if err != nil {
		return processResult{}, err
	}
	if !claimed {
		return processResult{Outcome: "skipped"}, nil
	}
	defer s.finishMap(finding.MapURL, false)

	if s.cfg.OnlyWithSourcesContent && !finding.HasSourcesContent {
		if err := s.markProcessedMap(finding.MapURL); err != nil {
			return processResult{}, err
		}
		return processResult{Outcome: "skipped"}, nil
	}

	workDir := s.workDirForFinding(finding)
	mapDir := filepath.Join(workDir, "map")
	restoredDir := filepath.Join(workDir, "restored")

	tempDir, err := os.MkdirTemp(filepath.Join(s.cfg.BaseDir, "results"), "maprun-")
	if err != nil {
		return processResult{}, err
	}
	defer os.RemoveAll(tempDir)

	if s.cfg.KeepArtifacts {
		console.Stagef("process", finding.MapURL, "prepare", "tempdir=%s", tempDir)
	} else {
		console.Stagef("process", finding.MapURL, "prepare", "tempdir=ephemeral")
	}

	mapFilePath := filepath.Join(tempDir, mapFileName(finding.MapURL))
	restoredDir = filepath.Join(tempDir, "restored")
	if err := os.MkdirAll(restoredDir, 0o755); err != nil {
		return processResult{}, err
	}
	console.Stagef("process", finding.MapURL, "download", "")
	if err := s.downloadMap(ctx, finding.MapURL, mapFilePath); err != nil {
		return processResult{}, fmt.Errorf("download map: %w", err)
	}

	console.Stagef("process", finding.MapURL, "shuji", "")
	if err := s.runShuji(ctx, mapFilePath, restoredDir); err != nil {
		return processResult{}, fmt.Errorf("shuji: %w", err)
	}

	console.Stagef("process", finding.MapURL, "trufflehog", "")
	hits, err := s.runTruffleHog(ctx, restoredDir, filepath.Join(workDir, "trufflehog.raw.jsonl"))
	if err != nil {
		return processResult{}, fmt.Errorf("trufflehog: %w", err)
	}

	if s.cfg.KeepArtifacts {
		mapDir = filepath.Join(workDir, "map")
		console.Stagef("process", finding.MapURL, "archive", "workdir=%s", workDir)
		if err := os.MkdirAll(mapDir, 0o755); err != nil {
			return processResult{}, err
		}
		if err := os.MkdirAll(filepath.Join(workDir, "restored"), 0o755); err != nil {
			return processResult{}, err
		}
		if err := copyFile(mapFilePath, filepath.Join(mapDir, filepath.Base(mapFilePath))); err != nil {
			return processResult{}, err
		}
		if s.cfg.KeepRestored {
			if err := copyDir(restoredDir, filepath.Join(workDir, "restored")); err != nil {
				return processResult{}, err
			}
		}
	}

	console.Stagef("process", finding.MapURL, "analyze", "hits=%d", len(hits))
	summaryResult, err := s.buildSummary(ctx, finding, hits)
	if err != nil {
		return processResult{}, err
	}
	if err := s.appendSummary(summaryResult); err != nil {
		return processResult{}, err
	}
	if s.cfg.KeepArtifacts {
		if err := s.writeSummary(filepath.Join(workDir, "summary.json"), summaryResult); err != nil {
			return processResult{}, err
		}
	}

	if err := s.markProcessedMap(finding.MapURL); err != nil {
		return processResult{}, err
	}

	if !s.cfg.KeepRestored {
		console.Stagef("process", finding.MapURL, "cleanup", "restored=false")
	}

	s.finishMap(finding.MapURL, true)
	return processResult{
		Outcome:      "processed",
		HitsTotal:    summaryResult.HitsTotal,
		VerifiedHits: summaryResult.VerifiedHits,
		Notified:     summaryResult.Notified,
	}, nil
}

func (s *Service) buildSummary(ctx context.Context, finding model.Finding, hits []truffleHogHit) (summary, error) {
	result := summary{
		Target:            finding.Target,
		TargetHost:        sanitizeHost(finding.Target),
		AssetURL:          finding.AssetURL,
		MapURL:            finding.MapURL,
		File:              finding.File,
		DiscoveredBy:      finding.DiscoveredBy,
		SourcesCount:      finding.SourcesCount,
		NamesCount:        finding.NamesCount,
		HasSourcesContent: finding.HasSourcesContent,
		ProcessedAt:       time.Now().UTC(),
		RestoreSuccess:    true,
		TruffleHogSuccess: true,
		Hits:              make([]classifiedHit, 0, len(hits)),
	}

	for _, hit := range hits {
		entry := classifiedHit{
			Detector: firstNonEmpty(hit.DetectorName, hit.DetectorType, "unknown"),
			Verified: hit.Verified,
			FilePath: hitFilePath(hit),
			Redacted: hit.Redacted,
		}

		result.HitsTotal++
		if hit.Verified {
			result.VerifiedHits++
		}

		if hit.Verified {
			sent, err := s.notifyFeishu(ctx, finding, hit)
			if err != nil {
				console.Warnf("process %s notify failed detector=%s err=%v", finding.MapURL, entry.Detector, err)
			} else if sent {
				result.Notified++
				entry.Notified = true
				console.Hitf(
					"%s verified secret detector=%s notified=true",
					console.Highlight(finding.MapURL),
					console.Highlight(entry.Detector),
				)
			}
		}

		result.Hits = append(result.Hits, entry)
	}

	return result, nil
}

func (s *Service) writeSummary(path string, value summary) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (s *Service) appendSummary(value summary) error {
	path := filepath.Join(s.cfg.BaseDir, "results", "summaries.jsonl")

	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	s.summaryMu.Lock()
	defer s.summaryMu.Unlock()

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write(append(data, '\n'))
	return err
}

func (s *Service) runShuji(ctx context.Context, mapFilePath string, restoredDir string) error {
	cmd := exec.CommandContext(ctx, s.cfg.ShujiBin, mapFilePath, "-o", restoredDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (s *Service) runTruffleHog(ctx context.Context, restoredDir string, rawOutputPath string) ([]truffleHogHit, error) {
	args := []string{"filesystem", restoredDir, "--json"}
	args = append(args, s.cfg.TruffleHogExtraArgs...)

	cmd := exec.CommandContext(ctx, s.cfg.TruffleHogBin, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}

	if s.cfg.KeepArtifacts {
		if err := os.WriteFile(rawOutputPath, output, 0o644); err != nil {
			return nil, err
		}
	}

	var hits []truffleHogHit
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}

		var hit truffleHogHit
		if err := json.Unmarshal([]byte(line), &hit); err != nil {
			continue
		}

		if firstNonEmpty(hit.DetectorName, hit.DetectorType) == "" {
			continue
		}

		hits = append(hits, hit)
	}

	return hits, scanner.Err()
}

func (s *Service) downloadMap(ctx context.Context, mapURL string, destination string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mapURL, nil)
	if err != nil {
		return err
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	return err
}

func (s *Service) workDirForFinding(finding model.Finding) string {
	host := sanitizeHost(finding.Target)
	hash := shortHash(finding.MapURL)
	return filepath.Join(s.cfg.BaseDir, "work", host, hash)
}

func (s *Service) processedStatePath() string {
	return filepath.Join(s.cfg.BaseDir, "state", "processed-maps.txt")
}

func (s *Service) loadProcessedState(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	s.processed = make(map[string]struct{})
	for _, line := range strings.Split(string(data), "\n") {
		value := strings.TrimSpace(line)
		if value == "" {
			continue
		}
		s.processed[value] = struct{}{}
	}

	return nil
}

func (s *Service) appendProcessedMap(mapURL string) error {
	file, err := os.OpenFile(s.processedStatePath(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = fmt.Fprintln(file, mapURL)
	return err
}

func (s *Service) beginMap(mapURL string) (bool, error) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	if _, ok := s.processed[mapURL]; ok {
		return false, nil
	}
	if _, ok := s.inFlight[mapURL]; ok {
		return false, nil
	}
	s.inFlight[mapURL] = struct{}{}
	return true, nil
}

func (s *Service) finishMap(mapURL string, success bool) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	delete(s.inFlight, mapURL)
	if success {
		s.processed[mapURL] = struct{}{}
	}
}

func (s *Service) markProcessedMap(mapURL string) error {
	s.stateMu.Lock()
	if _, ok := s.processed[mapURL]; ok {
		delete(s.inFlight, mapURL)
		s.stateMu.Unlock()
		return nil
	}
	s.stateMu.Unlock()

	if err := s.appendProcessedMap(mapURL); err != nil {
		return err
	}

	s.finishMap(mapURL, true)
	return nil
}

func (s *Service) notifyFeishu(ctx context.Context, finding model.Finding, hit truffleHogHit) (bool, error) {
	if strings.TrimSpace(s.cfg.FeishuWebhook) == "" {
		return false, nil
	}

	detector := firstNonEmpty(hit.DetectorName, hit.DetectorType, "unknown")
	filePath := hitFilePath(hit)
	redacted := firstNonEmpty(strings.TrimSpace(hit.Redacted), "(empty)")
	assetURL := firstNonEmpty(strings.TrimSpace(finding.AssetURL), "(unknown)")
	fileName := firstNonEmpty(strings.TrimSpace(finding.File), "(unknown)")
	processedAt := time.Now().UTC().Format(time.RFC3339)

	payload := map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"config": map[string]any{
				"wide_screen_mode": true,
				"enable_forward":   true,
			},
			"header": map[string]any{
				"template": "red",
				"title": map[string]any{
					"tag":     "plain_text",
					"content": "VERIFIED SECRET",
				},
			},
			"elements": []map[string]any{
				{
					"tag": "markdown",
					"content": strings.Join([]string{
						fmt.Sprintf("**Target**: %s", finding.Target),
						fmt.Sprintf("**Detector**: `%s`", detector),
						fmt.Sprintf("**Verified**: `true`"),
						fmt.Sprintf("**Secret**: `%s`", redacted),
					}, "\n"),
				},
				{
					"tag": "hr",
				},
				{
					"tag": "markdown",
					"content": strings.Join([]string{
						fmt.Sprintf("**Asset**: %s", assetURL),
						fmt.Sprintf("**Map**: %s", finding.MapURL),
						fmt.Sprintf("**Bundle File**: `%s`", fileName),
						fmt.Sprintf("**Source File**: `%s`", filePath),
					}, "\n"),
				},
				{
					"tag": "note",
					"elements": []map[string]any{
						{
							"tag":     "plain_text",
							"content": fmt.Sprintf("Time: %s UTC", processedAt),
						},
					},
				},
				{
					"tag": "action",
					"actions": []map[string]any{
						{
							"tag": "button",
							"text": map[string]any{
								"tag":     "plain_text",
								"content": "Open Map",
							},
							"type": "primary",
							"url":  finding.MapURL,
						},
						{
							"tag": "button",
							"text": map[string]any{
								"tag":     "plain_text",
								"content": "Open Asset",
							},
							"type": "default",
							"url":  assetURL,
						},
					},
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.FeishuWebhook, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	return true, nil
}

func sanitizeHost(target string) string {
	target = strings.TrimSpace(target)
	target = strings.TrimPrefix(target, "http://")
	target = strings.TrimPrefix(target, "https://")
	target = strings.Split(target, "/")[0]
	target = strings.Split(target, ":")[0]
	if target == "" {
		return "unknown"
	}
	return target
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
}

func mapFileName(mapURL string) string {
	name := filepath.Base(strings.Split(mapURL, "?")[0])
	if name == "." || name == "/" || name == "" {
		return "map.json"
	}
	return name
}

func hitFilePath(hit truffleHogHit) string {
	return firstNonEmpty(hit.SourceMeta.Data.Filesystem.File, hit.SourceMeta.Data.Filesystem.Path, "unknown")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func touchFile(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	return file.Close()
}

func copyFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func copyDir(src string, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}

	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == src {
			return nil
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		return copyFile(path, target)
	})
}

func (s *Service) storeStats(stats Stats) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.stats = stats
}

func (s *Service) logFindingStart(current int64, total int, finding model.Finding) {
	if total > 0 {
		console.Processf(
			"finding %d/%d start target=%s map=%s",
			current,
			total,
			console.Highlight(finding.Target),
			console.Highlight(finding.MapURL),
		)
		return
	}

	console.Processf(
		"stream finding %d start target=%s map=%s",
		current,
		console.Highlight(finding.Target),
		console.Highlight(finding.MapURL),
	)
}

func (s *Service) logFindingFailure(current int64, total int, completed int64, success int64, skipped int64, failed int64, finding model.Finding, err error) {
	if total > 0 {
		console.Failf(
			"process finding %d/%d failed target=%s map=%s err=%v %s",
			current,
			total,
			finding.Target,
			finding.MapURL,
			err,
			console.Dim(fmt.Sprintf("(completed=%d/%d success=%d skipped=%d failed=%d)", completed, total, success, skipped, failed)),
		)
		return
	}

	console.Failf(
		"process stream finding %d failed target=%s map=%s err=%v %s",
		current,
		finding.Target,
		finding.MapURL,
		err,
		console.Dim(fmt.Sprintf("(completed=%d success=%d skipped=%d failed=%d)", completed, success, skipped, failed)),
	)
}

func (s *Service) logFindingSkipped(current int64, total int, completed int64, success int64, skipped int64, failed int64, finding model.Finding) {
	if total > 0 {
		console.Skipf(
			"process finding %d/%d skipped target=%s map=%s %s",
			current,
			total,
			finding.Target,
			finding.MapURL,
			console.Dim(fmt.Sprintf("(completed=%d/%d success=%d skipped=%d failed=%d)", completed, total, success, skipped, failed)),
		)
		return
	}

	console.Skipf(
		"process stream finding %d skipped target=%s map=%s %s",
		current,
		finding.Target,
		finding.MapURL,
		console.Dim(fmt.Sprintf("(completed=%d success=%d skipped=%d failed=%d)", completed, success, skipped, failed)),
	)
}

func (s *Service) logFindingDone(current int64, total int, completed int64, success int64, skipped int64, failed int64, finding model.Finding, result processResult, hitsTotal int64, verifiedHits int64, notified int64) {
	if total > 0 {
		console.Successf(
			"finding %d/%d done target=%s map=%s hits=%d verified=%d notified=%d %s",
			current,
			total,
			finding.Target,
			finding.MapURL,
			result.HitsTotal,
			result.VerifiedHits,
			result.Notified,
			console.Dim(fmt.Sprintf("(completed=%d/%d success=%d skipped=%d failed=%d total_hits=%d verified_total=%d notified_total=%d)", completed, total, success, skipped, failed, hitsTotal, verifiedHits, notified)),
		)
		return
	}

	console.Successf(
		"stream finding %d done target=%s map=%s hits=%d verified=%d notified=%d %s",
		current,
		finding.Target,
		finding.MapURL,
		result.HitsTotal,
		result.VerifiedHits,
		result.Notified,
		console.Dim(fmt.Sprintf("(completed=%d success=%d skipped=%d failed=%d total_hits=%d verified_total=%d notified_total=%d)", completed, success, skipped, failed, hitsTotal, verifiedHits, notified)),
	)
}
