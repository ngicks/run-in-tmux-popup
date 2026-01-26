package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
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
		_,
		p,
		deferFunc := preprocess.Do("tmux")
	defer deferFunc()

	if !strings.Contains(p.SessionMeta, ",") {
		panic(fmt.Errorf("session meta is malformed: it must be something like \"/run/user/1000/tmux-1000/default,111,0\""))
	}

	os.Setenv("TMUX", p.SessionMeta)

	defer cancel()

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

	err = popup.CallPinentry(
		ctx,
		logger,
		tempdir,
		func(ttyFifo, doneFifo string) (cmd string, args []string) {
			return p.Path, []string{
				"popup",
				"-c", p.ClientId,
				"-e", "TTY_FIFO_FILE=" + ttyFifo,
				"-e", "DONE_FIFO_FILE=" + doneFifo,
				"-e", "SEC_PREFIX=" + pref,
				"-e", "SEC_SUFFIX=" + suf,
				"-E", "echo ${SEC_PREFIX}$(tty)${SEC_SUFFIX} >> ${TTY_FIFO_FILE} && read done < ${DONE_FIFO_FILE}",
			}
		},
		func(t string) (string, error) {
			t, ok := strings.CutPrefix(t, pref)
			if !ok {
				return "", fmt.Errorf("suspicious sender: incorrect prefix")
			}
			targetTty, ok := strings.CutSuffix(t, suf)
			if !ok {
				return "", fmt.Errorf("suspicious sender: incorrect suffix")
			}
			return targetTty, nil
		},
		"/usr/bin/pinentry-curses",
		os.Args[1:],
	)
	if err != nil {
		panic(err)
	}
}
