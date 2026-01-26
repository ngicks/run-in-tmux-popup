package popup

import (
	"bufio"
	"bytes"
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

func CallPinentry(
	ctx context.Context,
	logger *slog.Logger,
	tempdir string,
	buildPopUpCmd func(ttyFifo, doneFifo string) (cmd string, args []string),
	validateTtyStr func(string) (string, error),
	pinentryPath string,
	pinentryArgs []string,
) (err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ttyFifo := filepath.Join(tempdir, "tty")
	doneFifo := filepath.Join(tempdir, "done")

	for _, s := range []string{ttyFifo, doneFifo} {
		err = syscall.Mknod(s, syscall.S_IFIFO|0o600, 0)
		if err != nil {
			panic(err)
		}
	}

	logger.Debug("tty fifo created")

	popupCmdPath, popupArgs := buildPopUpCmd(ttyFifo, doneFifo)

	logger.Debug("popup cmd", slog.String("path", popupCmdPath), slog.Any("args", popupArgs))

	// Launch tmux popup in background
	popupCmd := exec.CommandContext(
		ctx,
		popupCmdPath, popupArgs...,
	)
	popupCmd.Cancel = func() error {
		return popupCmd.Process.Signal(syscall.SIGINT)
	}
	popupCmdStdout := new(bytes.Buffer)
	popupCmd.Stdout = popupCmdStdout
	popupCmdStderr := new(bytes.Buffer)
	popupCmd.Stderr = popupCmdStderr

	err = popupCmd.Start()
	if err != nil {
		return fmt.Errorf("popup failed: %w", err)
	}

	go func() {
		<-ctx.Done()
		popupCmd.Process.Kill()
		logger.Debug(
			"popup cmd out:",
			slog.String("stdout", popupCmdStdout.String()),
			slog.String("stderr", popupCmdStderr.String()),
		)
	}()

	defer func() {
		logger.Debug("waiting to done fifo")
		done, err := os.OpenFile(doneFifo, os.O_RDWR, 0)
		if err != nil {
			panic(err)
		}
		defer done.Close()
		done.SetWriteDeadline(time.Now().Add(time.Second))
		done.Write([]byte("done\n"))
	}()

	logger.Debug("opening tty fifo")
	f, err := os.OpenFile(ttyFifo, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open tty: %w", err)
	}
	defer f.Close()

	f.SetReadDeadline(time.Now().Add(20 * time.Second))

	scanner := bufio.NewScanner(f)

	logger.Debug("waiting tty notification")
	scanner.Scan()
	t := scanner.Text()
	if scanner.Err() != nil {
		return fmt.Errorf("scan failed: %w", scanner.Err())
	}

	targetTty, err := validateTtyStr(t)
	if err != nil {
		return err
	}

	logger.Debug("tmux popup started")

	if targetTty == "" {
		return fmt.Errorf("popup return an empty tty")
	}

	logger.Debug("got TTY from popup")

	// Run pinentry-curses with stdin interception
	cmd := exec.CommandContext(ctx, pinentryPath, pinentryArgs...)
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
		return fmt.Errorf("%s failed to start: %w", pinentryPath, err)
	}

	go func() {
		<-ctx.Done()
		cmd.Process.Kill()
	}()

	logger.Debug("pinentry-curses started")

	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}

	var wg sync.WaitGroup

	go func() {
		// don't wait on this gorountine.
		// I'm not sure but closing os.Stdin doesn't have
		// effect to unblock reading on it.
		io.Copy(w, os.Stdin)
		logger.Debug("piping done")
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()

			// Replace ttyname option with the popup's TTY
			if strings.HasPrefix(line, "OPTION ttyname=") {
				original := line
				line = "OPTION ttyname=" + targetTty
				logger.Debug("replaced ttyname", slog.String("old", original), slog.String("new", line))
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
	}()

	err = cmd.Wait()
	if err != nil {
		var execErr *exec.ExitError
		if errors.As(err, &execErr) {
			err = fmt.Errorf("%v: stderr = %s", execErr, string(execErr.Stderr))
		}
	}

	logger.Debug("pinentry-curses finished", slog.Any("err", err))

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
		return fmt.Errorf("pinentry-curses failed: %w", err)
	}

	return nil
}
