package backend

import (
	"cmp"
	"context"
	"maps"
	"slices"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

var _ runinpopup.PinentryHandshaker = (*TmuxPopup)(nil)

// TmuxPopup opens popups with tmux's display-popup ("tmux popup"). The
// popup is a client-side overlay, so it targets a client rather than a session.
type TmuxPopup struct {
	tmuxPath    string
	clientId    string
	sessionMeta string
	tmuxEnv     string
}

// NewTmuxPopup builds the "tmux-popup" backend. It uses BinaryPath
// (default "tmux"), ClientId, SessionMeta and TMUX; SessionId and Shell are not
// needed because display-popup runs on a client and through tmux's own
// default-shell.
//
// SessionMeta is only validated when it is the value that will be used, i.e.
// when TMUX is empty: a caller already inside tmux does not need it at all.
func NewTmuxPopup(opts Options) (*TmuxPopup, error) {
	b := &TmuxPopup{
		tmuxPath:    cmp.Or(opts.BinaryPath, "tmux"),
		clientId:    opts.ClientId,
		sessionMeta: opts.SessionMeta,
		tmuxEnv:     opts.TMUX,
	}
	if err := validateTmuxSessionMeta(b.sessionMeta, b.tmuxEnv); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *TmuxPopup) Name() string {
	return NameTmuxPopup
}

// PopupCommand builds "tmux popup -c <client> [-T <title>] [-e KEY=VALUE...] -E
// <command line>". display-popup takes a shell command line, so an argv payload
// is quoted and joined into one.
func (b *TmuxPopup) PopupCommand(spec runinpopup.PopupSpec) (string, []string) {
	args := []string{"popup"}
	if b.clientId != "" {
		args = append(args, "-c", b.clientId)
	}
	if spec.Title != "" {
		args = append(args, "-T", spec.Title)
	}
	for _, k := range slices.Sorted(maps.Keys(spec.Env)) {
		args = append(args, "-e", k+"="+spec.Env[k])
	}
	args = append(args, "-E", commandLine(spec))
	return b.tmuxPath, args
}

// Environ sets $TMUX from the session meta when the current process has none.
// Without $TMUX the popup silently never appears.
func (b *TmuxPopup) Environ() []string {
	return tmuxSessionEnviron(b.sessionMeta, b.tmuxEnv)
}

// Prepare is a no-op: the tmux 3.7b crash on popup creation over a zoomed pane
// is specific to floating panes, and display-popup is unaffected.
func (b *TmuxPopup) Prepare(_ context.Context) (func(context.Context) error, error) {
	return nil, nil
}

// NewPinentryHandshake uses the shared tmux handshake: display-popup injects the
// FIFO paths and the guard secrets as popup env (-e).
func (b *TmuxPopup) NewPinentryHandshake(
	ttyFifo, doneFifo string,
) (runinpopup.PinentryHandshake, error) {
	return newTmuxPinentryHandshake(ttyFifo, doneFifo)
}
