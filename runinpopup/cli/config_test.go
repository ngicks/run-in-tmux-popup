package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

func TestRenderConfig(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cfg    runinpopup.Config
		format string
		want   string
	}{
		{
			name: "no format writes indented JSON carrying every key",
			cfg: runinpopup.Config{
				PinentryPath:   "/usr/bin/pinentry-curses",
				DefaultBackend: "tmux-popup",
				Timeouts: runinpopup.TimeoutsConfig{
					Overall:   2 * time.Minute,
					TTYRead:   20 * time.Second,
					DoneWrite: time.Second,
				},
			},
			want: `{
  "pinentry_path": "/usr/bin/pinentry-curses",
  "default_backend": "tmux-popup",
  "timeouts": {
    "overall": 120000000000,
    "tty_read": 20000000000,
    "done_write": 1000000000
  }
}
`,
		},
		{
			name: "a zero config spells its zeros out rather than dropping keys",
			want: `{
  "pinentry_path": "",
  "default_backend": "",
  "timeouts": {
    "overall": 0,
    "tty_read": 0,
    "done_write": 0
  }
}
`,
		},
		{
			name:   "a format template addresses fields by their Go names",
			cfg:    runinpopup.Config{PinentryPath: "/opt/pinentry"},
			format: "{{.PinentryPath}}",
			want:   "/opt/pinentry\n",
		},
		{
			name: "a format template sees the shared json helper",
			cfg: runinpopup.Config{
				Timeouts: runinpopup.TimeoutsConfig{Overall: time.Minute},
			},
			format: "{{json .Timeouts}}",
			want: `{
  "overall": 60000000000,
  "tty_read": 0,
  "done_write": 0
}
`,
		},
		{
			name:   "a template rendering nothing still ends in a newline",
			format: "{{.DefaultBackend}}",
			want:   "\n",
		},
		{
			name:   "literal text passes through",
			cfg:    runinpopup.Config{DefaultBackend: "zellij"},
			format: "backend={{.DefaultBackend}}",
			want:   "backend=zellij\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			if err := RenderConfig(&buf, tc.cfg, tc.format); err != nil {
				t.Fatalf("RenderConfig: %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("RenderConfig =\n\t%q\nwant\n\t%q", got, tc.want)
			}
		})
	}
}

func TestRenderConfig_formatFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format string
	}{
		{
			name:   "a malformed template fails to parse",
			format: "{{.PinentryPath",
		},
		{
			name:   "an unknown field fails at execution",
			format: "{{.Nonexistent}}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			err := RenderConfig(&buf, runinpopup.Config{}, tc.format)
			if err == nil || !strings.Contains(err.Error(), "--format") {
				t.Fatalf("err = %v, want one attributed to --format", err)
			}
		})
	}
}
