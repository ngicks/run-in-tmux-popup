// Package backend implements the terminal-multiplexer backends used by
// runinpopup.
package backend

import (
	"fmt"
	"strings"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

// Options carries the coordinates consumed by the concrete backend
// constructors. Fields unused by a selected backend are ignored.
type Options struct {
	// BinaryPath is the multiplexer binary. Empty uses the backend's default.
	BinaryPath string
	// SessionId identifies the multiplexer session hosting the popup.
	SessionId string
	// ClientId identifies the tmux client on which to display a popup.
	ClientId string
	// SessionMeta is the $TMUX value supplied by PINENTRY_USER_DATA.
	SessionMeta string
	// TMUX is the caller's current $TMUX value.
	TMUX string
	// Shell runs payloads for backends requiring a shell. Empty means "sh".
	Shell string
}

// Backend names. They name the popup *mechanism*, not the multiplexer: tmux has
// two, and they are separate names rather than variants of one another.
const (
	NameTmuxPopup        = "tmux-popup"
	NameTmuxFloatingPane = "tmux-floating-pane"
	NameZellij           = "zellij"
)

// New builds the named backend.
func New(name string, opts Options) (runinpopup.Backend, error) {
	switch name {
	case NameTmuxPopup:
		return NewTmuxPopup(opts)
	case NameTmuxFloatingPane:
		return NewTmuxFloatingPane(opts)
	case NameZellij:
		return NewZellij(opts)
	default:
		return nil, fmt.Errorf(
			"unknown popup backend %q: valid values are %s",
			name, strings.Join(Names(), ", "),
		)
	}
}

// Names lists every name accepted by New, in the order reported to users.
func Names() []string {
	return []string{NameTmuxPopup, NameTmuxFloatingPane, NameZellij}
}

// DetectName picks a backend name from ambient hints, for callers that
// were not told which backend to use. Every hint is passed in — the caller
// reads the environment, the detection itself stays pure:
//
//   - userDataKind is runinpopup.PinentryUserData.Kind, the most specific hint
//     since the gpg-agent wrapper script picked it deliberately. A "_DEBUG"
//     suffix does not change the mechanism, so the kind is matched by prefix.
//   - tmuxEnv is $TMUX and zellijEnv is $ZELLIJ, checked in that order. A bare
//     $TMUX names the multiplexer, not one of its two popup mechanisms, and
//     resolves to NameTmuxPopup: display-popup is the older, unconditionally
//     safe one, so floating panes stay an explicit choice.
//
// It returns an error naming the valid backends when nothing matches.
func DetectName(userDataKind, tmuxEnv, zellijEnv string) (string, error) {
	switch kind := strings.ToUpper(strings.TrimSpace(userDataKind)); {
	case strings.HasPrefix(kind, "TMUX_POPUP"):
		return NameTmuxPopup, nil
	case strings.HasPrefix(kind, "TMUX_FLOATING_PANE"):
		return NameTmuxFloatingPane, nil
	case strings.HasPrefix(kind, "ZELLIJ_POPUP"):
		return NameZellij, nil
	}
	switch {
	case tmuxEnv != "":
		return NameTmuxPopup, nil
	case zellijEnv != "":
		return NameZellij, nil
	}
	return "", fmt.Errorf(
		"cannot detect the popup backend:"+
			" neither PINENTRY_USER_DATA, $TMUX nor $ZELLIJ names one;"+
			" select it explicitly, valid values are %s",
		strings.Join(Names(), ", "),
	)
}
