package logfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	filePrefix    = "windwatch-"
	fileSuffix    = ".log"
	dateFormat    = "2006-01-02"
	RetentionDays = 30
)

type Writer struct {
	dir string
	now func() time.Time

	mu      sync.Mutex
	file    *os.File
	current string
}

func New(dir string) (*Writer, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("log directory must not be empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	w := &Writer{dir: dir, now: time.Now}
	if err := w.rotate(w.now()); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.rotate(w.now()); err != nil {
		return 0, err
	}
	return w.file.Write(p)
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	w.current = ""
	return err
}

func (w *Writer) rotate(now time.Time) error {
	date := now.Format(dateFormat)
	if w.file != nil && date == w.current {
		return nil
	}
	file, err := os.OpenFile(w.path(date), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		if w.file != nil {
			return nil
		}
		return fmt.Errorf("open log file: %w", err)
	}
	if w.file != nil {
		w.file.Close()
	}
	w.file = file
	w.current = date
	w.prune(now)
	return nil
}

func (w *Writer) prune(now time.Time) {
	cutoff := now.AddDate(0, 0, -(RetentionDays - 1)).Format(dateFormat)
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, filePrefix) || !strings.HasSuffix(name, fileSuffix) {
			continue
		}
		dateText := strings.TrimSuffix(strings.TrimPrefix(name, filePrefix), fileSuffix)
		if _, err := time.Parse(dateFormat, dateText); err != nil {
			continue
		}
		if dateText < cutoff {
			_ = os.Remove(filepath.Join(w.dir, name))
		}
	}
}

func (w *Writer) path(date string) string {
	return filepath.Join(w.dir, filePrefix+date+fileSuffix)
}
