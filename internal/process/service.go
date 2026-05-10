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
	"time"

	"sourcemap-scan/internal/model"
)

type Service struct {
	cfg        Config
	httpClient *http.Client
}

func NewService(cfg Config) (*Service, error) {
	return &Service{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
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
		fmt.Fprintln(os.Stderr, "[process] no findings to process")
		return nil
	}

	var completed int
	var successCount int
	var failureCount int
	var skippedCount int

	for idx, finding := range findings {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		fmt.Fprintf(
			os.Stderr,
			"[process] finding %d/%d start target=%s map=%s\n",
			idx+1,
			total,
			finding.Target,
			finding.MapURL,
		)

		outcome, err := s.processFinding(ctx, finding)
		completed++
		if err != nil {
			failureCount++
			fmt.Fprintf(
				os.Stderr,
				"[process] finding %d/%d failed target=%s map=%s err=%v (completed=%d/%d success=%d skipped=%d failed=%d)\n",
				idx+1,
				total,
				finding.Target,
				finding.MapURL,
				err,
				completed,
				total,
				successCount,
				skippedCount,
				failureCount,
			)
			continue
		}

		switch outcome {
		case "skipped":
			skippedCount++
			fmt.Fprintf(
				os.Stderr,
				"[process] finding %d/%d skipped target=%s map=%s (completed=%d/%d success=%d skipped=%d failed=%d)\n",
				idx+1,
				total,
				finding.Target,
				finding.MapURL,
				completed,
				total,
				successCount,
				skippedCount,
				failureCount,
			)
		default:
			successCount++
			fmt.Fprintf(
				os.Stderr,
				"[process] finding %d/%d done target=%s map=%s (completed=%d/%d success=%d skipped=%d failed=%d)\n",
				idx+1,
				total,
				finding.Target,
				finding.MapURL,
				completed,
				total,
				successCount,
				skippedCount,
				failureCount,
			)
		}
	}

	fmt.Fprintf(
		os.Stderr,
		"[process] findings total=%d success=%d skipped=%d failed=%d\n",
		total,
		successCount,
		skippedCount,
		failureCount,
	)

	return nil
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
			fmt.Fprintf(os.Stderr, "[process] skip invalid finding JSON: %v\n", err)
			continue
		}
		findings = append(findings, finding)
	}

	return findings, scanner.Err()
}

func (s *Service) ensureLayout() error {
	dirs := []string{
		filepath.Join(s.cfg.BaseDir, "work"),
		filepath.Join(s.cfg.BaseDir, "state"),
		filepath.Join(s.cfg.BaseDir, "logs"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	return touchFile(filepath.Join(s.cfg.BaseDir, "state", "processed-maps.txt"))
}

func (s *Service) processFinding(ctx context.Context, finding model.Finding) (string, error) {
	if strings.TrimSpace(finding.Target) == "" || strings.TrimSpace(finding.MapURL) == "" {
		return "", errors.New("finding missing target or map_url")
	}

	if s.cfg.OnlyWithSourcesContent && !finding.HasSourcesContent {
		if err := s.appendProcessedMap(finding.MapURL); err != nil {
			return "", err
		}
		return "skipped", nil
	}

	alreadyProcessed, err := s.isProcessedMap(finding.MapURL)
	if err != nil {
		return "", err
	}
	if alreadyProcessed {
		return "skipped", nil
	}

	workDir := s.workDirForFinding(finding)
	mapDir := filepath.Join(workDir, "map")
	restoredDir := filepath.Join(workDir, "restored")
	fmt.Fprintf(os.Stderr, "[process][%s] prepare workdir=%s\n", finding.MapURL, workDir)

	if err := os.MkdirAll(mapDir, 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(restoredDir, 0o755); err != nil {
		return "", err
	}

	mapFilePath := filepath.Join(mapDir, mapFileName(finding.MapURL))
	fmt.Fprintf(os.Stderr, "[process][%s] stage=download\n", finding.MapURL)
	if err := s.downloadMap(ctx, finding.MapURL, mapFilePath); err != nil {
		return "", fmt.Errorf("download map: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[process][%s] stage=shuji\n", finding.MapURL)
	if err := s.runShuji(ctx, mapFilePath, restoredDir); err != nil {
		return "", fmt.Errorf("shuji: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[process][%s] stage=trufflehog\n", finding.MapURL)
	hits, err := s.runTruffleHog(ctx, restoredDir, filepath.Join(workDir, "trufflehog.raw.jsonl"))
	if err != nil {
		return "", fmt.Errorf("trufflehog: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[process][%s] stage=analyze hits=%d\n", finding.MapURL, len(hits))
	summaryResult, err := s.buildSummary(ctx, finding, hits)
	if err != nil {
		return "", err
	}
	if err := s.writeSummary(filepath.Join(workDir, "summary.json"), summaryResult); err != nil {
		return "", err
	}

	if err := s.appendProcessedMap(finding.MapURL); err != nil {
		return "", err
	}

	if !s.cfg.KeepRestored {
		fmt.Fprintf(os.Stderr, "[process][%s] stage=cleanup restored=false\n", finding.MapURL)
		if err := os.RemoveAll(restoredDir); err != nil {
			return "", err
		}
	}

	return "processed", nil
}

func (s *Service) buildSummary(ctx context.Context, finding model.Finding, hits []truffleHogHit) (summary, error) {
	result := summary{
		Target:            finding.Target,
		MapURL:            finding.MapURL,
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
				fmt.Fprintf(os.Stderr, "[process][%s] notify failed detector=%s err=%v\n", finding.MapURL, entry.Detector, err)
			} else if sent {
				result.Notified++
				entry.Notified = true
				fmt.Fprintf(os.Stderr, "[process][%s] notify sent detector=%s verified=true\n", finding.MapURL, entry.Detector)
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

	if err := os.WriteFile(rawOutputPath, output, 0o644); err != nil {
		return nil, err
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

func (s *Service) isProcessedMap(mapURL string) (bool, error) {
	data, err := os.ReadFile(s.processedStatePath())
	if err != nil {
		return false, err
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == mapURL {
			return true, nil
		}
	}
	return false, nil
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

func (s *Service) notifyFeishu(ctx context.Context, finding model.Finding, hit truffleHogHit) (bool, error) {
	if strings.TrimSpace(s.cfg.FeishuWebhook) == "" {
		return false, nil
	}

	payload := map[string]any{
		"msg_type": "text",
		"content": map[string]string{
			"text": fmt.Sprintf(
				"[VERIFIED] Sourcemap Secret Hit\nTarget: %s\nMap: %s\nDetector: %s\nVerified: %t\nFile: %s",
				finding.Target,
				finding.MapURL,
				firstNonEmpty(hit.DetectorName, hit.DetectorType, "unknown"),
				hit.Verified,
				hitFilePath(hit),
			),
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
