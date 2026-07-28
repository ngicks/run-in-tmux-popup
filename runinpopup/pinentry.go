package runinpopup

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// PinentryHandshake is how one backend's popup announces the tty it runs on.
type PinentryHandshake struct {
	// Spec is the popup payload: it must write the popup's tty name as a single
	// line to the tty FIFO, then block reading the done FIFO so the popup stays
	// open until the proxy is finished with the tty.
	Spec PopupSpec
	// ValidateTTY extracts the tty name from the line read off the tty FIFO,
	// rejecting anything the popup would not have written. nil accepts the line
	// as-is, minus surrounding space.
	ValidateTTY func(line string) (string, error)
}

// PinentryHandshaker is a Backend that can host the pinentry tty handshake.
// Backends build the handshake themselves because the popup mechanism decides
// how the payload receives the FIFO paths — tmux injects them as popup env,
// zellij can only embed them in the command line — and therefore how well the
// announcement can be hardened.
type PinentryHandshaker interface {
	Backend
	// NewPinentryHandshake builds a handshake for one popup. It is called once
	// per CallPinentry so a backend may mint per-popup secrets.
	NewPinentryHandshake(ttyFifo, doneFifo string) (PinentryHandshake, error)
}

// PinentryOptions configures CallPinentry.
type PinentryOptions struct {
	// Logger receives progress logs. nil discards them.
	Logger *slog.Logger
	// TempDir is where the two handshake FIFOs are created. Required: the
	// caller owns the directory's lifetime, so it can keep it for debugging.
	// It must not be writable by other users, since anything that can open the
	// tty FIFO can try to redirect the passphrase prompt.
	TempDir string
	// PinentryPath is the pinentry binary run outside the popup, on the tty the
	// popup reported. Empty means DefaultConfig().PinentryPath.
	PinentryPath string
	// PinentryArgs is passed through to that binary, normally this process's own
	// arguments as gpg-agent invoked them.
	PinentryArgs []string
	// Timeouts bounds each stage of the exchange. Zero fields fall back to
	// DefaultConfig().Timeouts.
	Timeouts TimeoutsConfig
}

func (o PinentryOptions) normalized() (PinentryOptions, error) {
	if o.TempDir == "" {
		return o, errors.New("PinentryOptions.TempDir must be set")
	}
	def := DefaultConfig()
	o.PinentryPath = cmp.Or(o.PinentryPath, def.PinentryPath)
	o.Timeouts.Overall = cmp.Or(o.Timeouts.Overall, def.Timeouts.Overall)
	o.Timeouts.TTYRead = cmp.Or(o.Timeouts.TTYRead, def.Timeouts.TTYRead)
	o.Timeouts.DoneWrite = cmp.Or(o.Timeouts.DoneWrite, def.Timeouts.DoneWrite)
	return o, nil
}

// CallPinentry proxies the Assuan exchange on this process's stdin/stdout to a
// pinentry running on a popup's tty.
//
// It opens a popup through backend whose only job is to report its tty and stay
// alive, runs pinentry outside the popup on that tty, and rewrites the
// "OPTION ttyname=" line gpg-agent sends so pinentry draws in the popup instead
// of on the terminal gpg-agent happened to pick. The popup is dismissed once
// pinentry exits.
//
// backend must implement PinentryHandshaker. The whole exchange is bounded by
// opts.Timeouts.Overall.
func CallPinentry(ctx context.Context, backend Backend, opts PinentryOptions) error {
	opts, err := opts.normalized()
	if err != nil {
		return err
	}
	handshaker, ok := backend.(PinentryHandshaker)
	if !ok {
		return fmt.Errorf(
			"backend %q does not implement runinpopup.PinentryHandshaker:"+
				" it cannot host the pinentry tty handshake",
			backend.Name(),
		)
	}
	logger := loggerOrDiscard(opts.Logger)

	ctx, cancel := context.WithTimeout(ctx, opts.Timeouts.Overall)
	defer cancel()

	return withPopupPrepared(ctx, logger, backend, func(ctx context.Context) error {
		return callPinentry(ctx, handshaker, logger, opts)
	})
}

