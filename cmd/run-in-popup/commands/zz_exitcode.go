package commands

import "fmt"

// exitCodeError asks the entry point for a specific exit status. Cobra's error
// path only ever means "failed", status 1, which is wrong for a command that
// stands in for another one: the exec payload *is* the user's command as far as
// the multiplexer can tell, so it has to exit the way that command did.
//
// A bare one — err nil — carries no message. Whatever there was to say the
// command already said on the popup terminal, and printing "error: exit status
// 2" underneath it would read as a failure of run-in-popup itself. Wrapping an
// error keeps the status and asks for that error to be printed too.
type exitCodeError struct {
	code int
	err  error
}

func (e *exitCodeError) ExitCode() int { return e.code }

func (e *exitCodeError) Unwrap() error { return e.err }

func (e *exitCodeError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit status %d", e.code)
	}
	return e.err.Error()
}
