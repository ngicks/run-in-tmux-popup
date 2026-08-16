package runinpopup

import (
	"context"
	"fmt"
	"strings"
)

// Backend names. They name the popup *mechanism*, not the multiplexer: tmux has
// two, and they are separate names rather than variants of one another.
const (
	BackendTmuxPopup        = "tmux-popup"
	BackendTmuxFloatingPane = "tmux-floating-pane"
	BackendZellij           = "zellij"
)

// BackendNames lists every known backend name in the order reported to users.
func BackendNames() []string {
	return []string{BackendTmuxPopup, BackendTmuxFloatingPane, BackendZellij}
}

// PopupSpec is the payload a popup runs: what to execute, with which
// environment, under which title. Backends translate it into their own argv.
type PopupSpec struct {
	// Title names the popup window (tmux: -T) or pane (zellij: --name). Empty
	// leaves the backend's default.
	Title string
	// Env is injected into the popup process (tmux: -e KEY=VALUE; zellij has no
	// such flag, so its backend exports them from the shell it wraps the payload
	// in). Map iteration order never reaches the argv: backends sort by key.
	Env map[string]string
	// Command is the argv executed inside the popup. Backends whose popup
	// mechanism only accepts a shell command line quote and join it.
	Command []string
	// Script is a raw shell command line executed inside the popup, for payloads
	// that need shell features (redirection, command substitution). It takes
	// precedence over Command; backends that run argv directly wrap it in a
	// shell.
	Script string
}

// shellCommandLine renders the spec's payload as a single shell command line,
// for backends that cannot take an argv.
func (s PopupSpec) shellCommandLine() string {
	if s.Script != "" {
		return s.Script
	}
	return shellJoin(s.Command)
}

// Backend opens a popup in a terminal multiplexer. Implementations hold the
// coordinates of the multiplexer they talk to (binary path, session, client)
// and are safe to reuse for several popups.
type Backend interface {
	// Name reports the backend's name, one of BackendNames.
	Name() string
	// PopupCommand returns the argv that opens a popup executing spec. It only
	// builds the command line — nothing is executed, so it is cheap and testable.
	PopupCommand(spec PopupSpec) (path string, args []string)
	// Environ returns "KEY=VALUE" adjustments the popup command needs on top of
	// the current process environment (tmux-popup needs $TMUX set from the
	// session meta, or the popup never appears), or nil when it needs none.
	Environ() []string
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

// DetectBackendName picks a backend name from ambient hints, for callers that
// were not told which backend to use. Every hint is passed in — the caller
// reads the environment, the detection itself stays pure:
//
//   - userDataKind is PinentryUserData.Kind, the most specific hint since the
//     gpg-agent wrapper script picked it deliberately. A "_DEBUG" suffix does
//     not change the mechanism, so the kind is matched by prefix.
//   - tmuxEnv is $TMUX and zellijEnv is $ZELLIJ, checked in that order. A bare
//     $TMUX names the multiplexer, not one of its two popup mechanisms, and
//     resolves to BackendTmuxPopup: display-popup is the older, unconditionally
//     safe one, so floating panes stay an explicit choice.
//
// It returns an error naming the valid backends when nothing matches.
func DetectBackendName(userDataKind, tmuxEnv, zellijEnv string) (string, error) {
	switch kind := strings.ToUpper(strings.TrimSpace(userDataKind)); {
	case strings.HasPrefix(kind, "TMUX_POPUP"):
		return BackendTmuxPopup, nil
	case strings.HasPrefix(kind, "TMUX_FLOATING_PANE"):
		return BackendTmuxFloatingPane, nil
	case strings.HasPrefix(kind, "ZELLIJ_POPUP"):
		return BackendZellij, nil
	}
	switch {
	case tmuxEnv != "":
		return BackendTmuxPopup, nil
	case zellijEnv != "":
		return BackendZellij, nil
	}
	return "", fmt.Errorf(
		"cannot detect the popup backend:"+
			" neither PINENTRY_USER_DATA, $TMUX nor $ZELLIJ names one;"+
			" select it explicitly, valid values are %s",
		strings.Join(BackendNames(), ", "),
	)
}

// shellQuote renders s as a single POSIX shell word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellJoin renders an argv as a shell command line that executes it unchanged.
func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}
