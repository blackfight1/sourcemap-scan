package output

import (
	"encoding/json"
	"io"
	"os"
	"sync"

	"sourcemap-scan/internal/model"
)

type JSONLWriter struct {
	mu     sync.Mutex
	enc    *json.Encoder
	closer io.Closer
}

func NewJSONLWriter(outputPath string) (*JSONLWriter, error) {
	return NewJSONLWriterMode(outputPath, false)
}

// NewJSONLWriterMode opens the findings file. When append is true, existing
// content is preserved (used with -resume).
func NewJSONLWriterMode(outputPath string, appendMode bool) (*JSONLWriter, error) {
	if outputPath == "" {
		return &JSONLWriter{
			enc: json.NewEncoder(os.Stdout),
		}, nil
	}

	var (
		file *os.File
		err  error
	)
	if appendMode {
		file, err = os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	} else {
		file, err = os.Create(outputPath)
	}
	if err != nil {
		return nil, err
	}

	return &JSONLWriter{
		enc:    json.NewEncoder(file),
		closer: file,
	}, nil
}

func (w *JSONLWriter) WriteFinding(f model.Finding) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enc.Encode(f)
}

func (w *JSONLWriter) Close() error {
	if w.closer == nil {
		return nil
	}
	return w.closer.Close()
}
