package backend

import (
	"context"
	"fmt"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
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
	return b.zellij.StartRun(ctx, b.runRequest(spec))
}

func (b *Zellij) runRequest(spec runinpopup.LaunchSpec) zellij.RunRequest {
	return zellij.RunRequest{
		SessionId: b.sessionId,
		Title:     spec.Title,
		Env:       spec.Env,
		Command:   spec.Command,
		Script:    spec.Script,
	}
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

// NewTTYHandshake announces the tty unguarded: "zellij run" has no env
// injection flag, so a prefix/suffix secret would have to travel in the argv,
// where any process on the machine can read it — the guard would be theater.
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
