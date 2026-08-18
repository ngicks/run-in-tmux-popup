// Command zellij-popup-pinentry-curses proxies a pinentry prompt into a zellij
// floating pane.
//
// Deprecated: use "run-in-popup pinentry --backend zellij". This binary stays
// as a thin shim over runinpopup so existing gpg-agent wrapper scripts keep
// working.
package main

import (
	"cmp"
	"context"
	"fmt"
	"os"

	"github.com/ngicks/run-in-tmux-popup/internal/legacyshim"
	"github.com/ngicks/run-in-tmux-popup/runinpopup"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/backend"
)

func main() {
	shim := legacyshim.Shim{
		Name:            "zellij-popup-pinentry-curses",
		Replacement:     "run-in-popup pinentry --backend zellij",
		UserDataFormat:  "ZELLIJ_POPUP:zellij_path:session_id:client_id",
		WorkspacePrefix: "zellij-popup-pinentry-curses-",
		NewBackend: func(userData runinpopup.PinentryUserData) (runinpopup.Backend, error) {
			return backend.NewZellij(backend.Options{
				BinaryPath: userData.Path,
				SessionId:  userData.SessionId,
				// bash where the backend would default to sh: this binary has always
				// fallen back to it, and that is what it stays compatible with.
				Shell: cmp.Or(os.Getenv("SHELL"), "bash"),
			})
		},
	}
	if err := shim.Run(context.Background(), os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
