package pipeline

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"sourcemap-scan/internal/process"
	"sourcemap-scan/internal/scan"
)

type Service struct {
	cfg Config
}

func NewService(cfg Config) (*Service, error) {
	return &Service{cfg: cfg}, nil
}

func (s *Service) Run(ctx context.Context) error {
	findingsPath, err := s.resolveFindingsPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(findingsPath), 0o755); err != nil {
		return err
	}

	fmt.Fprintf(
		os.Stderr,
		"[pipeline] stage=scan start targets=%d findings=%s base_dir=%s\n",
		len(s.cfg.Scan.Targets),
		findingsPath,
		s.cfg.Process.BaseDir,
	)

	scanCfg := s.cfg.Scan
	scanCfg.OutputPath = findingsPath

	scanSvc, err := scan.NewService(scanCfg)
	if err != nil {
		return err
	}
	if err := scanSvc.Run(ctx); err != nil {
		return err
	}

	findingCount, err := countJSONLLines(findingsPath)
	if err != nil {
		return err
	}

	fmt.Fprintf(
		os.Stderr,
		"[pipeline] stage=scan done findings=%d path=%s\n",
		findingCount,
		findingsPath,
	)

	fmt.Fprintf(
		os.Stderr,
		"[pipeline] stage=process start findings=%d path=%s\n",
		findingCount,
		findingsPath,
	)

	processCfg := s.cfg.Process
	processCfg.InputPath = findingsPath

	processSvc, err := process.NewService(processCfg)
	if err != nil {
		return err
	}
	if err := processSvc.Run(ctx); err != nil {
		return err
	}

	fmt.Fprintf(
		os.Stderr,
		"[pipeline] stage=process done findings=%d base_dir=%s\n",
		findingCount,
		s.cfg.Process.BaseDir,
	)

	return nil
}

func (s *Service) resolveFindingsPath() (string, error) {
	if s.cfg.FindingsPath != "" {
		return filepath.Clean(s.cfg.FindingsPath), nil
	}

	name := fmt.Sprintf("findings-%s.jsonl", time.Now().UTC().Format("20060102-150405"))
	return filepath.Join(s.cfg.Process.BaseDir, "findings", name), nil
}

func countJSONLLines(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var count int
	for scanner.Scan() {
		if scanner.Text() != "" {
			count++
		}
	}

	return count, scanner.Err()
}
