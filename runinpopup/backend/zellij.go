package backend

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

var _ runinpopup.PinentryHandshaker = (*Zellij)(nil)

// Zellij opens popups as zellij floating panes ("zellij run
// --floating"). zellij addresses sessions, not clients, so there is no client
// targeting here.
type Zellij struct {
	zellijPath string
	sessionId  string
	shell      string
}

// NewZellij builds the "zellij" backend. It uses BinaryPath (default
// "zellij"), SessionId and Shell (default "sh"); ClientId, SessionMeta and TMUX
// are ignored, since zellij cannot target a client and needs no session meta in
// the environment.
func NewZellij(opts Options) (*Zellij, error) {
	return &Zellij{
		zellijPath: cmp.Or(opts.BinaryPath, "zellij"),
		sessionId:  opts.SessionId,
		shell:      cmp.Or(opts.Shell, "sh"),
	}, nil
}

func (b *Zellij) Name() string {
	return NameZellij
}

// PopupCommand builds "zellij --session=<id> run [--name=<title>] --floating
// --close-on-exit --pinned=true -- <payload>". zellij runs the payload argv
// directly, so a script payload — or an env injection, for which zellij has no
// flag — is wrapped in a shell.
func (b *Zellij) PopupCommand(spec runinpopup.PopupSpec) (string, []string) {
	var args []string
	if b.sessionId != "" {
		args = append(args, "--session="+b.sessionId)
	}
	args = append(args, "run")
	if spec.Title != "" {
		args = append(args, "--name="+spec.Title)
	}
	args = append(args, "--floating", "--close-on-exit", "--pinned=true", "--")
	return b.zellijPath, append(args, b.payload(spec)...)
}

func (b *Zellij) payload(spec runinpopup.PopupSpec) []string {
	if spec.Script == "" && len(spec.Env) == 0 {
		return slices.Clone(spec.Command)
	}
	var sb strings.Builder
	for _, k := range slices.Sorted(maps.Keys(spec.Env)) {
		fmt.Fprintf(&sb, "export %s=%s; ", k, shellQuote(spec.Env[k]))
	}
	sb.WriteString(commandLine(spec))
	return []string{b.shell, "-c", sb.String()}
}

// Environ returns nil: zellij takes the session as a flag, so the popup command
// needs no environment of its own.
func (b *Zellij) Environ() []string {
	return nil
}

// Prepare is a no-op: nothing in zellij's floating-pane creation depends on the
// state of the surrounding pane.
func (b *Zellij) Prepare(_ context.Context) (func(context.Context) error, error) {
	return nil, nil
}

// zellijPinentryPaneName is the floating pane's name during the pinentry
// handshake, unchanged from cmd/zellij-popup-pinentry-curses so the argv this
// package builds stays identical to the one that binary shipped.
const zellijPinentryPaneName = "pinentry-curses"

// NewPinentryHandshake announces the tty unguarded: "zellij run" has no env
// injection flag, so a prefix/suffix secret would have to travel in the argv,
// where any process on the machine can read it — the guard would be theater.
func (b *Zellij) NewPinentryHandshake(
	ttyFifo, doneFifo string,
) (runinpopup.PinentryHandshake, error) {
	return runinpopup.PinentryHandshake{
		Spec: runinpopup.PopupSpec{
			Title:  zellijPinentryPaneName,
			Script: fmt.Sprintf("echo $(tty) >> %s && read done < %s", ttyFifo, doneFifo),
		},
	}, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