// callPinentry is a mechanical port of internal/popup.CallPinentry. The FIFO
// open/read/write order is timing-sensitive, so it stays one linear flow rather
// than being split into helpers that would invite reordering.
func callPinentry(
	ctx context.Context,
	backend PinentryHandshaker,
	logger *slog.Logger,
	opts PinentryOptions,
) (err error) {
	ttyFifo := filepath.Join(opts.TempDir, "tty")
	doneFifo := filepath.Join(opts.TempDir, "done")

	for _, s := range []string{ttyFifo, doneFifo} {
		if err := syscall.Mknod(s, syscall.S_IFIFO|0o600, 0); err != nil {
			return fmt.Errorf("creating fifo %q: %w", s, err)
		}
	}

	logger.Debug("tty fifo created")

	handshake, err := backend.NewPinentryHandshake(ttyFifo, doneFifo)
	if err != nil {
		return fmt.Errorf("backend %s: building tty handshake: %w", backend.Name(), err)
	}

	popupCmdStdout := new(bytes.Buffer)
	popupCmdStderr := new(bytes.Buffer)

	popupCmd, err := startPopup(
		ctx,
		logger,
		backend,
		handshake.Spec,
		popupCmdStdout,
		popupCmdStderr,
	)
	if err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		_ = popupCmd.Process.Kill()
		logger.Debug(
			"popup cmd out:",
			slog.String("stdout", popupCmdStdout.String()),
			slog.String("stderr", popupCmdStderr.String()),
		)
	}()

	defer func() {
		logger.Debug("waiting to done fifo")
		// Failing to dismiss the popup is worth reporting — it lingers until the
		// overall timeout kills it — but never worth hiding a pinentry failure,
		// so it only becomes the returned error when there is none.
		done, openErr := os.OpenFile(doneFifo, os.O_RDWR, 0)
		if openErr != nil {
			logger.Warn("opening done fifo failed", slog.Any("err", openErr))
			err = cmp.Or(err, fmt.Errorf("opening done fifo: %w", openErr))
			return
		}
		defer done.Close()
		_ = done.SetWriteDeadline(time.Now().Add(opts.Timeouts.DoneWrite))
		if _, writeErr := done.Write([]byte("done\n")); writeErr != nil {
			logger.Warn("writing done fifo failed", slog.Any("err", writeErr))
			err = cmp.Or(err, fmt.Errorf("writing done fifo: %w", writeErr))
		}
	}()

	logger.Debug("opening tty fifo")
	f, err := os.OpenFile(ttyFifo, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open tty: %w", err)
	}
	defer f.Close()

	_ = f.SetReadDeadline(time.Now().Add(opts.Timeouts.TTYRead))

	scanner := bufio.NewScanner(f)

	logger.Debug("waiting tty notification")
	scanner.Scan()
	t := scanner.Text()
	if scanner.Err() != nil {
		return fmt.Errorf("scan failed: %w", scanner.Err())
	}

	validateTTY := handshake.ValidateTTY
	if validateTTY == nil {
		validateTTY = func(line string) (string, error) { return strings.TrimSpace(line), nil }
	}
	targetTty, err := validateTTY(t)
	if err != nil {
		return err
	}

	logger.Debug("popup started")

	if targetTty == "" {
		return errors.New("popup return an empty tty")
	}

	logger.Debug("got TTY from popup")

	// Run pinentry with stdin interception
	cmd := exec.CommandContext(ctx, opts.PinentryPath, opts.PinentryArgs...)
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGINT)
	}

	p, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("%s failed to start: %w", opts.PinentryPath, err)
	}

	go func() {
		<-ctx.Done()
		_ = cmd.Process.Kill()
	}()

	logger.Debug("pinentry started")

	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("creating stdin pipe: %w", err)
	}

	var wg sync.WaitGroup

	go func() {
		// don't wait on this gorountine.
		// I'm not sure but closing os.Stdin doesn't have
		// effect to unblock reading on it.
		_, _ = io.Copy(w, os.Stdin)
		logger.Debug("piping done")
	}()

	wg.Go(func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()

			// Replace ttyname option with the popup's TTY
			if strings.HasPrefix(line, "OPTION ttyname=") {
				original := line
				line = "OPTION ttyname=" + targetTty
				logger.Debug(
					"replaced ttyname",
					slog.String("old", original),
					slog.String("new", line),
				)
			}

			logger.Debug("forwarding input", slog.String("line", line))

			_, err := p.Write([]byte(line + "\n"))
			if err != nil {
				logger.Warn("write error", slog.Any("err", err))
				break
			}
			logger.Debug("written")
		}

		logger.Debug("loop exited")

		if err := scanner.Err(); err != nil {
			logger.Warn("scanner error", slog.Any("err", err))
		}
	})

	err = cmd.Wait()
	if err != nil {
		if execErr, ok := errors.AsType[*exec.ExitError](err); ok {
			err = fmt.Errorf("%v: stderr = %s", execErr, string(execErr.Stderr))
		}
	}

	logger.Debug("pinentry finished", slog.Any("err", err))

	logger.Debug(
		"pipe closed",
		slog.Any("w_err", w.Close()),
		slog.Any("r_err", r.Close()),
	)
	logger.Debug(
		"stdin closed",
		slog.Any("err", os.Stdin.Close()),
	)

	wg.Wait()

	if err != nil {
		logger.Debug("done with error")
		return fmt.Errorf("%s failed: %w", opts.PinentryPath, err)
	}

	return nil
}
