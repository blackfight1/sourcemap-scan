package katana

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"sourcemap-scan/internal/app"
)

type Runner struct {
	cfg    app.Config
	target string
}

type jsonlRow struct {
	Request struct {
		Endpoint string `json:"endpoint"`
	} `json:"request"`
}

func NewRunner(cfg app.Config, target string) *Runner {
	return &Runner{cfg: cfg, target: target}
}

func (r *Runner) CollectJSURLs(ctx context.Context) ([]string, error) {
	args := []string{
		"-u", r.target,
		"-jsonl",
		"-silent",
		"-jc",
		"-eof", "raw,body",
		"-d", strconv.Itoa(r.cfg.KatanaDepth),
		"-c", strconv.Itoa(r.cfg.KatanaConcurrency),
		"-p", strconv.Itoa(r.cfg.KatanaParallelism),
		"-rl", strconv.Itoa(r.cfg.KatanaRateLimit),
	}

	// Whole-crawl budget (katana native). Also enforced via CommandContext below.
	runCtx := ctx
	var cancel context.CancelFunc
	if r.cfg.KatanaTimeout > 0 {
		args = append(args, "-ct", formatKatanaDuration(r.cfg.KatanaTimeout))
		// Grace so katana can exit after -ct before we SIGKILL the process.
		runCtx, cancel = context.WithTimeout(ctx, r.cfg.KatanaTimeout+30*time.Second)
		defer cancel()
	}
	args = append(args, r.cfg.KatanaExtraArgs...)

	cmd := exec.CommandContext(runCtx, r.cfg.KatanaBin, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting katana: %w", err)
	}

	var stderrText string
	var urls []string
	var stderrErr error
	var parseErr error

	var readWG sync.WaitGroup
	readWG.Add(2)

	go func() {
		defer readWG.Done()
		stderrText, stderrErr = readAllText(stderr)
	}()

	go func() {
		defer readWG.Done()
		urls, parseErr = parseJSURLs(stdout)
	}()

	readWG.Wait()
	waitErr := cmd.Wait()

	if stderrErr != nil {
		return nil, stderrErr
	}
	if parseErr != nil {
		return nil, parseErr
	}
	if waitErr != nil {
		if runCtx.Err() != nil {
			// Timed out: return whatever JS we already parsed (partial OK).
			if len(urls) > 0 {
				return urls, nil
			}
			return nil, fmt.Errorf("katana timed out after %s: %w", r.cfg.KatanaTimeout, runCtx.Err())
		}
		if strings.TrimSpace(stderrText) != "" {
			return nil, fmt.Errorf("katana failed: %v: %s", waitErr, strings.TrimSpace(stderrText))
		}
		return nil, fmt.Errorf("katana failed: %w", waitErr)
	}

	return urls, nil
}

// formatKatanaDuration formats for katana -ct (s/m/h/d). Prefer whole minutes/seconds.
func formatKatanaDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	secs := int(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return fmt.Sprintf("%ds", secs)
}

func parseJSURLs(r io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	seen := make(map[string]struct{})
	var urls []string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var row jsonlRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}

		endpoint := strings.TrimSpace(row.Request.Endpoint)
		if endpoint == "" || !looksLikeJS(endpoint) {
			continue
		}

		if _, ok := seen[endpoint]; ok {
			continue
		}
		seen[endpoint] = struct{}{}
		urls = append(urls, endpoint)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return urls, nil
}

func readAllText(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func looksLikeJS(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	ext := strings.ToLower(path.Ext(parsed.Path))
	return ext == ".js" || ext == ".mjs"
}
