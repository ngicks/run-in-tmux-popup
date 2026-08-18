package commands

import (
	"slices"
	"testing"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/backend"
)

// The messages a user actually sees when resolution fails, spelled out here so
// a reworded backend error cannot change the CLI's output unnoticed.
const (
	errUnknownBackend = `unknown popup backend "tmux":` +
		` valid values are tmux-popup, tmux-floating-pane, zellij`
	errNothingDetected = `cannot detect the popup backend:` +
		` neither PINENTRY_USER_DATA, $TMUX nor $ZELLIJ names one;` +
		` select it explicitly, valid values are tmux-popup, tmux-floating-pane, zellij`
	errMalformedSessionMeta = `tmux session meta is malformed:` +
		` it must be something like "/run/user/1000/tmux-1000/default,111,0" but is ""`
)

func TestResolveRuntime(t *testing.T) {
	const (
		tmuxEnv    = "TMUX=/run/user/1000/tmux-1000/default,111,0"
		zellijEnv  = "ZELLIJ=0"
		tmuxData   = "PINENTRY_USER_DATA=TMUX_POPUP:/usr/bin/tmux:$1:%1:/tmp/tmux-1000/default,111,0"
		zellijData = "PINENTRY_USER_DATA=ZELLIJ_POPUP:/usr/bin/zellij:session-id"

		floatingPaneDebugData = "PINENTRY_USER_DATA=TMUX_FLOATING_PANE_DEBUG:" +
			"/usr/bin/tmux:$1:%1:/tmp/tmux-1000/default,111,0"
	)

	for _, tc := range []struct {
		name        string
		config      runinpopup.Config
		overrides   runinpopup.PartialConfig
		environ     []string
		wantBackend string
		wantDebug   bool
		wantErr     string
	}{
		{
			name:        "the flag wins over the configured default",
			config:      runinpopup.Config{DefaultBackend: backend.NameZellij},
			overrides:   runinpopup.PartialConfig{DefaultBackend: new(backend.NameTmuxFloatingPane)},
			environ:     []string{tmuxEnv, zellijEnv, zellijData},
			wantBackend: backend.NameTmuxFloatingPane,
		},
		{
			name:        "the configured default wins over every ambient hint",
			config:      runinpopup.Config{DefaultBackend: backend.NameZellij},
			environ:     []string{tmuxEnv, tmuxData},
			wantBackend: backend.NameZellij,
		},
		{
			name:        "an explicitly empty flag clears the default and detection resumes",
			config:      runinpopup.Config{DefaultBackend: backend.NameZellij},
			overrides:   runinpopup.PartialConfig{DefaultBackend: new("")},
			environ:     []string{tmuxEnv},
			wantBackend: backend.NameTmuxPopup,
		},
		{
			name:        "PINENTRY_USER_DATA wins over $TMUX",
			environ:     []string{tmuxEnv, zellijData},
			wantBackend: backend.NameZellij,
		},
		{
			name:        "a debug kind still names the backend",
			environ:     []string{floatingPaneDebugData},
			wantBackend: backend.NameTmuxFloatingPane,
			wantDebug:   true,
		},
		{
			name:        "$TMUX alone selects display-popup",
			environ:     []string{tmuxEnv},
			wantBackend: backend.NameTmuxPopup,
		},
		{
			name:        "$TMUX wins over $ZELLIJ",
			environ:     []string{zellijEnv, tmuxEnv},
			wantBackend: backend.NameTmuxPopup,
		},
		{
			name:        "$ZELLIJ is the last hint left",
			environ:     []string{zellijEnv},
			wantBackend: backend.NameZellij,
		},
		{
			name:    "a name no backend answers to",
			config:  runinpopup.Config{DefaultBackend: "tmux"},
			environ: []string{tmuxEnv},
			wantErr: errUnknownBackend,
		},
		{
			name:    "nothing names a backend",
			environ: []string{"SHELL=/bin/zsh"},
			wantErr: errNothingDetected,
		},
		{
			// The session meta reaches the backend from PINENTRY_USER_DATA, and
			// is the only stand-in for $TMUX when the caller has none.
			name:    "a tmux backend outside tmux needs the session meta",
			environ: []string{"PINENTRY_USER_DATA=TMUX_POPUP:/usr/bin/tmux"},
			wantErr: errMalformedSessionMeta,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, err := resolveRuntime(
				runtimeInputs{Config: tc.config, Overrides: tc.overrides},
				tc.environ,
			)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("backend = %q, want the error %q", rt.Backend.Name(), tc.wantErr)
				}
				if err.Error() != tc.wantErr {
					t.Fatalf("err = %q, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRuntime: %v", err)
			}
			if got := rt.Backend.Name(); got != tc.wantBackend {
				t.Errorf("backend = %q, want %q", got, tc.wantBackend)
			}
			if got := rt.UserData.Debug(); got != tc.wantDebug {
				t.Errorf("UserData.Debug() = %v, want %v", got, tc.wantDebug)
			}
		})
	}
}

