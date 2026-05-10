package sourcemap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"sourcemap-scan/internal/httpx"
	"sourcemap-scan/internal/model"
)

var (
	ErrUnexpectedStatus = errors.New("unexpected status code")
	ErrLooksLikeHTML    = errors.New("response looks like HTML")
	ErrInvalidSourceMap = errors.New("response is not a valid source map")
	ErrMissingMapFields = errors.New("source map missing required fields")
)

type file struct {
	Version        int      `json:"version"`
	Sources        []string `json:"sources"`
	Names          []string `json:"names"`
	Mappings       string   `json:"mappings"`
	File           string   `json:"file"`
	SourcesContent []string `json:"sourcesContent"`
}

func ValidateRemoteMap(ctx context.Context, client *httpx.Client, candidateURL string, maxMapBytes int64) (*model.SourceMapValidation, error) {
	resp, err := client.FetchAll(ctx, candidateURL, maxMapBytes)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, ErrUnexpectedStatus
	}

	trimmed := strings.TrimSpace(string(resp.Body))
	if strings.HasPrefix(strings.ToLower(trimmed), "<!doctype") || strings.HasPrefix(strings.ToLower(trimmed), "<html") {
		return nil, ErrLooksLikeHTML
	}

	var parsed file
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		return nil, ErrInvalidSourceMap
	}

	if parsed.Version == 0 || len(parsed.Sources) == 0 || parsed.Mappings == "" {
		return nil, ErrMissingMapFields
	}

	return &model.SourceMapValidation{
		FinalURL:          resp.URL,
		StatusCode:        resp.StatusCode,
		ContentType:       resp.Header.Get("Content-Type"),
		SourcesCount:      len(parsed.Sources),
		NamesCount:        len(parsed.Names),
		HasSourcesContent: hasSourcesContent(parsed.SourcesContent),
		File:              parsed.File,
	}, nil
}

func hasSourcesContent(items []string) bool {
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			return true
		}
	}
	return false
}
