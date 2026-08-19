package runinpopup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// TTYHandshake is how one backend's popup announces the terminal it runs on.
type TTYHandshake struct {
	// Spec is the popup payload: it must write the popup's tty name as a single
	// line to the tty FIFO, then block reading the done FIFO so the popup stays
	// open until the caller is finished with that terminal.
	Spec PopupSpec
	// ValidateTTY extracts the tty name from the line read off the tty FIFO,
	// rejecting anything the popup would not have written. nil accepts the line
	// as-is, minus surrounding space.
	ValidateTTY func(line string) (string, error)
}

// TTYHandshaker is a Backend whose popups can report the terminal they run on
// and stay alive until they are dismissed.
//
// Backends build the handshake themselves because the popup mechanism decides
// how the payload receives the FIFO paths — tmux injects them as popup env,
// zellij can only embed them in the command line.
type TTYHandshaker interface {
	Backend
	// NewTTYHandshake builds a handshake for one popup.
	NewTTYHandshake(ttyFifo, doneFifo string) (TTYHandshake, error)
}

// ttyRendezvous hands out the terminal a popup is drawing on and dismisses that
// popup once the caller is done with it.
type ttyRendezvous interface {
	// acquire opens a popup and blocks until it announces its terminal.
	acquire(ctx context.Context) (tty string, err error)
	// dismiss releases the terminal back to the popup and gives back what the
	// launch took. It does nothing when no popup was ever opened.
	dismiss() error
}

// popupTTYHandshake runs the FIFO half of the exchange against a real backend.
//
// The order below is the protocol: both FIFOs exist before the popup is
// launched, the announcement is read before pinentry is told anything, and the
// dismissal is written before the launch is released. The payload does the
// mirror image of it, and neither half can be reordered without stranding the
// other.
type popupTTYHandshake struct {
	backend  TTYHandshaker
	launcher *PopupLauncher
	logger   *slog.Logger
	// dir holds the two FIFOs. It must not be writable by other users: anything
	// that can open the tty FIFO can announce a terminal of its own and be handed
	// the passphrase prompt.
	dir string
	// readTimeout bounds waiting for the announcement, dismissTimeout writing the
	// dismissal.
	readTimeout, dismissTimeout time.Duration

	// popup and doneFifo are set once the popup is open — the point from which
	// there is something to dismiss.
	popup    *PopupCommand
	doneFifo string
}

func (h *popupTTYHandshake) acquire(ctx context.Context) (string, error) {
	ttyFifo := filepath.Join(h.dir, "tty")
	doneFifo := filepath.Join(h.dir, "done")

	// Both FIFOs exist before the popup does: announcing its terminal and blocking
	// on the dismissal are the payload's first two acts, and a FIFO that is not
	// there yet fails the payload instead of making it wait.
	for _, s := range []string{ttyFifo, doneFifo} {
		if err := syscall.Mknod(s, syscall.S_IFIFO|0o600, 0); err != nil {
			return "", fmt.Errorf("creating fifo %q: %w", s, err)
		}
	}

	h.logger.Debug("tty fifo created")

	handshake, err := h.backend.NewTTYHandshake(ttyFifo, doneFifo)
	if err != nil {
		return "", fmt.Errorf("backend %s: building tty handshake: %w", h.backend.Name(), err)
	}

	// No payload stdio is allocated: the handshake payload announces its terminal
	// over the fifos above and must keep the popup's own terminal untouched, since
	// that terminal is the whole point of the exchange.
	popup, err := h.launcher.Exec(ctx, handshake.Spec, PopupStreams{})
	if err != nil {
		return "", err
	}
	h.popup, h.doneFifo = popup, doneFifo

	h.logger.Debug("opening tty fifo")
	// Read-write rather than read-only: this end is then a writer too, so the open
	// returns at once instead of blocking until the payload arrives, and the read
	// below cannot end in an EOF the moment the payload closes its end. Nothing
	// but the deadline ends the wait, which is what turns a popup that never
	// announces anything into a timeout rather than an empty answer.
	f, err := os.OpenFile(ttyFifo, os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("failed to open tty: %w", err)
	}
	// The announcement is a single line, so this end has nothing left to do once
	// it has arrived; what keeps the popup waiting is the done FIFO, not this one.
	defer f.Close()

	_ = f.SetReadDeadline(time.Now().Add(h.readTimeout))

	scanner := bufio.NewScanner(f)

	h.logger.Debug("waiting tty notification")
	scanner.Scan()
	line := scanner.Text()
	if scanner.Err() != nil {
		return "", fmt.Errorf("scan failed: %w", scanner.Err())
	}

	validateTTY := handshake.ValidateTTY
	if validateTTY == nil {
		validateTTY = func(line string) (string, error) { return strings.TrimSpace(line), nil }
	}
	tty, err := validateTTY(line)
	if err != nil {
		return "", err
	}

	h.logger.Debug("popup started")

	if tty == "" {
		return "", errors.New("the popup announced an empty tty")
	}

	h.logger.Debug("got TTY from popup")
	return tty, nil
}

func (h *popupTTYHandshake) dismiss() error {
	if h.popup == nil {
		return nil
	}
	// The popup goes away over the done FIFO, not by waiting for the launcher, so
	// releasing the launch is what reaps the launcher, logs whatever it printed
	// and puts the multiplexer state back — after the dismissal has been sent, or
	// there would be nothing left to send it to.
	defer h.popup.release()

	h.logger.Debug("waiting to done fifo")
	// Read-write again: a popup the user dismissed by hand is already gone, and a
	// write-only open would block forever waiting for a reader that never comes.
	done, err := os.OpenFile(h.doneFifo, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("opening done fifo: %w", err)
	}
	defer done.Close()

	_ = done.SetWriteDeadline(time.Now().Add(h.dismissTimeout))
	if _, err := done.Write([]byte("done\n")); err != nil {
		return fmt.Errorf("writing done fifo: %w", err)
	}
	return nil
}
