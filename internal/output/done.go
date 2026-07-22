package output

import (
	"bufio"
	"os"
	"strings"
	"sync"
)

// DoneTracker appends finished targets so a later -resume can skip them.
type DoneTracker struct {
	mu   sync.Mutex
	path string
	file *os.File
}

func NewDoneTracker(path string) (*DoneTracker, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return &DoneTracker{}, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &DoneTracker{path: path, file: file}, nil
}

func (d *DoneTracker) Enabled() bool {
	return d != nil && d.file != nil
}

func (d *DoneTracker) Mark(target string) error {
	if d == nil || d.file == nil {
		return nil
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := d.file.WriteString(target + "\n"); err != nil {
		return err
	}
	return d.file.Sync()
}

func (d *DoneTracker) Close() error {
	if d == nil || d.file == nil {
		return nil
	}
	return d.file.Close()
}

// LoadDoneSet reads a done file into a set of finished targets.
func LoadDoneSet(path string) (map[string]struct{}, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return map[string]struct{}{}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]struct{}{}, nil
		}
		return nil, err
	}
	defer file.Close()

	out := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = struct{}{}
	}
	return out, scanner.Err()
}
