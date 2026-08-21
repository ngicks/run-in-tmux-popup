package backend

import (
	"context"
	"fmt"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/internal/geometry"
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
	req, err := b.popupRequest(spec)
	if err != nil {
		return nil, err
	}
	return b.tmux.StartPopup(ctx, req)
}

// popupRequest translates the spec for display-popup. Y is the one value that
// does not arrive as it was written: a spec's Y is the popup's top edge, the
// same as everywhere else, while display-popup's -y is its bottom one.
func (b *TmuxPopup) popupRequest(spec runinpopup.LaunchSpec) (tmux.PopupRequest, error) {
	y, err := popupBottomEdge(spec.Y, spec.Height)
	if err != nil {
		return tmux.PopupRequest{}, err
	}
	return tmux.PopupRequest{
		ClientId: b.clientId,
		Title:    spec.Title,
		Env:      spec.Env,
		X:        spec.X,
		Y:        y,
		Width:    spec.Width,
		Height:   spec.Height,
		Command:  spec.Command,
		Script:   spec.Script,
	}, nil
}

// popupBottomEdge renders a top-edge y as the bottom-edge coordinate
// display-popup places a popup by, which is y plus the popup's own height. X
// needs nothing of the kind: the left edge is the left edge on every mechanism.
//
// Nothing is clamped here. tmux clamps a popup to the terminal itself, so a
// converted value at the edge lands where tmux decides it lands, and a second
// opinion about the bounds would only be a different wrong answer; the
// conversion is the whole job.
//
// A position specifier is not a row and passes through: "the centre of the
// terminal" names a placement tmux works out with the height already in hand.
// An empty y is likewise left alone — it puts no flag on the command line at
// all — and so needs no height beside it.
func popupBottomEdge(y, height string) (string, error) {
	if y == "" || geometry.IsPosition(y) {
		return y, nil
	}
	sum, ok := geometry.Sum(y, height)
	if !ok {
		return "", fmt.Errorf(
			"backend %s: popup geometry Y %q needs Height in the same unit, got %q:"+
				" display-popup places a popup by its bottom edge, so the top edge Y"+
				" names is only reachable by adding the height to it",
			NameTmuxPopup, y, height,
		)
	}
	return sum, nil
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
