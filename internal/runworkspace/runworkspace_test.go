package runworkspace

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// tempDir points os.MkdirTemp at a directory of the test's own, so what a run
// did or did not create is exactly what is in it.
func tempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	return dir
}

func TestOpen_nonDebugLeavesTheDirectoryToTheLaunch(t *testing.T) {
	tmp := tempDir(t)
	fallback := discardLogger()

	w, err := Open("runworkspace-test-", false, fallback)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want := runinpopup.WorkspaceOptions{NamePrefix: "runworkspace-test-"}
	if w.Options != want {
		t.Errorf(
			"Options = %+v, want %+v: the launch owns an ordinary run's directory",
			w.Options,
			want,
		)
	}
	if w.Logger != fallback {
		t.Error("Logger must be the fallback when the run is not a debug one")
	}
	if entries, err := os.ReadDir(tmp); err != nil || len(entries) != 0 {
		t.Errorf(
			"temp dir holds %v (err %v), want nothing created outside a debug run",
			entries,
			err,
		)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close = %v, want nil: nothing was opened", err)
	}
}

func TestOpen_debugLogsToFileAndKeepsDir(t *testing.T) {
	tempDir(t)
	fallback := discardLogger()

	w, err := Open("runworkspace-test-", true, fallback)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	dir := w.Options.Dir
	if dir == "" {
		t.Fatal("Options.Dir must be set: a debug run hands the launch a directory it already made")
	}
	if base := filepath.Base(dir); !strings.HasPrefix(base, "runworkspace-test-") {
		t.Errorf("dir base = %q, want the prefix applied", base)
	}
	if w.Logger == fallback {
		t.Error("Logger must not be the fallback in a debug run")
	}

	w.Logger.Debug("hello", slog.String("key", "value"))
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	logPath := filepath.Join(dir, logFileName)
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
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("Stat(%q) after Close = %v, want a debug run to keep its directory", dir, err)
	}
}

func TestOpen_debugLogFailureTakesTheDirectoryWithIt(t *testing.T) {
	tmp := tempDir(t)
	wantErr := errors.New("no log file for you")
	openFile = func(string, int, os.FileMode) (*os.File, error) { return nil, wantErr }
	t.Cleanup(func() { openFile = os.OpenFile })

	w, err := Open("runworkspace-test-", true, discardLogger())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Open = %v, %v, want the log file's error", w, err)
	}
	if entries, err := os.ReadDir(tmp); err != nil || len(entries) != 0 {
		t.Errorf(
			"temp dir holds %v (err %v), want the created directory removed again",
			entries,
			err,
		)
	}
}

func TestClose_reportsTheLogFileFailure(t *testing.T) {
	tempDir(t)

	w, err := Open("runworkspace-test-", true, discardLogger())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Closed behind Close's back, which is the one failure it can be made to
	// hit without a filesystem that refuses the flush.
	if err := w.logFile.Close(); err != nil {
		t.Fatalf("closing the log file: %v", err)
	}

	if err := w.Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("Close = %v, want the log file's own error reported", err)
	}
}
