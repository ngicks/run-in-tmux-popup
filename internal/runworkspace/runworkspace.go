// Package runworkspace opens the scratch directory a pinentry proxy run needs
// and points the run's logger at it. Every entry point (the pinentry subcommand
// and the two deprecated shims) wants the same thing — a temporary directory
// for the handshake FIFOs, thrown away afterwards unless the run is a debug one,
// in which case it also holds a log file — so the sequence lives here once
// instead of in each main.
package runworkspace

import (
	"log/slog"
	"os"
	"path/filepath"
)

// logFileName is the debug log inside Workspace.Dir. The name is part of what
// users are told to look for, so it is fixed rather than configurable.
const logFileName = "log.txt"

// Workspace is one run's temporary directory plus the logger writing into it.
// Close it when the run ends.
type Workspace struct {
	// Dir is the temporary directory, always created. It survives Close in a
	// debug run.
	Dir string
	// Logger is the debug-level logger writing to Dir/log.txt in a debug run,
	// and the fallback passed to Open otherwise.
	Logger *slog.Logger

	logFile *os.File
	keepDir bool
}

// Open creates a temporary directory named after prefix (an os.MkdirTemp
// pattern) and returns it with the logger the run should use.
//
// debug and fallback are parameters rather than something this package derives:
// the callers disagree on both — the shims OR an environment variable into
// debug and log to stderr otherwise, the subcommand honors only the
// PINENTRY_USER_DATA kind and falls back to its context logger — and reading
// either here would put that policy in the wrong layer.
//
// A debug run keeps its directory, so a failure to open the log file leaves the
// directory behind too: it is the evidence the caller was asking for.
func Open(prefix string, debug bool, fallback *slog.Logger) (*Workspace, error) {
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return nil, err
	}
	w := &Workspace{Dir: dir, Logger: fallback, keepDir: debug}
	if !debug {
		return w, nil
	}
	logFile, err := os.OpenFile(
		filepath.Join(dir, logFileName),
		os.O_APPEND|os.O_CREATE|os.O_RDWR,
		0o600,
	)
	if err != nil {
		return nil, err
	}
	w.logFile = logFile
	w.Logger = slog.New(
		slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelDebug}),
	)
	return w, nil
}

// Close flushes and closes the log file, then removes the directory unless the
// run was a debug one. Errors are dropped: the run is over, nothing can act on
// them, and a debug run's evidence is on disk either way.
func (w *Workspace) Close() {
	if w.logFile != nil {
		_ = w.logFile.Close()
	}
	if !w.keepDir {
		_ = os.RemoveAll(w.Dir)
	}
}
