package runinpopup

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

var _ PinentryHandshaker = (*TmuxFloatingPaneBackend)(nil)

// TmuxFloatingPaneBackend opens popups as tmux floating panes ("tmux new-pane",
// bound to `*` by default). A floating pane belongs to a window rather than to a
// client, so it is addressed by session.
type TmuxFloatingPaneBackend struct {
	tmuxPath    string
	sessionId   string
	sessionMeta string
	tmuxEnv     string
}

// NewTmuxFloatingPaneBackend builds the "tmux-floating-pane" backend. It uses
// BinaryPath (default "tmux"), SessionId, SessionMeta and TMUX. Shell is not
// needed because tmux runs the payload through its own default-shell, and
// ClientId cannot be honored at all: new-pane has no client-targeting flag —
// there is no equivalent of display-popup's -c, because the pane lives in a
// window and every client viewing that window sees it.
//
// SessionMeta is only validated when it is the value that will be used, i.e.
// when TMUX is empty: a caller already inside tmux does not need it at all.
func NewTmuxFloatingPaneBackend(opts BackendOptions) (*TmuxFloatingPaneBackend, error) {
	b := &TmuxFloatingPaneBackend{
		tmuxPath:    cmp.Or(opts.BinaryPath, "tmux"),
		sessionId:   opts.SessionId,
		sessionMeta: opts.SessionMeta,
		tmuxEnv:     opts.TMUX,
	}
	if err := validateTmuxSessionMeta(b.sessionMeta, b.tmuxEnv); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *TmuxFloatingPaneBackend) Name() string {
	return BackendTmuxFloatingPane
}

// PopupCommand builds "tmux new-pane [-t <session>] [-e KEY=VALUE...] --
// <command line>".
//
// new-pane can execute an argv directly, but the payload is passed as a single
// shell command line so Script payloads work unchanged; "--" then keeps a
// payload starting with "-" from being read as flags, which display-popup's -E
// does not need. spec.Title is dropped: new-pane has no title flag.
//
// -d is deliberately absent. It would leave the focus where it was, and a
// passphrase typed into an unfocused popup goes to the pane underneath.
func (b *TmuxFloatingPaneBackend) PopupCommand(spec PopupSpec) (string, []string) {
	args := b.targeted("new-pane")
	for _, k := range slices.Sorted(maps.Keys(spec.Env)) {
		args = append(args, "-e", k+"="+spec.Env[k])
	}
	args = append(args, "--", spec.shellCommandLine())
	return b.tmuxPath, args
}

// Environ sets $TMUX from the session meta when the current process has none.
// Without $TMUX the popup silently never appears.
func (b *TmuxFloatingPaneBackend) Environ() []string {
	return tmuxSessionEnviron(b.sessionMeta, b.tmuxEnv)
}

// NewPinentryHandshake uses the shared tmux handshake: new-pane injects the FIFO
// paths and the guard secrets as pane env (-e), same as display-popup.
func (b *TmuxFloatingPaneBackend) NewPinentryHandshake(
	ttyFifo, doneFifo string,
) (PinentryHandshake, error) {
	return newTmuxPinentryHandshake(ttyFifo, doneFifo)
}

// Prepare de-zooms the window before the floating pane is created, and returns a
// restore func re-zooming the pane that was zoomed (D8: best-effort, its error is
// logged by the caller rather than failing the run).
//
// This is the tmux 3.7b crash the Prepare seam exists for: a floating pane
// created while a pane in the window is zoomed takes the whole server down —
// observed here when the floating pane's command exits, which is exactly what
// the pinentry handshake payload does. Fixed in 3.7c, so the workaround is
// version-gated (D9).
//
// Failing to read the zoom state on an affected tmux aborts: creating the
// floating pane without knowing whether the window is zoomed is the one move
// that can crash the user's server.
//
// Restore does not wait for the popup, and no caller guarantees it is gone:
// Run returns as soon as new-pane does, and CallPinentry returns once it has
// written the done FIFO, without waiting for the pane to act on it. What makes
// that safe is measured, not ordered — re-zooming while a floating pane is
// still alive un-floats it into the layout rather than crashing the server. So
// the worst case is a popup pulled out of its float, most likely on the Run
// path, where the pane is still alive by construction.
func (b *TmuxFloatingPaneBackend) Prepare(
	ctx context.Context,
) (func(context.Context) error, error) {
	// A tmux that cannot report its version tells us nothing about whether it is
	// fixed, so it falls through to the zoom query like an affected one.
	if version, err := b.runTmux(ctx, "-V"); err == nil && !tmuxAffectedByZoomCrash(version) {
		return nil, nil
	}

	zoomed, paneId, err := b.zoomedPane(ctx)
	if err != nil {
		return nil, err
	}
	if !zoomed {
		return nil, nil
	}
	if err := b.toggleZoom(ctx, paneId); err != nil {
		return nil, fmt.Errorf("de-zooming pane %s: %w", paneId, err)
	}
	return func(ctx context.Context) error {
		if err := b.toggleZoom(ctx, paneId); err != nil {
			return fmt.Errorf("re-zooming pane %s: %w", paneId, err)
		}
		return nil
	}, nil
}

