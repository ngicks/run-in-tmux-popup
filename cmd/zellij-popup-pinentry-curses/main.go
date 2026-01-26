package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/ngicks/run-in-tmux-popup/cmd/internal/preprocess"
	"github.com/ngicks/run-in-tmux-popup/internal/popup"
)

func main() {
	var err error
	ctx,
		cancel,
		logger,
		tempdir,
		shellName,
		p,
		deferFunc := preprocess.Do("tmux")
	defer deferFunc()

	defer cancel()

	err = popup.CallPinentry(
		ctx,
		logger,
		tempdir,
		func(ttyFifo, doneFifo string) (cmd string, args []string) {
			return p.Path, []string{
				"--session=" + p.SessionId,
				"run",
				"--name=pinentry-curses",
				"--floating",
				"--close-on-exit",
				"--pinned=true",
				"--",
				shellName,
				"-c",
				fmt.Sprintf("echo $(tty) >> %s && read done < %s", ttyFifo, doneFifo),
			}
		},
		func(t string) (string, error) {
			return strings.TrimSpace(t), nil
		},
		"/usr/bin/pinentry-curses",
		os.Args[1:],
	)
	if err != nil {
		panic(err)
	}
}
