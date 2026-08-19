package backend

import (
	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

// Shared by the two tmux backends. They differ only in the popup mechanism
// (display-popup vs. new-pane); the tty handshake works identically, so it
// lives here rather than being duplicated per backend.

// tmuxTTYHandshakeScript reports the popup's tty on ${TTY_FIFO_FILE}, then
// blocks until the proxy writes to ${DONE_FIFO_FILE}. The FIFO paths arrive as
// popup env so they never appear in the tmux argv twice.
const tmuxTTYHandshakeScript = "echo $(tty) >> ${TTY_FIFO_FILE}" +
	" && read done < ${DONE_FIFO_FILE}"

// newTmuxTTYHandshake announces the tty as-is. Earlier versions wrapped it in
// per-popup random prefix/suffix secrets injected through the popup env, but
// the FIFOs live as mode-0600 files in a mode-0700 workspace, so filesystem
// permissions already decide who can speak on them — the secrets re-checked
// the same boundary and are gone.
func newTmuxTTYHandshake(ttyFifo, doneFifo string) (runinpopup.TTYHandshake, error) {
	return runinpopup.TTYHandshake{
		Spec: runinpopup.PopupSpec{
			Env: map[string]string{
				"TTY_FIFO_FILE":  ttyFifo,
				"DONE_FIFO_FILE": doneFifo,
			},
			Script: tmuxTTYHandshakeScript,
		},
	}, nil
}
