package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ngicks/run-in-tmux-popup/cmd/run-in-popup/commands"
)

// Cobra is told to keep quiet about errors, so whatever a leaf hands back has to
// travel out of Execute intact for the entry point to print it and exit 1. This
// drives the whole path instead of a stand-in error, and would break if either
// side changed the shape it agrees on.
func TestExecute_aFailureReachesTheEntryPoint(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "a leaf's own failure",
			args: []string{"exec"},
			want: "no command to run",
		},
		{
			name: "one cobra itself reports",
			args: []string{"no-such-command"},
			want: `unknown command "no-such-command"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withArgs(t, tc.args...)

			err := commands.Execute(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Execute = %v, want an error naming %q", err, tc.want)
			}
		})
	}
}

func withArgs(t *testing.T, args ...string) {
	t.Helper()
	saved := os.Args
	t.Cleanup(func() { os.Args = saved })
	os.Args = append([]string{"run-in-popup"}, args...)
}
