// Package runworkspace settles where one run's scratch directory comes from and
// where a debug run's log goes.
//
// An ordinary run needs no directory of its own: the launch creates the one
// holding its FIFOs and takes it away afterwards. A debug run is told to look
// for a log file in that directory and to find both still there when the run is
// over — and a logger cannot be pointed at a directory that does not exist yet,
// so such a run creates the directory up front and hands it to the launch as a
// caller-owned one, which the launch uses and never removes.
//
// Every entry point (the pinentry and exec subcommands, and the two deprecated
// shims) faces that same fork, so it is decided here once instead of in each of
// them.
package runworkspace

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

// logFileName is the debug log inside the run's directory. The name is part of
// what users are told to look for, so it is fixed rather than configurable.
const logFileName = "log.txt"

// openFile is os.OpenFile, seamed so a test can drive the failure that has to
// take the directory back down with it.
var openFile = os.OpenFile

// Workspace is what one run hands to its launch: where the payload's FIFOs go,
// and the logger the run writes through. Close it when the run ends.
type Workspace struct {
	// Options is the launch's workspace configuration — the caller-owned
	// directory of a debug run, or the prefix naming the one the launch creates
	// and removes itself.
	Options runinpopup.WorkspaceOptions
	// Logger is the debug-level logger writing to the log file in a debug run,
	// and the fallback passed to Open otherwise.
	Logger *slog.Logger

	logFile *os.File
}

// Open returns the workspace of a run whose directory is named after namePrefix
// (an os.MkdirTemp prefix).
//
// debug and fallback are parameters rather than something this package derives:
// the callers disagree on both — the shims OR an environment variable into
// debug and log to stderr otherwise, the subcommands honor only the
// PINENTRY_USER_DATA kind and fall back to their context logger — and reading
// either here would put that policy in the wrong layer.
func Open(namePrefix string, debug bool, fallback *slog.Logger) (*Workspace, error) {
	if !debug {
		return &Workspace{
			Options: runinpopup.WorkspaceOptions{NamePrefix: namePrefix},
			Logger:  fallback,
		}, nil
	}
	dir, err := os.MkdirTemp("", namePrefix)
	if err != nil {
		return nil, err
	}
	logFile, err := openFile(
		filepath.Join(dir, logFileName),
		os.O_APPEND|os.O_CREATE|os.O_RDWR,
		0o600,
	)
	if err != nil {
		// The run this failure ends never gets as far as the directory, so what
		// would be left behind is an empty one nobody is coming back for.
		return nil, errors.Join(err, os.RemoveAll(dir))
	}
	return &Workspace{
		Options: runinpopup.WorkspaceOptions{Dir: dir},
		Logger: slog.New(
			slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelDebug}),
		),
		logFile: logFile,
	}, nil
}

// Close closes the debug log file and reports a failure to do so: what a debug
// run asked for is that file's contents, and a close that failed means some of
// them never reached it. The caller decides what such a failure is worth — it
// says nothing about how the run itself went.
//
// The directory stays. It is the other half of what a debug run asked for, and
// an ordinary run has none of its own to remove.
func (w *Workspace) Close() error {
	if w.logFile == nil {
		return nil
	}
	return w.logFile.Close()
}
