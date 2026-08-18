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

const (
	NameTmuxPopup        = runinpopup.BackendTmuxPopup
	NameTmuxFloatingPane = runinpopup.BackendTmuxFloatingPane
	NameZellij           = runinpopup.BackendZellij
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
			name, strings.Join(runinpopup.BackendNames(), ", "),
		)
	}
}

// Names lists every name accepted by New.
func Names() []string { return runinpopup.BackendNames() }

// DetectName selects a backend from the caller's ambient multiplexer hints.
func DetectName(userDataKind, tmuxEnv, zellijEnv string) (string, error) {
	return runinpopup.DetectBackendName(userDataKind, tmuxEnv, zellijEnv)
}