// zoomStateFormat asks for both halves of the answer in one round trip: whether
// the window is zoomed, and which pane holds the zoom.
const zoomStateFormat = "#{window_zoomed_flag}:#{pane_id}"

// zoomedPane reports whether the targeted window has a zoomed pane, and which
// one. The pane is remembered by id rather than re-derived from the session on
// restore: by then the session's active pane may still be the popup, and
// toggling zoom on that would zoom the popup instead of the user's pane.
func (b *TmuxFloatingPaneBackend) zoomedPane(ctx context.Context) (bool, string, error) {
	out, err := b.runTmux(ctx, append(b.targeted("display-message", "-p"), zoomStateFormat)...)
	if err != nil {
		return false, "", fmt.Errorf("querying the zoomed pane: %w", err)
	}
	flag, paneId, ok := strings.Cut(strings.TrimSpace(out), ":")
	if !ok || paneId == "" {
		return false, "", fmt.Errorf("querying the zoomed pane: unexpected output %q", out)
	}
	return flag == "1", paneId, nil
}

func (b *TmuxFloatingPaneBackend) toggleZoom(ctx context.Context, paneId string) error {
	_, err := b.runTmux(ctx, "resize-pane", "-Z", "-t", paneId)
	return err
}

// targeted appends "-t <session>" to a tmux command, matching how PopupCommand
// targets the popup so Prepare inspects the window the popup will open in.
// Without a SessionId both omit it and tmux resolves the same current session,
// so they stay consistent — just less explicit.
func (b *TmuxFloatingPaneBackend) targeted(args ...string) []string {
	if b.sessionId == "" {
		return args
	}
	return append(args, "-t", b.sessionId)
}

// runTmux runs a tmux command carrying the same $TMUX the popup command gets —
// without it Prepare would inspect the default socket's server while the popup
// opens on another — and folds the command's stderr into the error, where tmux
// reports "can't find pane" and friends.
func (b *TmuxFloatingPaneBackend) runTmux(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, b.tmuxPath, args...)
	if extraEnv := b.Environ(); len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	out, err := cmd.Output()
	if execErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return "", fmt.Errorf(
			"%s %s: %w: %s",
			b.tmuxPath, strings.Join(args, " "), err, strings.TrimSpace(string(execErr.Stderr)),
		)
	}
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", b.tmuxPath, strings.Join(args, " "), err)
	}
	return string(out), nil
}

// The floating-pane zoom crash is fixed in tmux 3.7c, unreleased as of
// 2026-07-28.
const (
	tmuxZoomCrashFixedMajor  = 3
	tmuxZoomCrashFixedMinor  = 7
	tmuxZoomCrashFixedSuffix = "c"
)

// tmuxAffectedByZoomCrash reports whether the tmux that printed version — the
// raw output of "tmux -V" — crashes when a floating pane is created over a
// zoomed pane.
//
// Only a version positively identifiable as 3.7c or later counts as fixed (D9).
// Everything parseTmuxVersion rejects is treated as affected, including the
// "tmux next-3.8" of development builds: that string pins no commit, so it
// cannot show the fix is in. A spurious de-zoom is flicker; a missed one takes
// the server down.
func tmuxAffectedByZoomCrash(version string) bool {
	major, minor, suffix, ok := parseTmuxVersion(version)
	if !ok {
		return true
	}
	return cmp.Or(
		cmp.Compare(major, tmuxZoomCrashFixedMajor),
		cmp.Compare(minor, tmuxZoomCrashFixedMinor),
		cmp.Compare(suffix, tmuxZoomCrashFixedSuffix),
	) < 0
}

// parseTmuxVersion splits the output of "tmux -V" — "tmux 3.7b" — into 3, 7 and
// "b", an absent letter suffix being the empty string that sorts before "a". It
// accepts only that release form; anything else reports ok == false.
func parseTmuxVersion(out string) (major, minor int, suffix string, ok bool) {
	v, ok := strings.CutPrefix(strings.TrimSpace(out), "tmux ")
	if !ok {
		return 0, 0, "", false
	}
	majorStr, rest, ok := strings.Cut(v, ".")
	if !ok {
		return 0, 0, "", false
	}
	minorStr := rest
	if i := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		minorStr, suffix = rest[:i], rest[i:]
	}
	for _, r := range suffix {
		if r < 'a' || r > 'z' {
			return 0, 0, "", false
		}
	}
	major, err := strconv.Atoi(majorStr)
	if err != nil {
		return 0, 0, "", false
	}
	minor, err = strconv.Atoi(minorStr)
	if err != nil {
		return 0, 0, "", false
	}
	return major, minor, suffix, true
}
