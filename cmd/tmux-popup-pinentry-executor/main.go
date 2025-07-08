package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

func isInTmux() error {
	tmux := os.Getenv("TMUX")
	if tmux == "" {
		return fmt.Errorf("not in tmux: empty $TMUX")
	}
	tmuxFile, _, _ := strings.Cut(tmux, ",")
	_, err := os.Stat(tmuxFile)
	if err != nil {
		return fmt.Errorf("not in tmux: %w", err)
	}
	return nil
}

func main() {
	if err := isInTmux(); err != nil {
		panic(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT)
	defer stop()

	ttyName := os.Getenv("TTY_NAME")
	tempdir := os.Getenv("TMP_DIR")
	stdin := os.Getenv("FIFO_IN")
	stdout := os.Getenv("FIFO_OUT")
	stderr := os.Getenv("FIFO_ERR")
	commandArgs := os.Getenv("COMMAND_ARGS")

	logger := slog.New(slog.DiscardHandler)
	if os.Getenv("TMUX_POPUP_DEBUG") == "1" {
		if tempdir == "" {
			var err error
			tempdir, err = os.MkdirTemp("", "")
			if err != nil {
				panic(err)
			}
			defer func() {
				_ = os.RemoveAll(tempdir)
			}()
		}

		logFile, err := os.OpenFile(
			filepath.Join(tempdir, "log.txt"),
			os.O_APPEND|os.O_CREATE|os.O_RDWR, fs.ModePerm,
		)
		if err != nil {
			panic(err)
		}
		defer logFile.Close()

		logger = slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	logger = logger.With(slog.String("side", "executor"))

	logger.Debug("started")

	logger.Debug("tty=" + ttyName)

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

	logger.Debug("fifo opened")

	var args []string
	err = json.Unmarshal([]byte(commandArgs), &args)
	if err != nil {
		panic(err)
	}

	logger.Debug("command", slog.Any("args", args))

	cmd := exec.CommandContext(ctx, args[0])
	cmd.Args = args
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}

	p, err := cmd.StdinPipe()
	if err != nil {
		panic(err)
	}

	var wg sync.WaitGroup

	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile

	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdinFile)
		for scanner.Scan() {
			s := scanner.Text()
			switch {
			case strings.HasPrefix(s, "OPTION ttyname="):
				org := s
				s = "OPTION ttyname=" + ttyName
				logger.Debug("swapped", slog.String("original", org), slog.String("swapped", s))
			}
			logger.Debug("input: " + s)
			_, err := p.Write([]byte(s + "\n"))
			if err != nil {
				logger.Warn("write error", slog.Any("err", err))
				break
			}
		}
		if scanner.Err() != nil {
			logger.Warn("scan err", slog.Any("err", scanner.Err()))
		}
		stdinFile.Close()
	}()

	err = cmd.Run()
	if err != nil {
		var execErr *exec.ExitError
		if errors.As(err, &execErr) {
			err = fmt.Errorf("%v: stderr = %s", execErr, string(execErr.Stderr))
		}
	}
	logger.Debug("done", slog.Any("err", err))
	stdinFile.Close()
	stdoutFile.Close()
	stderrFile.Close()
	wg.Wait()
	if err != nil {
		panic(err)
	}
}
