package commands

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

// parsePinentryFlags mirrors what pinentryCmd builds — the two flags bound to
// locals — and parses argv into them, so Changed reflects a real invocation.
func parsePinentryFlags(t *testing.T, argv []string) (*cobra.Command, string, string) {
	t.Helper()
	var (
		flagBackend  string
		flagPinentry string
	)
	cmd := &cobra.Command{Use: "pinentry"}
	cmd.Flags().StringVar(&flagBackend, "backend", "", "")
	cmd.Flags().StringVar(&flagPinentry, "pinentry", "", "")
	if err := cmd.ParseFlags(argv); err != nil {
		t.Fatalf("ParseFlags(%q): %v", argv, err)
	}
	return cmd, flagBackend, flagPinentry
}

func TestPinentryFlagOverrides(t *testing.T) {
	ptr := func(s string) *string { return &s }

	for _, tc := range []struct {
		name string
		argv []string
		want runinpopup.PartialConfig
	}{
		{
			name: "flags left alone stay absent",
			argv: nil,
			want: runinpopup.PartialConfig{},
		},
		{
			name: "only the changed flag overlays",
			argv: []string{"--backend=zellij"},
			want: runinpopup.PartialConfig{DefaultBackend: ptr("zellij")},
		},
		{
			name: "pinentry alone",
			argv: []string{"--pinentry=/usr/bin/pinentry-tty"},
			want: runinpopup.PartialConfig{PinentryPath: ptr("/usr/bin/pinentry-tty")},
		},
		{
			name: "both",
			argv: []string{"--backend", "tmux-popup", "--pinentry", "/opt/pinentry"},
			want: runinpopup.PartialConfig{
				DefaultBackend: ptr("tmux-popup"),
				PinentryPath:   ptr("/opt/pinentry"),
			},
		},
		{
			name: "an explicitly empty flag is a value, not an absence",
			argv: []string{"--backend="},
			want: runinpopup.PartialConfig{DefaultBackend: ptr("")},
		},
		{
			name: "trailing pinentry args do not set anything",
			argv: []string{"--", "--display", ":0"},
			want: runinpopup.PartialConfig{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, backend, pinentry := parsePinentryFlags(t, tc.argv)

			got := pinentryFlagOverrides(cmd, backend, pinentry)
			assertStringPtr(t, "DefaultBackend", got.DefaultBackend, tc.want.DefaultBackend)
			assertStringPtr(t, "PinentryPath", got.PinentryPath, tc.want.PinentryPath)
			if got.Timeouts != (runinpopup.PartialTimeoutsConfig{}) {
				t.Errorf("Timeouts = %+v, want the zero partial: no flag feeds it", got.Timeouts)
			}
		})
	}
}

// The overlay is only meaningful through Apply, so the two edge semantics —
// absent leaves the lower layer, explicit empty overwrites it — are asserted on
// the merged result too.
func TestPinentryFlagOverrides_apply(t *testing.T) {
	base := runinpopup.Config{PinentryPath: "/from/config", DefaultBackend: "tmux-popup"}

	cmd, backend, pinentry := parsePinentryFlags(t, nil)
	if got := pinentryFlagOverrides(cmd, backend, pinentry).Apply(base); got != base {
		t.Errorf("Apply = %+v, want the lower layer untouched: %+v", got, base)
	}

	cmd, backend, pinentry = parsePinentryFlags(t, []string{"--backend="})
	got := pinentryFlagOverrides(cmd, backend, pinentry).Apply(base)
	if got.DefaultBackend != "" {
		t.Errorf("DefaultBackend = %q, want the explicit empty flag to win", got.DefaultBackend)
	}
	if got.PinentryPath != base.PinentryPath {
		t.Errorf("PinentryPath = %q, want %q", got.PinentryPath, base.PinentryPath)
	}
}

func assertStringPtr(t *testing.T, name string, got, want *string) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil || want == nil:
		t.Errorf("%s = %v, want %v (nil-ness differs)", name, fmtStringPtr(got), fmtStringPtr(want))
	case *got != *want:
		t.Errorf("%s = %q, want %q", name, *got, *want)
	}
}

func fmtStringPtr(p *string) string {
	if p == nil {
		return "<absent>"
	}
	return "&" + *p
}
