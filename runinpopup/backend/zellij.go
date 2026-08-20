package backend

import (
	"context"
	"errors"
	"fmt"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/internal/geometry"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/internal/zellij"
)

var _ runinpopup.TTYHandshaker = (*Zellij)(nil)

// Zellij opens popups as zellij floating panes ("zellij run
// --floating"). zellij addresses sessions, not clients, so there is no client
// targeting here.
type Zellij struct {
	zellij    *zellij.Client
	sessionId string
}

// NewZellij builds the "zellij" backend. It uses BinaryPath (default
// "zellij"), SessionId and Shell (default "sh"); ClientId, SessionMeta and TMUX
// are ignored, since zellij cannot target a client and needs no session meta in
// the environment.
func NewZellij(opts Options) (*Zellij, error) {
	return &Zellij{
		zellij: zellij.New(zellij.Options{
			Path:  opts.BinaryPath,
			Shell: opts.Shell,
		}),
		sessionId: opts.SessionId,
	}, nil
}

func (b *Zellij) Name() string {
	return NameZellij
}

// Launch opens the spec as a floating pane in this backend's session. zellij
// takes the session as a flag, so the launcher needs no environment of its own.
func (b *Zellij) Launch(
	ctx context.Context,
	spec runinpopup.LaunchSpec,
) (runinpopup.PopupHandle, error) {
	req, err := b.runRequest(spec)
	if err != nil {
		return nil, err
	}
	return b.zellij.StartRun(ctx, req)
}

// runRequest completes the launch into a zellij run, placing what does not fit
// in the argv beside it: "zellij run" has no environment flag, and an argv is
// readable by every process for as long as the pane lives, so an environment is
// delivered over a FIFO in the launch's work directory and sourced by the
// payload — the zellij client owns that delivery, since it also owns the
// launcher whose Wait has to join it.
func (b *Zellij) runRequest(spec runinpopup.LaunchSpec) (zellij.RunRequest, error) {
	if err := rejectTmuxPositions(spec); err != nil {
		return zellij.RunRequest{}, err
	}
	if len(spec.Env) > 0 && spec.WorkDir == "" {
		return zellij.RunRequest{}, errors.New(
			"the launch has no work directory to deliver the popup environment in",
		)
	}
	return zellij.RunRequest{
		SessionId:      b.sessionId,
		Title:          spec.Title,
		Env:            spec.Env,
		WorkDir:        spec.WorkDir,
		StartupTimeout: spec.StartupTimeout,
		X:              spec.X,
		Y:              spec.Y,
		Width:          spec.Width,
		Height:         spec.Height,
		Command:        spec.Command,
		Script:         spec.Script,
	}, nil
}

// rejectTmuxPositions refuses the popup positions zellij cannot express. Cells
// and percentages are its own vocabulary too, but the single-letter specifiers
// are tmux's alone — "the centre of the terminal" is not something "zellij run"
// can be asked for — and a pane placed at a guessed position instead is worse
// than one that never opened. Only X and Y are looked at: a specifier is the
// one value the size fields cannot hold, and the launch layer has already said
// so by the time a request is built.
func rejectTmuxPositions(spec runinpopup.LaunchSpec) error {
	for _, value := range []string{spec.X, spec.Y} {
		if geometry.IsPosition(value) {
			return fmt.Errorf(
				"backend %s: position %q is tmux-specific; use cells or a percentage",
				NameZellij, value,
			)
		}
	}
	return nil
}

// Prepare is a no-op: nothing in zellij's floating-pane creation depends on the
// state of the surrounding pane.
func (b *Zellij) Prepare(_ context.Context) (func(context.Context) error, error) {
	return nil, nil
}

// zellijHandshakePaneName is the floating pane's name during the handshake. It
// still reads "pinentry-curses" — the name cmd/zellij-popup-pinentry-curses
// shipped — so the argv this package builds stays identical to that binary's.
const zellijHandshakePaneName = "pinentry-curses"

// NewTTYHandshake announces the tty as-is. The FIFO paths travel in the argv
// itself: "zellij run" has no env injection flag, and a path is not what the
// sourced environment FIFO exists to keep out of a command line.
func (b *Zellij) NewTTYHandshake(
	ttyFifo, doneFifo string,
) (runinpopup.TTYHandshake, error) {
	return runinpopup.TTYHandshake{
		Spec: runinpopup.PopupSpec{
			Title:  zellijHandshakePaneName,
			Script: fmt.Sprintf("echo $(tty) >> %s && read done < %s", ttyFifo, doneFifo),
		},
	}, nil
}
