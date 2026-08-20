package runinpopup

import (
	"context"
	"io"
)

// PopupSpec is the payload a popup runs: what to execute, with which
// environment, under which title. It is a template — it holds no stream and no
// per-launch state, so one spec can open any number of popups.
type PopupSpec struct {
	// Title names the popup window (tmux: -T) or pane (zellij: --name). Empty
	// leaves the backend's default.
	Title string
	// Env is injected into the popup process (tmux: -e KEY=VALUE; zellij has no
	// such flag, so its backend writes the values into the launch's work
	// directory and has the payload source them). Map iteration order never
	// reaches the argv: backends sort by key.
	Env map[string]string
	// X, Y, Width and Height place and size the popup, in the vocabulary tmux's
	// display-popup takes: a bare number is terminal cells and "N%" a percentage
	// of the terminal. Empty leaves the backend's own placement, and a field left
	// empty puts nothing on a backend's command line.
	//
	// X and Y are the popup's top-left corner on every backend. Backends whose
	// mechanism says the same thing get the values as they were written; tmux's
	// display-popup, which places a popup by its bottom edge instead, has the
	// height added to Y on its way to the command line. That translation needs a
	// height to add: on the tmux-popup backend a numeric or percentage Y with no
	// Height, or with a Height in the other unit, fails the launch rather than
	// putting the popup somewhere the caller did not ask for. Either way tmux
	// still clamps a popup that would fall outside the terminal.
	//
	// X and Y additionally take tmux's single-letter position specifiers, which
	// reach tmux untranslated:
	//
	//	C  the centre of the terminal
	//	R  the right side of the terminal (X only)
	//	P  the bottom left of the pane
	//	M  the mouse position
	//	W  the window position on the status line
	//	S  the line above or below the status line (Y only)
	//
	// Which axis a specifier suits is tmux's own rule and tmux's to enforce: all
	// six are accepted here for both. zellij has no equivalent for any of them —
	// its backend refuses a specifier rather than guessing at one — while cells
	// and percentages reach every backend.
	//
	// A malformed value fails the launch before a popup is opened, so a typo
	// costs nothing but the error naming it.
	X, Y, Width, Height string
	// Command is the argv executed inside the popup. Backends whose popup
	// mechanism only accepts a shell command line quote and join it.
	Command []string
	// Script is a raw shell command line executed inside the popup, for payloads
	// that need shell features (redirection, command substitution). It takes
	// precedence over Command; backends that run argv directly wrap it in a
	// shell.
	Script string
}

// LaunchSpec is what a backend opens a popup for: one completed launch, built
// by PopupLauncher from a PopupSpec template. Its command line is final —
// whatever attaches the payload's streams is already in it — so a backend
// translates it into its mechanism's argv and starts it, nothing else.
type LaunchSpec struct {
	// Title, Env, X, Y, Width, Height, Command and Script carry the same meaning
	// as their PopupSpec counterparts. Each value has been validated on its own by
	// the time a backend sees it, so what is left is what only the backend knows:
	// translating the geometry into its mechanism's flags and coordinates, or
	// refusing what that mechanism cannot be asked for — a value it has no
	// equivalent for, or a combination it cannot place.
	Title               string
	Env                 map[string]string
	X, Y, Width, Height string
	Command             []string
	Script              string

	// WorkDir is the launch's private mode-0700 scratch directory, present
	// whenever the launch has one: it already holds the payload FIFOs named in
	// the command line above, and a backend needing a file of its own — an
	// environment its multiplexer has no flag for, say — puts it here. The
	// launch gives the directory back when it is over, so nothing written into
	// it needs a lifetime of its own.
	WorkDir string

	// Stdin, Stdout and Stderr are the endpoints the payload's streams of those
	// names are connected to for this launch, nil meaning the stream was not
	// allocated one and stays on the popup's terminal.
	//
	// Backends do no piping: the multiplexer runs the payload, not this process,
	// so the launch layer connects these endpoints to the FIFOs it has already
	// written into the command line above, and a backend that cannot hand a
	// payload its stdio directly simply ignores them. The launcher process's own
	// output is not here at all — it is internal diagnostics, and belongs to
	// whoever runs the launcher.
	Stdin  io.ReadCloser
	Stdout io.WriteCloser
	Stderr io.WriteCloser
}

// PopupHandle is a launched popup, waited on by whoever launched it. It is
// deliberately wait-only: connecting a payload's stdio is the same work for
// every mechanism, so it lives in the launch layer instead of in each backend.
type PopupHandle interface {
	// Wait waits for the popup launcher to exit and reports why it failed.
	//
	// The launcher exiting is not the payload finishing: tmux display-popup
	// stays for as long as the popup, but the floating-pane mechanisms return as
	// soon as the pane exists.
	Wait() error
}

// Backend opens a popup in a terminal multiplexer. Implementations hold the
// coordinates of the multiplexer they talk to (binary path, session, client)
// and are safe to reuse for several popups.
type Backend interface {
	// Name reports the backend's name, one of the names defined by the backend
	// package.
	Name() string
	// Launch opens a popup running spec and returns a handle on the launcher.
	// Canceling ctx dismisses the popup.
	Launch(ctx context.Context, spec LaunchSpec) (PopupHandle, error)
	// Prepare adjusts multiplexer state that would break — or crash — popup
	// creation, and returns a func restoring that state once the popup is gone.
	// Both may be no-ops: a nil restore means "nothing to undo", and is the
	// normal result for backends that need no adjustment.
	//
	// tmux-floating-pane is the one implementation: it works around the tmux 3.7b
	// crash on creating a floating pane while a pane is zoomed, under the
	// contract fixed for it before it was written:
	//   - Version-gated: de-zoom only on tmux versions affected by the bug
	//     (< 3.7c). An unparseable version counts as affected — a spurious
	//     de-zoom is flicker, a missed one takes down the tmux server.
	//   - Restore is best-effort: callers run it after the popup closes, with a
	//     context that outlives the popup's cancellation, and log its error
	//     rather than failing the run.
	Prepare(ctx context.Context) (restore func(context.Context) error, err error)
}
