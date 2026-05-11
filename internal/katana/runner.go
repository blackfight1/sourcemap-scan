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
	args = append(args, r.cfg.KatanaExtraArgs...)

	cmd := exec.CommandContext(ctx, r.cfg.KatanaBin, args...)

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
		if strings.TrimSpace(stderrText) != "" {
			return nil, fmt.Errorf("katana failed: %v: %s", waitErr, strings.TrimSpace(stderrText))
		}
		return nil, fmt.Errorf("katana failed: %w", waitErr)
	}

	return urls, nil
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
