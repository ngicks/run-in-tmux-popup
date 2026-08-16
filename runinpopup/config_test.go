package runinpopup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// configEnvVars is every variable LoadConfig reads: the hand-read config-path
// override plus one per PartialConfig env tag.
var configEnvVars = []string{
	ENV_RUN_IN_POPUP_CONF,
	"RUN_IN_POPUP_PINENTRY_PATH",
	"RUN_IN_POPUP_DEFAULT_BACKEND",
	"RUN_IN_POPUP_TIMEOUTS_OVERALL",
	"RUN_IN_POPUP_TIMEOUTS_TTY_READ",
	"RUN_IN_POPUP_TIMEOUTS_DONE_WRITE",
}

// isolateConfigEnv unsets every variable of the env layer so a case sees only
// what it sets itself, whatever the developer's shell exports. t.Setenv first,
// for the restore it registers; os.Unsetenv then removes the variable, which
// t.Setenv alone cannot express.
func isolateConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range configEnvVars {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("Unsetenv(%q): %v", k, err)
		}
	}
}

// writeConfig writes body to a file in a fresh temp dir and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	return path
}

func TestLoadConfig_layering(t *testing.T) {
	def := DefaultConfig()

	for _, tc := range []struct {
		name string
		// file is the config file body; empty means no file is written and the
		// path points at a nonexistent file.
		file string
		env  map[string]string
		want Config
	}{
		{
			name: "no file and no env leaves the defaults",
			want: def,
		},
		{
			name: `file wins over the defaults`,
			file: `{"pinentry_path":"/usr/bin/pinentry-tty","default_backend":"zellij"}`,
			want: Config{
				PinentryPath:   "/usr/bin/pinentry-tty",
				DefaultBackend: "zellij",
				Timeouts:       def.Timeouts,
			},
		},
		{
			name: "a nested timeout from the file preserves its siblings",
			file: `{"timeouts":{"overall":60000000000}}`,
			want: Config{
				PinentryPath:   def.PinentryPath,
				DefaultBackend: def.DefaultBackend,
				Timeouts: TimeoutsConfig{
					Overall:   time.Minute,
					TTYRead:   def.Timeouts.TTYRead,
					DoneWrite: def.Timeouts.DoneWrite,
				},
			},
		},
		{
			name: "an explicit zero in the file overwrites the default",
			file: `{"timeouts":{"overall":0}}`,
			want: Config{
				PinentryPath:   def.PinentryPath,
				DefaultBackend: def.DefaultBackend,
				Timeouts: TimeoutsConfig{
					Overall:   0,
					TTYRead:   def.Timeouts.TTYRead,
					DoneWrite: def.Timeouts.DoneWrite,
				},
			},
		},
		{
			name: "env wins over the defaults and parses duration strings",
			env: map[string]string{
				"RUN_IN_POPUP_PINENTRY_PATH":     "/opt/pinentry",
				"RUN_IN_POPUP_TIMEOUTS_TTY_READ": "5s",
			},
			want: Config{
				PinentryPath:   "/opt/pinentry",
				DefaultBackend: def.DefaultBackend,
				Timeouts: TimeoutsConfig{
					Overall:   def.Timeouts.Overall,
					TTYRead:   5 * time.Second,
					DoneWrite: def.Timeouts.DoneWrite,
				},
			},
		},
		{
			name: "env wins over the file, key by key",
			file: `{"pinentry_path":"/from/file","default_backend":"tmux-popup",` +
				`"timeouts":{"overall":60000000000,"tty_read":1000000000}}`,
			env: map[string]string{
				"RUN_IN_POPUP_PINENTRY_PATH":     "/from/env",
				"RUN_IN_POPUP_TIMEOUTS_TTY_READ": "5s",
			},
			want: Config{
				PinentryPath:   "/from/env",
				DefaultBackend: "tmux-popup",
				Timeouts: TimeoutsConfig{
					Overall:   time.Minute,
					TTYRead:   5 * time.Second,
					DoneWrite: def.Timeouts.DoneWrite,
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateConfigEnv(t)
			path := filepath.Join(t.TempDir(), "absent.json")
			if tc.file != "" {
				path = writeConfig(t, tc.file)
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			got, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if got != tc.want {
				t.Errorf("LoadConfig =\n\t%+v\nwant\n\t%+v", got, tc.want)
			}
		})
	}
}

func TestLoadConfig_path(t *testing.T) {
	t.Run("RUN_IN_POPUP_CONF names the file when --config is empty", func(t *testing.T) {
		isolateConfigEnv(t)
		t.Setenv(ENV_RUN_IN_POPUP_CONF, writeConfig(t, `{"pinentry_path":"/from/conf-var"}`))

		got, err := LoadConfig("")
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if got.PinentryPath != "/from/conf-var" {
			t.Errorf(
				"PinentryPath = %q, want the file %s names",
				got.PinentryPath,
				ENV_RUN_IN_POPUP_CONF,
			)
		}
	})

	t.Run("--config wins over RUN_IN_POPUP_CONF", func(t *testing.T) {
		isolateConfigEnv(t)
		t.Setenv(ENV_RUN_IN_POPUP_CONF, writeConfig(t, `{"pinentry_path":"/from/conf-var"}`))
		flagPath := writeConfig(t, `{"pinentry_path":"/from/flag"}`)

		got, err := LoadConfig(flagPath)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if got.PinentryPath != "/from/flag" {
			t.Errorf("PinentryPath = %q, want the file --config names", got.PinentryPath)
		}
	})

	t.Run("a malformed file aborts", func(t *testing.T) {
		isolateConfigEnv(t)
		path := writeConfig(t, `{"pinentry_path":`)

		_, err := LoadConfig(path)
		if err == nil || !strings.Contains(err.Error(), "parse config") {
			t.Fatalf("err = %v, want one reporting a parse failure", err)
		}
	})
}
