package runinpopup

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

var _ PinentryHandshaker = (*TmuxPopupBackend)(nil)

// TmuxPopupBackend opens popups with tmux's display-popup ("tmux popup"). The
// popup is a client-side overlay, so it targets a client rather than a session.
type TmuxPopupBackend struct {
	tmuxPath    string
	clientId    string
	sessionMeta string
	tmuxEnv     string
}

// NewTmuxPopupBackend builds the "tmux-popup" backend. It uses BinaryPath
// (default "tmux"), ClientId, SessionMeta and TMUX; SessionId and Shell are not
// needed because display-popup runs on a client and through tmux's own
// default-shell.
//
// SessionMeta is only validated when it is the value that will be used, i.e.
// when TMUX is empty: a caller already inside tmux does not need it at all.
func NewTmuxPopupBackend(opts BackendOptions) (*TmuxPopupBackend, error) {
	b := &TmuxPopupBackend{
		tmuxPath:    cmp.Or(opts.BinaryPath, "tmux"),
		clientId:    opts.ClientId,
		sessionMeta: opts.SessionMeta,
		tmuxEnv:     opts.TMUX,
	}
	if b.tmuxEnv == "" && !strings.Contains(b.sessionMeta, ",") {
		return nil, fmt.Errorf(
			"tmux session meta is malformed:"+
				" it must be something like %q but is %q",
			"/run/user/1000/tmux-1000/default,111,0", b.sessionMeta,
		)
	}
	return b, nil
}

func (b *TmuxPopupBackend) Name() string {
	return BackendTmuxPopup
}

// PopupCommand builds "tmux popup -c <client> [-T <title>] [-e KEY=VALUE...] -E
// <command line>". display-popup takes a shell command line, so an argv payload
// is quoted and joined into one.
func (b *TmuxPopupBackend) PopupCommand(spec PopupSpec) (string, []string) {
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
	args = append(args, "-E", spec.shellCommandLine())
	return b.tmuxPath, args
}

// Environ sets $TMUX from the session meta when the current process has none.
// Without $TMUX the popup silently never appears.
func (b *TmuxPopupBackend) Environ() []string {
	if b.tmuxEnv != "" || b.sessionMeta == "" {
		return nil
	}
	return []string{"TMUX=" + b.sessionMeta}
}

// Prepare is a no-op: the tmux 3.7b crash on popup creation over a zoomed pane
// is specific to floating panes, and display-popup is unaffected.
func (b *TmuxPopupBackend) Prepare(_ context.Context) (func(context.Context) error, error) {
	return nil, nil
}

// tmuxTTYHandshakeScript reports the popup's tty on ${TTY_FIFO_FILE}, wrapped in
// the secrets, then blocks until the proxy writes to ${DONE_FIFO_FILE}. The FIFO
// paths and secrets arrive as popup env so they never appear in the tmux argv
// twice.
const tmuxTTYHandshakeScript = "echo ${SEC_PREFIX}$(tty)${SEC_SUFFIX} >> ${TTY_FIFO_FILE}" +
	" && read done < ${DONE_FIFO_FILE}"

// NewPinentryHandshake wraps the announced tty in a per-popup random prefix and
// suffix. Anything else that manages to open the tty FIFO first cannot produce
// them, so its tty is rejected instead of being handed the passphrase prompt.
func (b *TmuxPopupBackend) NewPinentryHandshake(
	ttyFifo, doneFifo string,
) (PinentryHandshake, error) {
	prefix, err := randomToken()
	if err != nil {
		return PinentryHandshake{}, err
	}
	suffix, err := randomToken()
	if err != nil {
		return PinentryHandshake{}, err
	}
	return PinentryHandshake{
		Spec: PopupSpec{
			Env: map[string]string{
				"TTY_FIFO_FILE":  ttyFifo,
				"DONE_FIFO_FILE": doneFifo,
				"SEC_PREFIX":     prefix,
				"SEC_SUFFIX":     suffix,
			},
			Script: tmuxTTYHandshakeScript,
		},
		ValidateTTY: guardedTTYValidator(prefix, suffix),
	}, nil
}

// guardedTTYValidator peels the prefix/suffix guard off the announced tty.
func guardedTTYValidator(prefix, suffix string) func(string) (string, error) {
	return func(line string) (string, error) {
		line, ok := strings.CutPrefix(line, prefix)
		if !ok {
			return "", errors.New("suspicious sender: incorrect prefix")
		}
		targetTty, ok := strings.CutSuffix(line, suffix)
		if !ok {
			return "", errors.New("suspicious sender: incorrect suffix")
		}
		return targetTty, nil
	}
}

// randomToken returns 128 hex-encoded random bits, the width the legacy binary
// used.
func randomToken() (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", fmt.Errorf("generating random token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
