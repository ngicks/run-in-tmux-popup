package preprocess

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

func Do(name string) (
	ctx context.Context,
	cancel func(),
	logger *slog.Logger,
	tempdir string,
	shellName string,
	path string,
	session string,
	rest string,
	deferFunc func(),
) {
	var err error
	var stack []func()

	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)
	stack = append(stack, cancel)

	tempdir, err = os.MkdirTemp("", name+"-popup-pinentry-curses-*")
	if err != nil {
		panic(err)
	}
	stack = append(
		stack,
		func() {
			_ = os.RemoveAll(tempdir)
		},
	)

	logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	if os.Getenv("TMUX_POPUP_DEBUG") == "1" {
		logFile, err := os.OpenFile(
			filepath.Join(tempdir, "log.txt"),
			os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o700,
		)
		if err != nil {
			panic(err)
		}
		stack = append(
			stack,
			func() {
				_ = logFile.Close()
			},
		)

		logger = slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	sigCh := make(chan os.Signal, 10)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT)
	stack = append(
		stack,
		func() {
			signal.Stop(sigCh)
		},
	)
	go func() {
		select {
		case <-ctx.Done():
			logger.Debug(
				"context canceled",
				slog.Any("err", ctx.Err()),
				slog.Any("cause", context.Cause(ctx)),
			)
		case sig := <-sigCh:
			cancel()
			logger.Debug(
				"signal received",
				slog.String("signal", sig.String()),
			)
		}
	}()

	shellName = cmp.Or(os.Getenv("SHELL"), "bash")

	pinentryUserData := strings.TrimPrefix(
		strings.TrimSpace(
			os.Getenv("PINENTRY_USER_DATA"),
		),
		strings.ToUpper(name)+"_POPUP:",
	)
	path, session, _ = strings.Cut(pinentryUserData, ":")
	session, rest, _ = strings.Cut(session, ":")

	if len(path) == 0 || len(session) == 0 {
		panic(
			fmt.Errorf(
				"enviroment variable \"PINENTRY_USER_DATA\" must be"+
					" formated as \"%s_POPUP:%s_path:session_name\" but is %q",
				strings.ToUpper(name),
				name,
				os.Getenv("PINENTRY_USER_DATA"),
			),
		)
	}

	deferFunc = func() {
		for _, f := range slices.Backward(stack) {
			f()
		}
	}
	return
}
