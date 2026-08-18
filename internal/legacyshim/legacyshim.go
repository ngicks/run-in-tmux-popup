// Package legacyshim runs the deprecated per-multiplexer pinentry binaries.
//
// Both of them are the same program: announce their own deprecation, read the
// multiplexer's coordinates out of PINENTRY_USER_DATA, build the one backend
// they were named after, and proxy the Assuan exchange into it. What they
// disagree on — the names in that announcement, the layout the validation error
// quotes, the backend, the directory name — is what a Shim carries, so the
// binaries themselves are left with their arguments and their exit code.
//
// Their oddities are compatibility, not design: the wording gpg-agent wrapper
// scripts have been grepping for, and a debug switch spelled after tmux on both
// binaries. Nothing here is a template for a new entry point — "run-in-popup
// pinentry" is.
package legacyshim

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ngicks/run-in-tmux-popup/internal/runworkspace"
	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

const (
	// userDataEnvVar carries the multiplexer's coordinates, forwarded verbatim
	// by gpg-agent from the environment its wrapper script ran in.
	userDataEnvVar = "PINENTRY_USER_DATA"
	// debugEnvVar turns debug logging on for either binary, whatever the
	// PINENTRY_USER_DATA kind says. The tmux name has no zellij counterpart on
	// purpose: the zellij binary grew out of the tmux one, and users' setups know
	// this variable.
	debugEnvVar = "TMUX_POPUP_DEBUG"
)

// Shim is one deprecated binary: everything the two of them disagree on.
type Shim struct {
	// Name is the binary's own name, as its deprecation notice reports it.
	Name string
	// Replacement is the run-in-popup invocation that notice sends users to.
	Replacement string
	// UserDataFormat is the PINENTRY_USER_DATA layout this binary wants, quoted
	// verbatim by the error a value not matching it gets.
	UserDataFormat string
	// WorkspacePrefix names the directory holding the handshake FIFOs, and the
	// debug log when the run has one.
	WorkspacePrefix string
	// NewBackend builds the one backend this binary ever opens a popup with.
	// Whatever it needs beyond the parsed user data — the process environment,
	// in practice — it reads itself.
	NewBackend func(userData runinpopup.PinentryUserData) (runinpopup.Backend, error)

	// call runs the assembled exchange. Only tests replace it: a real call wants
	// a multiplexer, and what is worth asserting is what the run handed it.
	call func(ctx context.Context, launcher *runinpopup.PinentryLauncher) error
}

// Run is the binary: it announces the deprecation, validates the environment,
// and proxies the Assuan exchange gpg-agent runs over this process's stdin and
// stdout into a popup. args is passed to pinentry unchanged, normally the
// binary's own arguments as gpg-agent invoked them.
//
// stderr takes the notice and the run's log, and they go nowhere else: stdout
// carries the Assuan exchange, and any stray byte there breaks the protocol.
// A canceled ctx ends the exchange, as do SIGINT, SIGTERM and SIGABRT.
func (s Shim) Run(ctx context.Context, args []string, stderr io.Writer) (err error) {
	fmt.Fprintf(stderr, "%s is deprecated; run %q instead.\n", s.Name, s.Replacement)

	// The set these binaries have always watched, kept rather than replaced by
	// the one the run-in-popup command uses: SIGABRT is not on that list.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT)
	defer stop()

	userData := runinpopup.ParsePinentryUserData(os.Getenv(userDataEnvVar))
	if userData.Path == "" || userData.SessionId == "" {
		return fmt.Errorf(
			"environment variable %q must be formatted as %q but is %q",
			userDataEnvVar, s.UserDataFormat, os.Getenv(userDataEnvVar),
		)
	}

	popupBackend, err := s.NewBackend(userData)
	if err != nil {
		return err
	}

	workspace, err := runworkspace.Open(
		s.WorkspacePrefix,
		userData.Debug() || os.Getenv(debugEnvVar) == "1",
		slog.New(slog.NewTextHandler(stderr, nil)),
	)
	if err != nil {
		return err
	}
	defer func() {
		// A debug log that would not close is worth reporting, but never worth
		// hiding how the exchange itself went.
		if cerr := workspace.Close(); err == nil {
			err = cerr
		}
	}()
	workspace.Logger.Info(userDataEnvVar, slog.Any("data", userData))

	pinentry := &runinpopup.PinentryLauncher{
		Popup: &runinpopup.PopupLauncher{
			Backend:   popupBackend,
			Logger:    workspace.Logger,
			Workspace: workspace.Options,
		},
		PinentryArgs: args,
	}
	if s.call != nil {
		return s.call(ctx, pinentry)
	}
	return pinentry.Call(ctx)
}
