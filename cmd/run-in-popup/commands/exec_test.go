package commands

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

// parseExecFlags mirrors what execCmd builds — the two flags bound to locals —
// and parses argv into them, so Changed and ArgsLenAtDash reflect a real
// invocation.
func parseExecFlags(t *testing.T, argv []string) (*cobra.Command, string, string) {
	t.Helper()
	var (
		flagBackend string
		flagTitle   string
	)
	cmd := &cobra.Command{Use: "exec"}
	cmd.Flags().StringVar(&flagBackend, "backend", "", "")
	cmd.Flags().StringVar(&flagTitle, "title", "", "")
	if err := cmd.ParseFlags(argv); err != nil {
		t.Fatalf("ParseFlags(%q): %v", argv, err)
	}
	return cmd, flagBackend, flagTitle
}

func TestExecCommandArgs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		argv    []string
		want    []string
		wantErr bool
	}{
		{
			name: "everything after the separator",
			argv: []string{"--", "make", "test"},
			want: []string{"make", "test"},
		},
		{
			name: "flags of our own stay ours",
			argv: []string{"--title", "build", "--", "go", "build", "./..."},
			want: []string{"go", "build", "./..."},
		},
		{
			name: "the command keeps its own flags",
			argv: []string{"--", "ls", "-l", "--color=always"},
			want: []string{"ls", "-l", "--color=always"},
		},
		{
			// pflag stops at the first "--", so a command using "--" itself
			// arrives whole.
			name: "a second separator belongs to the command",
			argv: []string{"--", "git", "log", "--", "some/path"},
			want: []string{"git", "log", "--", "some/path"},
		},
		{
			name: "the separator may be left out for a flagless command",
			argv: []string{"make", "test"},
			want: []string{"make", "test"},
		},
		{
			name:    "nothing to run",
			argv:    nil,
			wantErr: true,
		},
		{
			name:    "a separator with nothing behind it",
			argv:    []string{"--title", "build", "--"},
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _, _ := parseExecFlags(t, tc.argv)

			got, err := execCommandArgs(cmd, cmd.Flags().Args())
			if tc.wantErr {
				if err == nil {
					t.Fatalf("command = %q, want an error naming the missing command", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("execCommandArgs(%q): %v", tc.argv, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("command = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExecFlagOverrides(t *testing.T) {
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
			name: "backend overlays",
			argv: []string{"--backend=zellij"},
			want: runinpopup.PartialConfig{DefaultBackend: ptr("zellij")},
		},
		{
			name: "an explicitly empty flag is a value, not an absence",
			argv: []string{"--backend="},
			want: runinpopup.PartialConfig{DefaultBackend: ptr("")},
		},
		{
			// The popup title belongs to one run, so it never reaches the config.
			name: "title feeds nothing",
			argv: []string{"--title", "build", "--", "make"},
			want: runinpopup.PartialConfig{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, backend, _ := parseExecFlags(t, tc.argv)

			got := execFlagOverrides(cmd, backend)
			assertStringPtr(t, "DefaultBackend", got.DefaultBackend, tc.want.DefaultBackend)
			if got.PinentryPath != nil {
				t.Errorf("PinentryPath = %q, want it absent: no exec flag feeds it",
					*got.PinentryPath)
			}
			if got.Timeouts != (runinpopup.PartialTimeoutsConfig{}) {
				t.Errorf("Timeouts = %+v, want the zero partial: no flag feeds it", got.Timeouts)
			}
		})
	}
}

// The exec leaves must be reachable and the payload one must stay out of help
// output: exec builds its popup argv around the payload's name.
func TestExecCommandsAreWired(t *testing.T) {
	root := rootCmd()

	for _, tc := range []struct {
		name       string
		wantHidden bool
	}{
		{name: "exec"},
		{name: runinpopup.ExecPayloadCommandName, wantHidden: true},
	} {
		cmd, _, err := root.Find([]string{tc.name})
		if err != nil || cmd.Name() != tc.name {
			t.Fatalf("Find(%q) = %v, %v; want the leaf itself", tc.name, cmd.Name(), err)
		}
		if cmd.Hidden != tc.wantHidden {
			t.Errorf("%s Hidden = %v, want %v", tc.name, cmd.Hidden, tc.wantHidden)
		}
	}
}
