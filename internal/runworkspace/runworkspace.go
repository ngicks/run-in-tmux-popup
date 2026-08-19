// Package runworkspace settles where one run's scratch directory comes from and
// where a debug run's log goes.
//
// Every run gets its directory made here, up front: the launch receives it as a
// caller-owned one and never removes it, so its lifetime has exactly one owner.
// An ordinary run's directory is taken away again by Close; a debug run is told
// to look for a log file in that directory and to find both still there when
// the run is over, so Close keeps it.
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
	// Options is the launch's workspace configuration: the run's directory,
	// caller-owned — this package created it and Close settles its fate.
	Options runinpopup.WorkspaceOptions
	// Logger is the debug-level logger writing to the log file in a debug run,
	// and the fallback passed to Open otherwise.
	Logger *slog.Logger

	logFile *os.File
	retain  bool
}

// Open creates the run's directory, named after namePrefix (an os.MkdirTemp
// prefix), and returns the workspace built on it.
//
// debug and fallback are parameters rather than something this package derives:
// the callers disagree on both — the shims OR an environment variable into
// debug and log to stderr otherwise, the subcommands honor only the
// PINENTRY_USER_DATA kind and fall back to their context logger — and reading
// either here would put that policy in the wrong layer.
func Open(namePrefix string, debug bool, fallback *slog.Logger) (*Workspace, error) {
	dir, err := os.MkdirTemp("", namePrefix)
	if err != nil {
		return nil, err
	}
	if !debug {
		return &Workspace{
			Options: runinpopup.WorkspaceOptions{Dir: dir},
			Logger:  fallback,
		}, nil
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
		retain:  true,
	}, nil
}

// Close settles the directory's fate and reports what failed on the way. An
// ordinary run's directory is removed with everything the launch left in it; a
// debug run keeps it — the directory and the log file in it are the two things
// such a run asked for, and a log close that failed means some of its contents
// never reached the file. The caller decides what a failure is worth — it says
// nothing about how the run itself went.
func (w *Workspace) Close() error {
	var errs []error
	if w.logFile != nil {
		errs = append(errs, w.logFile.Close())
	}
	if !w.retain {
		errs = append(errs, os.RemoveAll(w.Options.Dir))
	}
	return errors.Join(errs...)
}
