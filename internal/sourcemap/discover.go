package sourcemap

import (
	"bytes"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"sourcemap-scan/internal/model"
)

var sourceMapCommentPattern = regexp.MustCompile(`(?m)//[#@]\s*sourceMappingURL\s*=\s*(\S+)\s*$`)

func DiscoverExplicitCandidates(assetURL string, headers http.Header, tail []byte) []model.Candidate {
	candidates := make([]model.Candidate, 0, 3)

	for _, headerName := range []struct {
		Name   string
		Method string
	}{
		{Name: "SourceMap", Method: "header_sourcemap"},
		{Name: "X-SourceMap", Method: "header_xsourcemap"},
	} {
		rawValue := strings.TrimSpace(headers.Get(headerName.Name))
		if rawValue == "" {
			continue
		}

		resolved, ok := resolveCandidateURL(assetURL, rawValue)
		if !ok {
			continue
		}

		candidates = append(candidates, model.Candidate{
			URL:                resolved,
			Method:             headerName.Method,
			SourceMappingValue: rawValue,
		})
	}

	matches := sourceMapCommentPattern.FindAllSubmatch(bytes.TrimSpace(tail), -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}

		rawValue := strings.TrimSpace(strings.Trim(string(match[1]), `"'`))
		if rawValue == "" {
			continue
		}

		resolved, ok := resolveCandidateURL(assetURL, rawValue)
		if !ok {
			continue
		}

		candidates = append(candidates, model.Candidate{
			URL:                resolved,
			Method:             "js_comment",
			SourceMappingValue: rawValue,
		})
	}

	return candidates
}

func GuessAdjacentCandidate(assetURL string) (model.Candidate, bool) {
	parsed, err := url.Parse(assetURL)
	if err != nil {
		return model.Candidate{}, false
	}

	ext := strings.ToLower(path.Ext(parsed.Path))
	if ext != ".js" && ext != ".mjs" {
		return model.Candidate{}, false
	}

	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = parsed.Path + ".map"

	return model.Candidate{
		URL:    parsed.String(),
		Method: "guessed_adjacent",
	}, true
}

func resolveCandidateURL(assetURL string, rawValue string) (string, bool) {
	rawValue = strings.TrimSpace(rawValue)
	if rawValue == "" {
		return "", false
	}

	parsedValue, err := url.Parse(rawValue)
	if err != nil {
		return "", false
	}
	if strings.EqualFold(parsedValue.Scheme, "data") {
		return "", false
	}

	baseURL, err := url.Parse(assetURL)
	if err != nil {
		return "", false
	}

	return baseURL.ResolveReference(parsedValue).String(), true
}
