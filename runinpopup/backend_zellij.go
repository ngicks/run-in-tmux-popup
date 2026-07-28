package runinpopup

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
)

var _ PinentryHandshaker = (*ZellijBackend)(nil)

// ZellijBackend opens popups as zellij floating panes ("zellij run
// --floating"). zellij addresses sessions, not clients, so there is no client
// targeting here.
type ZellijBackend struct {
	zellijPath string
	sessionId  string
	shell      string
}

// NewZellijBackend builds the "zellij" backend. It uses BinaryPath (default
// "zellij"), SessionId and Shell (default "sh"); ClientId, SessionMeta and TMUX
// are ignored, since zellij cannot target a client and needs no session meta in
// the environment.
func NewZellijBackend(opts BackendOptions) (*ZellijBackend, error) {
	return &ZellijBackend{
		zellijPath: cmp.Or(opts.BinaryPath, "zellij"),
		sessionId:  opts.SessionId,
		shell:      cmp.Or(opts.Shell, "sh"),
	}, nil
}

func (b *ZellijBackend) Name() string {
	return BackendZellij
}

// PopupCommand builds "zellij --session=<id> run [--name=<title>] --floating
// --close-on-exit --pinned=true -- <payload>". zellij runs the payload argv
// directly, so a script payload — or an env injection, for which zellij has no
// flag — is wrapped in a shell.
func (b *ZellijBackend) PopupCommand(spec PopupSpec) (string, []string) {
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

func (b *ZellijBackend) payload(spec PopupSpec) []string {
	if spec.Script == "" && len(spec.Env) == 0 {
		return slices.Clone(spec.Command)
	}
	var sb strings.Builder
	for _, k := range slices.Sorted(maps.Keys(spec.Env)) {
		fmt.Fprintf(&sb, "export %s=%s; ", k, shellQuote(spec.Env[k]))
	}
	sb.WriteString(spec.shellCommandLine())
	return []string{b.shell, "-c", sb.String()}
}

// Environ returns nil: zellij takes the session as a flag, so the popup command
// needs no environment of its own.
func (b *ZellijBackend) Environ() []string {
	return nil
}

// Prepare is a no-op: nothing in zellij's floating-pane creation depends on the
// state of the surrounding pane.
func (b *ZellijBackend) Prepare(_ context.Context) (func(context.Context) error, error) {
	return nil, nil
}

// zellijPinentryPaneName is the floating pane's name during the pinentry
// handshake, unchanged from cmd/zellij-popup-pinentry-curses so the argv this
// package builds stays identical to the one that binary shipped.
const zellijPinentryPaneName = "pinentry-curses"

// NewPinentryHandshake announces the tty unguarded: "zellij run" has no env
// injection flag, so a prefix/suffix secret would have to travel in the argv,
// where any process on the machine can read it — the guard would be theater.
func (b *ZellijBackend) NewPinentryHandshake(ttyFifo, doneFifo string) (PinentryHandshake, error) {
	return PinentryHandshake{
		Spec: PopupSpec{
			Title:  zellijPinentryPaneName,
			Script: fmt.Sprintf("echo $(tty) >> %s && read done < %s", ttyFifo, doneFifo),
		},
	}, nil
}
