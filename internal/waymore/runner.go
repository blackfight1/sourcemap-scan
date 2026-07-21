package waymore

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"sourcemap-scan/internal/app"
)

// Runner executes waymore to collect historical JS / map URLs for a target host.
type Runner struct {
	cfg    app.Config
	target string
}

func NewRunner(cfg app.Config, target string) *Runner {
	return &Runner{cfg: cfg, target: target}
}

// CollectURLs runs waymore in URL-only mode and returns JS / sourcemap URLs.
func (r *Runner) CollectURLs(ctx context.Context) ([]string, error) {
	host, err := hostFromTarget(r.target)
	if err != nil {
		return nil, err
	}

	tmpDir, err := os.MkdirTemp("", "sourcemap-waymore-*")
	if err != nil {
		return nil, fmt.Errorf("creating waymore temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	outFile := filepath.Join(tmpDir, "urls.txt")
	args := []string{
		"-i", host,
		"-mode", "U",
		"-oU", outFile,
		"-ow",
		// Prefer JS and map URLs to keep volume manageable.
		"-ko", `\.m?js(\?.*|$)|\.js\.map(\?.*|$)`,
	}
	if r.cfg.WaymoreNoSubs {
		args = append(args, "-n")
	}
	args = append(args, r.cfg.WaymoreExtraArgs...)

	runCtx := ctx
	var cancel context.CancelFunc
	if r.cfg.WaymoreTimeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.cfg.WaymoreTimeout)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, r.cfg.WaymoreBin, args...)
	// waymore is noisy; keep stderr for error context only.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting waymore: %w", err)
	}

	stderrText, _ := readAllLimited(stderr, 256*1024)
	waitErr := cmd.Wait()
	if waitErr != nil {
		if runCtx.Err() != nil {
			return nil, fmt.Errorf("waymore timed out or canceled: %w", runCtx.Err())
		}
		msg := strings.TrimSpace(string(stderrText))
		if msg != "" {
			return nil, fmt.Errorf("waymore failed: %v: %s", waitErr, msg)
		}
		return nil, fmt.Errorf("waymore failed: %w", waitErr)
	}

	return readAssetURLs(outFile)
}

func hostFromTarget(target string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("invalid target for waymore: %q", target)
	}
	// Strip port; waymore expects domain-like input.
	host := parsed.Hostname()
	if host == "" {
		return "", fmt.Errorf("invalid target host for waymore: %q", target)
	}
	return host, nil
}

func readAssetURLs(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		// waymore may produce no file when zero results.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	seen := make(map[string]struct{})
	var urls []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Some outputs include tab-separated metadata; take first field.
		if i := strings.IndexAny(line, " \t"); i > 0 {
			line = line[:i]
		}
		if !looksLikeJSOrMap(line) {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		urls = append(urls, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return urls, nil
}

func looksLikeJSOrMap(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	p := strings.ToLower(parsed.Path)
	ext := strings.ToLower(path.Ext(p))
	if ext == ".js" || ext == ".mjs" {
		return true
	}
	// *.js.map
	if strings.HasSuffix(p, ".js.map") || strings.HasSuffix(p, ".mjs.map") || ext == ".map" {
		return true
	}
	return false
}

func readAllLimited(r io.Reader, max int) ([]byte, error) {
	limited := io.LimitReader(r, int64(max))
	return io.ReadAll(limited)
}

