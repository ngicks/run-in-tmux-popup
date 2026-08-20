package backend

import (
	"context"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/internal/tmux"
)

var _ runinpopup.TTYHandshaker = (*TmuxPopup)(nil)

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

// Launch opens the spec as a display-popup on this backend's client.
func (b *TmuxPopup) Launch(
	ctx context.Context,
	spec runinpopup.LaunchSpec,
) (runinpopup.PopupHandle, error) {
	return b.tmux.StartPopup(ctx, b.popupRequest(spec))
}

func (b *TmuxPopup) popupRequest(spec runinpopup.LaunchSpec) tmux.PopupRequest {
	return tmux.PopupRequest{
		ClientId: b.clientId,
		Title:    spec.Title,
		Env:      spec.Env,
		X:        spec.X,
		Y:        spec.Y,
		Width:    spec.Width,
		Height:   spec.Height,
		Command:  spec.Command,
		Script:   spec.Script,
	}
}

// Prepare is a no-op: the tmux 3.7b crash on popup creation over a zoomed pane
// is specific to floating panes, and display-popup is unaffected.
func (b *TmuxPopup) Prepare(_ context.Context) (func(context.Context) error, error) {
	return nil, nil
}

// NewTTYHandshake uses the shared tmux handshake: display-popup injects the
// FIFO paths as popup env (-e).
func (b *TmuxPopup) NewTTYHandshake(
	ttyFifo, doneFifo string,
) (runinpopup.TTYHandshake, error) {
	return newTmuxTTYHandshake(ttyFifo, doneFifo)
}
