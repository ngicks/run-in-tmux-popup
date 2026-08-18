package backend

import (
	"context"
	"fmt"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/internal/tmux"
)

var _ runinpopup.PinentryHandshaker = (*TmuxFloatingPane)(nil)

// TmuxFloatingPane opens popups as tmux floating panes ("tmux new-pane",
// bound to `*` by default). A floating pane belongs to a window rather than to a
// client, so it is addressed by session.
type TmuxFloatingPane struct {
	tmux      *tmux.Client
	sessionId string
}

// NewTmuxFloatingPane builds the "tmux-floating-pane" backend. It uses
// BinaryPath (default "tmux"), SessionId, SessionMeta and TMUX. Shell is not
// needed because tmux runs the payload through its own default-shell, and
// ClientId cannot be honored at all: new-pane has no client-targeting flag —
// there is no equivalent of display-popup's -c, because the pane lives in a
// window and every client viewing that window sees it.
//
// SessionMeta is only validated when it is the value that will be used, i.e.
// when TMUX is empty: a caller already inside tmux does not need it at all.
func NewTmuxFloatingPane(opts Options) (*TmuxFloatingPane, error) {
	client, err := tmux.New(tmux.Options{
		Path:        opts.BinaryPath,
		SessionMeta: opts.SessionMeta,
		TMUX:        opts.TMUX,
	})
	if err != nil {
		return nil, err
	}
	return &TmuxFloatingPane{tmux: client, sessionId: opts.SessionId}, nil
}

func (b *TmuxFloatingPane) Name() string {
	return NameTmuxFloatingPane
}

// Launch opens the spec as a floating pane in this backend's session.
func (b *TmuxFloatingPane) Launch(
	ctx context.Context,
	spec runinpopup.LaunchSpec,
) (runinpopup.PopupHandle, error) {
	return b.tmux.StartNewPane(ctx, b.paneRequest(spec))
}

// paneRequest translates the spec for new-pane. spec.Title is dropped: new-pane
// has no title flag.
func (b *TmuxFloatingPane) paneRequest(spec runinpopup.LaunchSpec) tmux.PaneRequest {
	return tmux.PaneRequest{
		SessionId: b.sessionId,
		Env:       spec.Env,
		Command:   spec.Command,
		Script:    spec.Script,
	}
}

// NewPinentryHandshake uses the shared tmux handshake: new-pane injects the FIFO
// paths and the guard secrets as pane env (-e), same as display-popup.
func (b *TmuxFloatingPane) NewPinentryHandshake(
	ttyFifo, doneFifo string,
) (runinpopup.PinentryHandshake, error) {
	return newTmuxPinentryHandshake(ttyFifo, doneFifo)
}

// Prepare de-zooms the window before the floating pane is created, and returns a
// restore func re-zooming the pane that was zoomed — best-effort, its error is
// logged by the caller rather than failing the run.
//
// This is the tmux 3.7b crash the Prepare seam exists for: a floating pane
// created while a pane in the window is zoomed takes the whole server down —
// observed here when the floating pane's command exits, which is exactly what
// the pinentry handshake payload does. Fixed in 3.7c, so the workaround is
// version-gated.
//
// Failing to read the zoom state on an affected tmux aborts: creating the
// floating pane without knowing whether the window is zoomed is the one move
// that can crash the user's server.
//
// The zoomed pane is remembered by id rather than re-derived from the session on
// restore: by then the session's active pane may still be the popup, and
// toggling zoom on that would zoom the popup instead of the user's pane.
//
// Restore does not wait for the popup, and no caller guarantees it is gone: it
// runs when the launch is released, which for new-pane is long after the
// launcher returned and may be while the pane still lives — the pinentry
// exchange releases once it has written the done FIFO, without waiting for the
// pane to act on it. What makes that safe is measured, not ordered — re-zooming
// while a floating pane is still alive un-floats it into the layout rather than
// crashing the server. So the worst case is a popup pulled out of its float.
func (b *TmuxFloatingPane) Prepare(
	ctx context.Context,
) (func(context.Context) error, error) {
	// A tmux that cannot report its version tells us nothing about whether it is
	// fixed, so it falls through to the zoom query like an affected one.
	if version, err := b.tmux.Version(ctx); err == nil && !tmux.AffectedByZoomCrash(version) {
		return nil, nil
	}

	zoomed, paneId, err := b.tmux.ZoomedPane(ctx, b.sessionId)
	if err != nil {
		return nil, err
	}
	if !zoomed {
		return nil, nil
	}
	if err := b.tmux.ToggleZoom(ctx, paneId); err != nil {
		return nil, fmt.Errorf("de-zooming pane %s: %w", paneId, err)
	}
	return func(ctx context.Context) error {
		if err := b.tmux.ToggleZoom(ctx, paneId); err != nil {
			return fmt.Errorf("re-zooming pane %s: %w", paneId, err)
		}
		return nil
	}, nil
}
