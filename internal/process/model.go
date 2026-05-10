package process

import "time"

type truffleHogHit struct {
	DetectorName string `json:"DetectorName"`
	DetectorType string `json:"DetectorType"`
	Verified     bool   `json:"Verified"`
	Raw          string `json:"Raw"`
	Redacted     string `json:"Redacted"`
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
	MapURL            string          `json:"map_url"`
	ProcessedAt       time.Time       `json:"processed_at"`
	RestoreSuccess    bool            `json:"restore_success"`
	TruffleHogSuccess bool            `json:"trufflehog_success"`
	HitsTotal         int             `json:"hits_total"`
	VerifiedHits      int             `json:"verified_hits"`
	Notified          int             `json:"notified"`
	Hits              []classifiedHit `json:"hits,omitempty"`
}
