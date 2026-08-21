package commands

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ngicks/run-in-tmux-popup/runinpopup/backend"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/cli"
)

// findCommand returns the subcommand of a freshly built root, so the assertions
// below see the help a user does rather than the constants behind it.
func findCommand(t *testing.T, name string) *cobra.Command {
	t.Helper()
	cmd, _, err := rootCmd().Find([]string{name})
	if err != nil {
		t.Fatalf("finding the %s command: %v", name, err)
	}
	if cmd.Name() != name {
		t.Fatalf("Find(%q) returned %q", name, cmd.Name())
	}
	return cmd
}

// A backend nobody names in help is one nobody finds. The names come from
// backend.Names through cli, so this fails when a new backend is added without
// the help following it.
func TestHelp_namesEveryBackend(t *testing.T) {
	for _, tc := range []struct {
		name string
		text func(t *testing.T) string
	}{
		{
			name: "config describes backend",
			text: func(t *testing.T) string { return findCommand(t, "config").Long },
		},
		{
			name: "exec documents its --backend flag",
			text: func(t *testing.T) string { return backendFlagUsage(t, "exec") },
		},
		{
			name: "pinentry documents its --backend flag",
			text: func(t *testing.T) string { return backendFlagUsage(t, "pinentry") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := tc.text(t)
			for _, name := range backend.Names() {
				if !strings.Contains(text, name) {
					t.Errorf("help does not name the backend %q:\n%s", name, text)
				}
			}
		})
	}
}

func backendFlagUsage(t *testing.T, command string) string {
	t.Helper()
	flag := findCommand(t, command).Flags().Lookup("backend")
	if flag == nil {
		t.Fatalf("%s has no --backend flag", command)
	}
	return flag.Usage
}

// The config help is where a --format author reads the schema from, so every
// documented field has to survive the way the command assembles its Long text.
func TestConfigHelp_showsTheSchemaAndTemplateHelpers(t *testing.T) {
	long := findCommand(t, "config").Long
	for _, want := range []string{cli.ConfigSchemaHelp(), cli.TemplateFuncHelp()} {
		if !strings.Contains(long, want) {
			t.Errorf("config help is missing:\n%s\ngot:\n%s", want, long)
		}
	}
}
