package logfile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewCreatesTodaysFileAndAppends(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	if _, err := w.Write([]byte("first\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := w.Write([]byte("second\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	name := filepath.Join(dir, filePrefix+time.Now().Format(dateFormat)+fileSuffix)
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if string(data) != "first\nsecond\n" {
		t.Fatalf("log content = %q", string(data))
	}

	info, err := os.Stat(name)
	if err != nil {
		t.Fatalf("stat log file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("log file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestRotatesOnDateChange(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	today := time.Date(2026, 10, 28, 23, 59, 0, 0, time.Local)
	w.now = func() time.Time { return today }
	if _, err := w.Write([]byte("day one\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	w.now = func() time.Time { return today.AddDate(0, 0, 1) }
	if _, err := w.Write([]byte("day two\n")); err != nil {
		t.Fatalf("Write after rotation: %v", err)
	}

	first, err := os.ReadFile(filepath.Join(dir, "windwatch-2026-10-28.log"))
	if err != nil {
		t.Fatalf("read first file: %v", err)
	}
	if string(first) != "day one\n" {
		t.Fatalf("first file content = %q", string(first))
	}
	second, err := os.ReadFile(filepath.Join(dir, "windwatch-2026-10-29.log"))
	if err != nil {
		t.Fatalf("read second file: %v", err)
	}
	if string(second) != "day two\n" {
		t.Fatalf("second file content = %q", string(second))
	}
}

func TestPruneKeepsRetentionWindow(t *testing.T) {
	dir := t.TempDir()
	keep := []string{
		"windwatch-2026-10-28.log", // today
		"windwatch-2026-09-29.log", // oldest retained day (30th)
		"notes.txt",                // not a rotated log
		"windwatch-bad-date.log",   // unparseable date
	}
	drop := []string{
		"windwatch-2026-09-28.log", // 31 days old
		"windwatch-2026-08-01.log",
	}
	for _, name := range append(append([]string{}, keep...), drop...) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "windwatch-2026-01-01.log"), 0o700); err != nil {
		t.Fatalf("seed directory: %v", err)
	}

	fixed := time.Date(2026, 10, 28, 12, 0, 0, 0, time.Local)
	w := &Writer{dir: dir, now: func() time.Time { return fixed }}
	if err := w.rotate(fixed); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	defer w.Close()
	if _, err := w.Write([]byte("trigger prune\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s to be kept: %v", name, err)
		}
	}
	for _, name := range drop {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be deleted, stat err = %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "windwatch-2026-01-01.log")); err != nil {
		t.Fatalf("directory with log-like name must not be deleted: %v", err)
	}
}

func TestClose(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestNewRejectsEmptyDir(t *testing.T) {
	if _, err := New(" "); err == nil {
		t.Fatal("expected error for empty directory")
	}
}
