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
	"sync"

	"sourcemap-scan/internal/app"
	"sourcemap-scan/internal/console"
	"sourcemap-scan/internal/domain"
)

// Runner executes waymore for a domain/host.
type Runner struct {
	cfg    app.Config
	domain string
	noSubs bool
}

// NewRunnerForDomain builds a runner for an explicit domain input to waymore -i.
func NewRunnerForDomain(cfg app.Config, domainName string, noSubs bool) *Runner {
	return &Runner{cfg: cfg, domain: strings.ToLower(strings.TrimSpace(domainName)), noSubs: noSubs}
}

// CollectURLs runs waymore in URL-only mode and returns JS / sourcemap URLs.
func (r *Runner) CollectURLs(ctx context.Context) ([]string, error) {
	if r.domain == "" {
		return nil, fmt.Errorf("empty domain for waymore")
	}

	tmpDir, err := os.MkdirTemp("", "sourcemap-waymore-*")
	if err != nil {
		return nil, fmt.Errorf("creating waymore temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	outFile := filepath.Join(tmpDir, "urls.txt")
	args := []string{
		"-i", r.domain,
		"-mode", "U",
		"-oU", outFile,
		"-ow",
		// Prefer JS and map URLs to keep volume manageable.
		"-ko", `\.m?js(\?.*|$)|\.js\.map(\?.*|$)`,
	}
	if r.noSubs {
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

// PrefetchByRoot runs waymore once per unique registrable root domain (no -n).
// Returns map[rootDomain][]urls.
func PrefetchByRoot(ctx context.Context, cfg app.Config, targets []string) map[string][]string {
	roots := make(map[string]struct{})
	for _, t := range targets {
		host, err := domain.HostFromTarget(t)
		if err != nil {
			continue
		}
		root, err := domain.RootDomain(host)
		if err != nil || root == "" {
			continue
		}
		roots[root] = struct{}{}
	}

	if len(roots) == 0 {
		return map[string][]string{}
	}

	console.Scanf("waymore: unique root domains=%d (one run per root, includes subdomains)", len(roots))

	workers := cfg.TargetWorkers
	if workers < 1 {
		workers = 1
	}
	if workers > len(roots) {
		workers = len(roots)
	}

	type job struct{ root string }
	jobs := make(chan job)
	var mu sync.Mutex
	out := make(map[string][]string, len(roots))

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}
				console.Scanf("waymore root start %s", console.Highlight(j.root))
				urls, err := NewRunnerForDomain(cfg, j.root, false).CollectURLs(ctx)
				if err != nil {
					console.Warnf("waymore root %s failed: %v", j.root, err)
					mu.Lock()
					out[j.root] = nil
					mu.Unlock()
					continue
				}
				console.Scanf("waymore root %s done urls=%d", console.Highlight(j.root), len(urls))
				mu.Lock()
				out[j.root] = urls
				mu.Unlock()
			}
		}()
	}

	for root := range roots {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return out
		case jobs <- job{root: root}:
		}
	}
	close(jobs)
	wg.Wait()
	return out
}

// FilterByHost keeps only URLs whose hostname equals host (case-insensitive).
func FilterByHost(urls []string, host string) []string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || len(urls) == 0 {
		return nil
	}
	var out []string
	for _, raw := range urls {
		if domain.HostOfURL(raw) == host {
			out = append(out, raw)
		}
	}
	return out
}

func readAssetURLs(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
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
	if strings.HasSuffix(p, ".js.map") || strings.HasSuffix(p, ".mjs.map") || ext == ".map" {
		return true
	}
	return false
}

func readAllLimited(r io.Reader, max int) ([]byte, error) {
	limited := io.LimitReader(r, int64(max))
	return io.ReadAll(limited)
}
