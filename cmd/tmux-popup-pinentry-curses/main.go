// Command tmux-popup-pinentry-curses proxies a pinentry prompt into a tmux
// display-popup.
//
// Deprecated: use "run-in-popup pinentry --backend tmux-popup". This binary
// stays as a thin shim over runinpopup so existing gpg-agent wrapper
// scripts keep working.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ngicks/run-in-tmux-popup/internal/legacyshim"
	"github.com/ngicks/run-in-tmux-popup/runinpopup"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/backend"
)

func main() {
	shim := legacyshim.Shim{
		Name:            "tmux-popup-pinentry-curses",
		Replacement:     "run-in-popup pinentry --backend tmux-popup",
		UserDataFormat:  "TMUX_POPUP:tmux_path:session_id:client_id",
		WorkspacePrefix: "tmux-popup-pinentry-curses-",
		NewBackend: func(userData runinpopup.PinentryUserData) (runinpopup.Backend, error) {
			return backend.NewTmuxPopup(backend.Options{
				BinaryPath:  userData.Path,
				ClientId:    userData.ClientId,
				SessionMeta: userData.SessionMeta,
				TMUX:        os.Getenv("TMUX"),
			})
		},
	}
	if err := shim.Run(context.Background(), os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
