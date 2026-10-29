// Package logfile provides a daily-rotating log file writer with fixed retention.
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
	filePrefix = "windwatch-"
	fileSuffix = ".log"
	dateFormat = "2006-01-02"
	// RetentionDays is how many daily log files are kept, including today's.
	RetentionDays = 30
)

// Writer appends to one log file per local day and deletes files older than
// the retention window. It is safe for concurrent use.
type Writer struct {
	dir string
	now func() time.Time

	mu      sync.Mutex
	file    *os.File
	current string
}

// New opens (or creates) the log directory and today's log file, and prunes
// expired files. The directory is created with mode 0700 and files with 0600,
// matching the sensitivity of the data directory.
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

// rotate ensures the file for now's local day is open. If opening the new
// day's file fails mid-run, the previous file stays open so log lines are not
// lost; the rotation is retried on the next write.
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

// prune deletes rotated log files strictly older than the retention window.
// Only files matching the exact naming scheme are touched.
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
			os.Remove(filepath.Join(w.dir, name))
		}
	}
}

func (w *Writer) path(date string) string {
	return filepath.Join(w.dir, filePrefix+date+fileSuffix)
}
