package backend

import (
	"context"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/internal/tmux"
)

var _ runinpopup.PinentryHandshaker = (*TmuxPopup)(nil)

// TmuxPopup opens popups with tmux's display-popup ("tmux popup"). The
// popup is a client-side overlay, so it targets a client rather than a session.
type TmuxPopup struct {
	tmux     *tmux.Client
	clientId string
}

// NewTmuxPopup builds the "tmux-popup" backend. It uses BinaryPath
// (default "tmux"), ClientId, SessionMeta and TMUX; SessionId and Shell are not
// needed because display-popup runs on a client and through tmux's own
// default-shell.
//
// SessionMeta is only validated when it is the value that will be used, i.e.
// when TMUX is empty: a caller already inside tmux does not need it at all.
func NewTmuxPopup(opts Options) (*TmuxPopup, error) {
	client, err := tmux.New(tmux.Options{
		Path:        opts.BinaryPath,
		SessionMeta: opts.SessionMeta,
		TMUX:        opts.TMUX,
	})
	if err != nil {
		return nil, err
	}
	return &TmuxPopup{tmux: client, clientId: opts.ClientId}, nil
}

func (b *TmuxPopup) Name() string {
	return NameTmuxPopup
}

// PopupCommand renders the spec as a display-popup targeting this backend's
// client.
func (b *TmuxPopup) PopupCommand(spec runinpopup.PopupSpec) (string, []string) {
	return b.tmux.PopupCommand(tmux.PopupRequest{
		ClientId: b.clientId,
		Title:    spec.Title,
		Env:      spec.Env,
		Command:  spec.Command,
		Script:   spec.Script,
	})
}

// Environ sets $TMUX from the session meta when the current process has none.
// Without $TMUX the popup silently never appears.
func (b *TmuxPopup) Environ() []string {
	return b.tmux.Environ()
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
