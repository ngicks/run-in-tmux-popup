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

type PinentryUserData struct {
	Kind        string
	Path        string
	SessionId   string
	ClientId    string
	SessionMeta string
	Rest        []string
}

func ParsePinentryUserData(pinentryUserData string) PinentryUserData {
	if pinentryUserData == "" {
		pinentryUserData = strings.TrimSpace(os.Getenv("PINENTRY_USER_DATA"))
	}
	var p PinentryUserData
	s := strings.Split(pinentryUserData, ":")
	if len(s) > 0 {
		p.Kind = s[0]
	}
	if len(s) > 1 {
		p.Path = s[1]
	}
	if len(s) > 2 {
		p.SessionId = s[2]
	}
	if len(s) > 3 {
		p.ClientId = s[3]
	}
	if len(s) > 4 {
		p.SessionMeta = s[4]
	}
	if len(s) > 5 {
		p.Rest = s[5:]
	}
	return p
}

func Do(name string) (
	ctx context.Context,
	cancel func(),
	logger *slog.Logger,
	tempdir string,
	shellName string,
	pinentryUserData PinentryUserData,
	deferFunc func(),
) {
	var err error
	var stack []func()

	pinentryUserData = ParsePinentryUserData("")
	if len(pinentryUserData.Path) == 0 || len(pinentryUserData.SessionId) == 0 {
		panic(
			fmt.Errorf(
				"enviroment variable \"PINENTRY_USER_DATA\" must be"+
					" formated as \"%s_POPUP:%s_path:session_id:client_id\" but is %q",
				strings.ToUpper(name),
				name,
				os.Getenv("PINENTRY_USER_DATA"),
			),
		)
	}

	debugMode := strings.HasSuffix(pinentryUserData.Kind, "_DEBUG") || os.Getenv("TMUX_POPUP_DEBUG") == "1"

	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)
	stack = append(stack, cancel)

	tempdir, err = os.MkdirTemp("", name+"-popup-pinentry-curses-*")
	if err != nil {
		panic(err)
	}
	if !debugMode {
		stack = append(
			stack,
			func() {
				_ = os.RemoveAll(tempdir)
			},
		)
	}

	logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	if debugMode {
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

	logger.Info("PINENTRY_USER_DATA", slog.Any("data", pinentryUserData))

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

	deferFunc = func() {
		for _, f := range slices.Backward(stack) {
			f()
		}
	}
	return
}
