package commands

import (
	"cmp"
	"strings"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/backend"
)

// runtimeInputs is what a command knows before the environment is consulted:
// the configuration it loaded (defaults < file < env) and the flags the user
// set explicitly. Cobra stays in the command — Overrides is the partial its
// flag inspection produced, so an absent field means "flag left alone".
type runtimeInputs struct {
	Config    runinpopup.Config
	Overrides runinpopup.PartialConfig
}

// commandRuntime is the assembled result: the merged configuration a command
// still reads fields from, the parsed PINENTRY_USER_DATA it derives its
// debug-retention policy from, and the popup backend to run on.
type commandRuntime struct {
	Config   runinpopup.Config
	UserData runinpopup.PinentryUserData
	Backend  runinpopup.Backend
}

// resolveRuntime overlays the flag layer on the configuration and builds the
// popup backend for it. The backend is the explicitly-flagged one, else the
// configured default_backend, else whatever the environment reveals — an
// explicitly empty flag value clears a configured default and so asks for
// detection again.
//
// environ holds "KEY=VALUE" entries, the form os.Environ returns: every ambient
// value the resolution consumes arrives through it, so the precedence is
// testable without touching the process environment.
func resolveRuntime(inputs runtimeInputs, environ []string) (commandRuntime, error) {
	cfg := inputs.Overrides.Apply(inputs.Config)
	userData := runinpopup.ParsePinentryUserData(lookupEnviron(environ, "PINENTRY_USER_DATA"))
	tmuxEnv := lookupEnviron(environ, "TMUX")

	backendName := cfg.DefaultBackend
	if backendName == "" {
		var err error
		backendName, err = backend.DetectName(
			userData.Kind,
			tmuxEnv,
			lookupEnviron(environ, "ZELLIJ"),
		)
		if err != nil {
			return commandRuntime{}, err
		}
	}

	popupBackend, err := backend.New(backendName, backend.Options{
		BinaryPath:  userData.Path,
		SessionId:   userData.SessionId,
		ClientId:    userData.ClientId,
		SessionMeta: userData.SessionMeta,
		TMUX:        tmuxEnv,
		// $SHELL rather than the library's "sh": the popup payload is the user's
		// login shell in every released version of this tool.
		Shell: cmp.Or(lookupEnviron(environ, "SHELL"), "bash"),
	})
	if err != nil {
		return commandRuntime{}, err
	}

	return commandRuntime{Config: cfg, UserData: userData, Backend: popupBackend}, nil
}

// lookupEnviron reads one variable out of "KEY=VALUE" entries. The first entry
// wins, as in os.Getenv, so a duplicated variable resolves the same way here as
// it would when read from the process environment.
func lookupEnviron(environ []string, key string) string {
	for _, entry := range environ {
		if k, v, ok := strings.Cut(entry, "="); ok && k == key {
			return v
		}
	}
	return ""
}