// The resolved runtime carries the layers the commands keep reading from: the
// merged configuration and the parsed user data.
func TestResolveRuntime_carriesMergedConfigAndUserData(t *testing.T) {
	rt, err := resolveRuntime(
		runtimeInputs{
			Config: runinpopup.Config{PinentryPath: "/from/config"},
			Overrides: runinpopup.PartialConfig{
				PinentryPath:   new("/from/flag"),
				DefaultBackend: new(backend.NameZellij),
			},
		},
		[]string{"PINENTRY_USER_DATA=ZELLIJ_POPUP:/usr/bin/zellij:session-id:%1:meta:tail"},
	)
	if err != nil {
		t.Fatalf("resolveRuntime: %v", err)
	}

	if rt.Config.PinentryPath != "/from/flag" {
		t.Errorf("PinentryPath = %q, want the flag layer to win", rt.Config.PinentryPath)
	}
	if rt.Config.DefaultBackend != backend.NameZellij {
		t.Errorf("DefaultBackend = %q, want %q", rt.Config.DefaultBackend, backend.NameZellij)
	}
	want := runinpopup.PinentryUserData{
		Kind:        "ZELLIJ_POPUP",
		Path:        "/usr/bin/zellij",
		SessionId:   "session-id",
		ClientId:    "%1",
		SessionMeta: "meta",
		Rest:        []string{"tail"},
	}
	if rt.UserData.Kind != want.Kind ||
		rt.UserData.Path != want.Path ||
		rt.UserData.SessionId != want.SessionId ||
		rt.UserData.ClientId != want.ClientId ||
		rt.UserData.SessionMeta != want.SessionMeta ||
		!slices.Equal(rt.UserData.Rest, want.Rest) {
		t.Errorf("UserData = %+v, want %+v", rt.UserData, want)
	}
}

func TestLookupEnviron(t *testing.T) {
	environ := []string{
		"malformed-entry-without-a-separator",
		"TMUX=",
		"SHELL=/bin/zsh",
		"PINENTRY_USER_DATA=TMUX_POPUP:/usr/bin/tmux",
		"SHELL=/bin/dash",
	}

	for _, tc := range []struct {
		name string
		key  string
		want string
	}{
		{name: "a plain value", key: "PINENTRY_USER_DATA", want: "TMUX_POPUP:/usr/bin/tmux"},
		{name: "an empty value is not an absence to the caller", key: "TMUX", want: ""},
		{name: "an absent variable reads empty", key: "ZELLIJ", want: ""},
		{name: "a repeated variable resolves to the first entry, as os.Getenv does",
			key: "SHELL", want: "/bin/zsh"},
		{name: "an entry without a separator is skipped", key: "malformed-entry-without-a-separator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := lookupEnviron(environ, tc.key); got != tc.want {
				t.Errorf("lookupEnviron(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}
