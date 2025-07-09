package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT)
	defer stop()

	tempdir, err := os.MkdirTemp("", "")
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = os.RemoveAll(tempdir)
	}()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if os.Getenv("TMUX_POPUP_DEBUG") == "1" {
		logFile, err := os.OpenFile(
			filepath.Join(tempdir, "log.txt"),
			os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o700,
		)
		if err != nil {
			panic(err)
		}
		defer logFile.Close()

		logger = slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	ttyFifo := filepath.Join(tempdir, "tty")
	doneFifo := filepath.Join(tempdir, "done")

	for _, s := range []string{ttyFifo, doneFifo} {
		err = syscall.Mknod(s, syscall.S_IFIFO|0o600, 0)
		if err != nil {
			panic(err)
		}
	}

	logger.Debug("tty fifo created")

	// Just little counter measurement for fifo hijack.
	// Adding random generated prefix and suffix to info
	// to detect suspicious sender
	var prefBytes, sufBytes [16]byte
	_, err = io.ReadFull(rand.Reader, prefBytes[:])
	if err != nil {
		panic(err)
	}
	_, err = io.ReadFull(rand.Reader, sufBytes[:])
	if err != nil {
		panic(err)
	}

	pref := hex.EncodeToString(prefBytes[:])
	suf := hex.EncodeToString(sufBytes[:])

	// Launch tmux popup in background
	popupCmd := exec.CommandContext(
		ctx,
		"tmux", "popup",
		"-e", "TTY_FIFO_FILE="+ttyFifo,
		"-e", "DONE_FIFO_FILE="+doneFifo,
		"-e", "SEC_PREFIX="+pref,
		"-e", "SEC_SUFFIX="+suf,
		"-E", "echo ${SEC_PREFIX}$(tty)${SEC_SUFFIX} >> ${TTY_FIFO_FILE} && read done < ${DONE_FIFO_FILE}",
	)
	popupCmd.Cancel = func() error {
		return popupCmd.Process.Signal(syscall.SIGTERM)
	}

	err = popupCmd.Start()
	if err != nil {
		panic(err)
	}
	defer func() {
		done, err := os.OpenFile(doneFifo, os.O_RDWR, 0)
		if err != nil {
			panic(err)
		}
		defer done.Close()
		done.Write([]byte("done\n"))
	}()

	f, err := os.Open(ttyFifo)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)

	scanner.Scan()
	t := scanner.Text()
	t, ok := strings.CutPrefix(t, pref)
	if !ok {
		panic(fmt.Errorf("suspicious sender: incorrect prefix"))
	}
	targetTty, ok := strings.CutSuffix(t, suf)
	if !ok {
		panic(fmt.Errorf("suspicious sender: incorrect suffix"))
	}

	logger.Debug("tmux popup started")

	if targetTty == "" {
		panic("empty tty")
	}

	logger.Debug("got TTY from popup")

	// Run pinentry-curses with stdin interception
	cmd := exec.CommandContext(ctx, "/usr/bin/pinentry-curses", os.Args[1:]...)
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}

	p, err := cmd.StdinPipe()
	if err != nil {
		panic(err)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	if err != nil {
		panic(err)
	}

	logger.Debug("pinentry-curses started")

	// Intercept stdin and replace ttyname
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer p.Close()

		scanner := bufio.NewScanner(os.Stdin)
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
		}

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

	os.Stdin.Close()

	wg.Wait()

	if err != nil {
		panic(err)
	}
}

