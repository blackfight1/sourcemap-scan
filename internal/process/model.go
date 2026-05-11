package process

import (
	"encoding/json"
	"fmt"
	"time"
)

type detectorType string

func (d *detectorType) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*d = ""
		return nil
	}

	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*d = detectorType(text)
		return nil
	}

	var number json.Number
	if err := json.Unmarshal(data, &number); err == nil {
		*d = detectorType(number.String())
		return nil
	}

	return fmt.Errorf("unsupported DetectorType: %s", string(data))
}

type truffleHogHit struct {
	DetectorName string       `json:"DetectorName"`
	DetectorType detectorType `json:"DetectorType"`
	Verified     bool         `json:"Verified"`
	Raw          string       `json:"Raw"`
	Redacted     string       `json:"Redacted"`
	SourceMeta   struct {
		Data struct {
			Filesystem struct {
				File string `json:"file"`
				Path string `json:"path"`
			} `json:"Filesystem"`
		} `json:"Data"`
	} `json:"SourceMetadata"`
}

type classifiedHit struct {
	Detector string `json:"detector"`
	Verified bool   `json:"verified"`
	FilePath string `json:"file_path"`
	Redacted string `json:"redacted,omitempty"`
	Notified bool   `json:"notified"`
}

type summary struct {
	Target            string          `json:"target"`
	TargetHost        string          `json:"target_host,omitempty"`
	AssetURL          string          `json:"asset_url,omitempty"`
	MapURL            string          `json:"map_url"`
	File              string          `json:"file,omitempty"`
	DiscoveredBy      string          `json:"discovered_by,omitempty"`
	SourcesCount      int             `json:"sources_count"`
	NamesCount        int             `json:"names_count"`
	HasSourcesContent bool            `json:"has_sources_content"`
	ProcessedAt       time.Time       `json:"processed_at"`
	RestoreSuccess    bool            `json:"restore_success"`
	TruffleHogSuccess bool            `json:"trufflehog_success"`
	HitsTotal         int             `json:"hits_total"`
	VerifiedHits      int             `json:"verified_hits"`
	Notified          int             `json:"notified"`
	Hits              []classifiedHit `json:"hits,omitempty"`
}
