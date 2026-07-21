package model

type Finding struct {
	Target              string `json:"target"`
	AssetURL            string `json:"asset_url"`
	MapURL              string `json:"map_url"`
	RequestedMapURL     string `json:"requested_map_url,omitempty"`
	DiscoveredBy        string `json:"discovered_by"`
	JSSource            string `json:"js_source,omitempty"` // katana | waymore | both
	SourceMappingURLRaw string `json:"source_mapping_url_raw,omitempty"`
	StatusCode          int    `json:"status_code"`
	ContentType         string `json:"content_type,omitempty"`
	SourcesCount        int    `json:"sources_count"`
	NamesCount          int    `json:"names_count"`
	HasSourcesContent   bool   `json:"has_sources_content"`
	File                string `json:"file,omitempty"`
}

type Candidate struct {
	URL                string
	Method             string
	SourceMappingValue string
}

type SourceMapValidation struct {
	FinalURL           string
	StatusCode         int
	ContentType        string
	SourcesCount       int
	NamesCount         int
	HasSourcesContent  bool
	File               string
	RequestedCandidate string
}
