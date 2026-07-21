package jscollect

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
	"sync"

	"sourcemap-scan/internal/app"
	"sourcemap-scan/internal/console"
	"sourcemap-scan/internal/katana"
	"sourcemap-scan/internal/waymore"
)

// Asset is a collected JS or sourcemap URL with provenance.
type Asset struct {
	URL    string
	Kind   string // "js" or "map"
	Source string // katana | waymore | both
}

// Collect gathers JS/map URLs for a target using enabled collectors.
// One collector failing does not fail the target if the other succeeds.
func Collect(ctx context.Context, cfg app.Config, target string) ([]Asset, error) {
	type result struct {
		source string
		urls   []string
		err    error
	}

	var collectors []func(context.Context) result
	if !cfg.DisableKatana {
		collectors = append(collectors, func(ctx context.Context) result {
			urls, err := katana.NewRunner(cfg, target).CollectJSURLs(ctx)
			return result{source: "katana", urls: urls, err: err}
		})
	}
	if !cfg.DisableWaymore {
		collectors = append(collectors, func(ctx context.Context) result {
			urls, err := waymore.NewRunner(cfg, target).CollectURLs(ctx)
			return result{source: "waymore", urls: urls, err: err}
		})
	}

	if len(collectors) == 0 {
		return nil, fmt.Errorf("no JS collectors enabled (both -no-katana and -no-waymore)")
	}

	results := make([]result, len(collectors))
	var wg sync.WaitGroup
	for i, fn := range collectors {
		wg.Add(1)
		go func(i int, fn func(context.Context) result) {
			defer wg.Done()
			results[i] = fn(ctx)
		}(i, fn)
	}
	wg.Wait()

	// sourceSets tracks which collectors saw each URL.
	type meta struct {
		sources map[string]struct{}
		kind    string
	}
	merged := make(map[string]*meta)
	var errs []string
	var okCount int

	for _, res := range results {
		if res.err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", res.source, res.err))
			console.Warnf("%s %s collect failed: %v", target, res.source, res.err)
			continue
		}
		okCount++
		console.Scanf("%s %s discovered %d assets", console.Highlight(target), res.source, len(res.urls))
		for _, raw := range res.urls {
			normalized, kind, ok := normalizeAssetURL(raw)
			if !ok {
				continue
			}
			entry, exists := merged[normalized]
			if !exists {
				entry = &meta{sources: make(map[string]struct{}), kind: kind}
				merged[normalized] = entry
			}
			entry.sources[res.source] = struct{}{}
		}
	}

	if okCount == 0 {
		return nil, fmt.Errorf("all JS collectors failed for %s: %s", target, strings.Join(errs, "; "))
	}

	assets := make([]Asset, 0, len(merged))
	for u, m := range merged {
		assets = append(assets, Asset{
			URL:    u,
			Kind:   m.kind,
			Source: joinSources(m.sources),
		})
	}
	return assets, nil
}

func normalizeAssetURL(raw string) (string, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", false
	}
	// Drop fragment only; keep query (build hashes often live in query).
	parsed.Fragment = ""
	p := strings.ToLower(parsed.Path)
	ext := strings.ToLower(path.Ext(p))

	switch {
	case strings.HasSuffix(p, ".js.map") || strings.HasSuffix(p, ".mjs.map") || ext == ".map":
		return parsed.String(), "map", true
	case ext == ".js" || ext == ".mjs":
		return parsed.String(), "js", true
	default:
		return "", "", false
	}
}

func joinSources(sources map[string]struct{}) string {
	hasKatana := false
	hasWaymore := false
	for s := range sources {
		switch s {
		case "katana":
			hasKatana = true
		case "waymore":
			hasWaymore = true
		}
	}
	switch {
	case hasKatana && hasWaymore:
		return "both"
	case hasKatana:
		return "katana"
	case hasWaymore:
		return "waymore"
	default:
		return "unknown"
	}
}
