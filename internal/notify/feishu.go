package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ScanSummary is the end-of-run report sent to Feishu.
type ScanSummary struct {
	StartedAt              time.Time
	FinishedAt             time.Time
	TargetsTotal           int
	TargetsSuccess         int64
	TargetsFailed          int64
	Findings               int64
	WithSourcesContent     int64
	OutputPath             string
	SampleMapURLs          []string
	DisableWaymore         bool
	DisableKatana          bool
}

// SendFeishuSummary posts a text summary to a Feishu bot webhook.
// Empty webhook is a no-op. Failures are returned to the caller (non-fatal for the scan).
func SendFeishuSummary(ctx context.Context, webhook string, summary ScanSummary) error {
	webhook = strings.TrimSpace(webhook)
	if webhook == "" {
		return nil
	}

	text := buildSummaryText(summary)
	payload := map[string]any{
		"msg_type": "text",
		"content": map[string]string{
			"text": text,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("feishu webhook status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	// Feishu returns 200 with {"code":0,...} on success; surface business errors too.
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(respBody, &result); err == nil && result.Code != 0 {
		return fmt.Errorf("feishu webhook code=%d msg=%s", result.Code, result.Msg)
	}
	return nil
}

func buildSummaryText(s ScanSummary) string {
	duration := s.FinishedAt.Sub(s.StartedAt)
	if duration < 0 {
		duration = 0
	}

	sources := "waymore+katana"
	switch {
	case s.DisableWaymore && !s.DisableKatana:
		sources = "katana only"
	case s.DisableKatana && !s.DisableWaymore:
		sources = "waymore only"
	case s.DisableWaymore && s.DisableKatana:
		sources = "none"
	}

	out := s.OutputPath
	if strings.TrimSpace(out) == "" {
		out = "stdout"
	}

	lines := []string{
		"[sourcemap-scan] 扫描完成",
		fmt.Sprintf("时间: %s → %s", s.StartedAt.Local().Format("2006-01-02 15:04:05"), s.FinishedAt.Local().Format("15:04:05")),
		fmt.Sprintf("耗时: %s", formatDuration(duration)),
		fmt.Sprintf("采集: %s", sources),
		fmt.Sprintf("目标: total=%d success=%d failed=%d", s.TargetsTotal, s.TargetsSuccess, s.TargetsFailed),
		fmt.Sprintf("Sourcemap: 发现 %d 个（含 sourcesContent: %d）", s.Findings, s.WithSourcesContent),
		fmt.Sprintf("输出: %s", out),
	}

	if len(s.SampleMapURLs) > 0 {
		lines = append(lines, "样例 map:")
		for _, u := range s.SampleMapURLs {
			lines = append(lines, "- "+trimForMessage(u, 200))
		}
	} else {
		lines = append(lines, "样例 map: (无)")
	}

	return strings.Join(lines, "\n")
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	sec := int(d.Seconds()) % 60
	if m < 60 {
		return fmt.Sprintf("%dm%ds", m, sec)
	}
	h := m / 60
	m = m % 60
	return fmt.Sprintf("%dh%dm%ds", h, m, sec)
}

func trimForMessage(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "…"
}
