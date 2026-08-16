package backend

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

// Shared by the two tmux backends. They differ only in the popup mechanism
// (display-popup vs. new-pane); reaching the server and hardening the tty
// handshake work identically, so those parts live here rather than being
// duplicated per backend.

// validateTmuxSessionMeta rejects a session meta that could not stand in for
// $TMUX. It only matters when it is the value that will be used, i.e. when
// tmuxEnv is empty: a caller already inside tmux does not need it at all.
func validateTmuxSessionMeta(sessionMeta, tmuxEnv string) error {
	if tmuxEnv != "" || strings.Contains(sessionMeta, ",") {
		return nil
	}
	return fmt.Errorf(
		"tmux session meta is malformed:"+
			" it must be something like %q but is %q",
		"/run/user/1000/tmux-1000/default,111,0", sessionMeta,
	)
}

// tmuxSessionEnviron sets $TMUX from the session meta when the current process
// has none. Without it every tmux command addresses the default socket instead
// of the server hosting the popup: the popup silently never appears.
func tmuxSessionEnviron(sessionMeta, tmuxEnv string) []string {
	if tmuxEnv != "" || sessionMeta == "" {
		return nil
	}
	return []string{"TMUX=" + sessionMeta}
}

// tmuxTTYHandshakeScript reports the popup's tty on ${TTY_FIFO_FILE}, wrapped in
// the secrets, then blocks until the proxy writes to ${DONE_FIFO_FILE}. The FIFO
// paths and secrets arrive as popup env so they never appear in the tmux argv
// twice.
const tmuxTTYHandshakeScript = "echo ${SEC_PREFIX}$(tty)${SEC_SUFFIX} >> ${TTY_FIFO_FILE}" +
	" && read done < ${DONE_FIFO_FILE}"

// newTmuxPinentryHandshake wraps the announced tty in a per-popup random prefix
// and suffix. Anything else that manages to open the tty FIFO first cannot
// produce them, so its tty is rejected instead of being handed the passphrase
// prompt. Both tmux mechanisms can inject the secrets as popup environment
// (-e), which is what makes the guard worth having here and theater in zellij.
func newTmuxPinentryHandshake(ttyFifo, doneFifo string) (runinpopup.PinentryHandshake, error) {
	prefix, err := randomToken()
	if err != nil {
		return runinpopup.PinentryHandshake{}, err
	}
	suffix, err := randomToken()
	if err != nil {
		return runinpopup.PinentryHandshake{}, err
	}
	return runinpopup.PinentryHandshake{
		Spec: runinpopup.PopupSpec{
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
