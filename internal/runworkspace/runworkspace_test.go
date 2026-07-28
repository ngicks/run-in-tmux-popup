package runworkspace

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestOpen_nonDebugKeepsFallbackAndRemovesDir(t *testing.T) {
	fallback := discardLogger()

	w, err := Open("runworkspace-test-*", false, fallback)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if w.Logger != fallback {
		t.Error("Logger must be the fallback when the run is not a debug one")
	}
	if fi, err := os.Stat(w.Dir); err != nil || !fi.IsDir() {
		t.Fatalf("Stat(%q) = %v, %v: want an existing directory", w.Dir, fi, err)
	}
	if entries, err := os.ReadDir(w.Dir); err != nil || len(entries) != 0 {
		t.Errorf("dir holds %v (err %v), want no log file outside a debug run", entries, err)
	}

	w.Close()
	if _, err := os.Stat(w.Dir); !os.IsNotExist(err) {
		t.Errorf("Stat(%q) after Close = %v, want the directory removed", w.Dir, err)
	}
}

func TestOpen_debugLogsToFileAndKeepsDir(t *testing.T) {
	fallback := discardLogger()

	w, err := Open("runworkspace-test-*", true, fallback)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Close keeps a debug directory on purpose, so the test owns the cleanup.
	t.Cleanup(func() { _ = os.RemoveAll(w.Dir) })

	if w.Logger == fallback {
		t.Error("Logger must not be the fallback in a debug run")
	}
	w.Logger.Debug("hello", slog.String("key", "value"))
	w.Close()

	logPath := filepath.Join(w.Dir, "log.txt")
	fi, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Stat(%q): %v", logPath, err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("log file mode = %v, want %v", got, os.FileMode(0o600))
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", logPath, err)
	}
	// Debug level: the fallback's default handler would have dropped this record.
	if !strings.Contains(string(b), "hello") || !strings.Contains(string(b), "key=value") {
		t.Errorf("log = %q, want the debug record", b)
	}
	if _, err := os.Stat(w.Dir); err != nil {
		t.Errorf("Stat(%q) after Close = %v, want a debug run to keep its directory", w.Dir, err)
	}
}

func TestOpen_prefixNamesTheDir(t *testing.T) {
	w, err := Open("runworkspace-prefix-test-*", false, discardLogger())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer w.Close()

	if base := filepath.Base(w.Dir); !strings.HasPrefix(base, "runworkspace-prefix-test-") {
		t.Errorf("dir base = %q, want the prefix applied", base)
	}
}
