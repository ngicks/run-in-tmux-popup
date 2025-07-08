package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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

	logger := slog.New(slog.DiscardHandler)
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

	logger = logger.With(slog.String("side", "wrapper"))

	var (
		stdin  = filepath.Join(tempdir, "stdin")
		stdout = filepath.Join(tempdir, "stdout")
		stderr = filepath.Join(tempdir, "stderr")
	)

	for _, s := range []string{stdin, stdout, stderr} {
		err := syscall.Mknod(s, syscall.S_IFIFO|0o600, 0)
		if err != nil {
			panic(err)
		}
	}

	logger.Debug("fifo created")

	stdinFile, err := os.OpenFile(stdin, os.O_RDWR, 0)
	if err != nil {
		panic(err)
	}
	defer stdinFile.Close()

	stdoutFile, err := os.OpenFile(stdout, os.O_RDWR, 0)
	if err != nil {
		panic(err)
	}
	defer stdoutFile.Close()

	stderrFile, err := os.OpenFile(stderr, os.O_RDWR, 0)
	if err != nil {
		panic(err)
	}
	defer stderrFile.Close()

	args, _ := json.Marshal(os.Args[1:])
	logger.Debug("args", slog.Any("args", args))

	cmd := exec.CommandContext(
		ctx,
		"tmux", "popup",
		"-e", "TMUX_POPUP_DEBUG="+os.Getenv("TMUX_POPUP_DEBUG"),
		"-e", "TMP_DIR="+tempdir,
		"-e", "FIFO_IN="+stdin,
		"-e", "FIFO_OUT="+stdout,
		"-e", "FIFO_ERR="+stderr,
		"-e", "COMMAND_ARGS="+string(args),
		"-E", "TTY_NAME=$(tty) tmux-popup-pinentry-executor",
	)
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}

	err = cmd.Start()
	if err != nil {
		panic(err)
	}
	logger.Debug("started")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(os.Stdout, stdoutFile)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(os.Stderr, stderrFile)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(stdinFile, os.Stdin)
	}()

	err = cmd.Wait()
	if err != nil {
		var execErr *exec.ExitError
		if errors.As(err, &execErr) {
			err = fmt.Errorf("%v: stderr = %s", execErr, string(execErr.Stderr))
		}
	}
	logger.Debug("done", slog.Any("err", err))

	os.Stdin.Close()
	os.Stdout.Close()
	os.Stderr.Close()
	stdoutFile.Close()
	stderrFile.Close()
	stdinFile.Close()

	wg.Wait()
	if err != nil {
		panic(err)
	}
}
