// Package cmdsignals lists the OS signals that should cancel top-level CLI execution.
package cmdsignals

import (
	"os"
	"syscall"
)

// ExitSignals are the signals that should cancel top-level CLI execution.
//
// The set is a per-project decision and may evolve over time
// (e.g. adding syscall.SIGHUP or syscall.SIGQUIT);
// edit this list instead of touching the signal wiring in main.
var ExitSignals = [...]os.Signal{
	os.Interrupt,
	syscall.SIGTERM,
}
