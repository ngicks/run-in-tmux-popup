package zellij

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func testClient() *Client {
	return New(Options{Path: "/usr/bin/zellij", Shell: "/bin/bash"})
}

func assertCommand(
	t *testing.T,
	gotPath string,
	gotArgs []string,
	wantPath string,
	wantArgs []string,
) {
	t.Helper()
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
	if !slices.Equal(gotArgs, wantArgs) {
		t.Errorf("args =\n\t%#v\nwant\n\t%#v", gotArgs, wantArgs)
	}
}

func TestClient_RunCommand_argvRunsDirectly(t *testing.T) {
	c := testClient()

	path, args := c.RunCommand(RunRequest{
		SessionId: "session-id",
		Command:   []string{"vim", "my file.txt"},
	})
	assertCommand(t, path, args, "/usr/bin/zellij", []string{
		"--session=session-id",
		"run",
		"--floating",
		"--close-on-exit",
		"--pinned=true",
		"--",
		"vim",
		"my file.txt",
	})
}

// Cells and percentages are zellij's own vocabulary, so they reach it as typed;
// the flags take them in the "--flag=value" form the rest of this argv uses.
func TestClient_RunCommand_geometry(t *testing.T) {
	c := testClient()

	path, args := c.RunCommand(RunRequest{
		SessionId: "session-id",
		Title:     "editor",
		X:         "10",
		Y:         "5%",
		Width:     "80%",
		Height:    "20",
		Command:   []string{"true"},
	})
	assertCommand(t, path, args, "/usr/bin/zellij", []string{
		"--session=session-id",
		"run",
		"--name=editor",
		"--x=10",
		"--y=5%",
		"--width=80%",
		"--height=20",
		"--floating",
		"--close-on-exit",
		"--pinned=true",
		"--",
		"true",
	})
}

// A field left empty is not a value: it emits no flag, and the pane lands
// wherever zellij would have put it.
func TestClient_RunCommand_partialGeometry(t *testing.T) {
	path, args := New(Options{}).RunCommand(RunRequest{
		Width:   "80%",
		Command: []string{"true"},
	})
	assertCommand(t, path, args, "zellij", []string{
		"run",
		"--width=80%",
		"--floating",
		"--close-on-exit",
		"--pinned=true",
		"--",
		"true",
	})
}

// The environment reaches the pane by being sourced: the argv names the file
// and nothing of what is in it.
func TestClient_RunCommand_envFileIsSourced(t *testing.T) {
	c := testClient()

	path, args := c.RunCommand(RunRequest{
		SessionId: "session-id",
		EnvFile:   "/tmp/popup/env",
		Command:   []string{"env"},
	})
	assertCommand(t, path, args, "/usr/bin/zellij", []string{
		"--session=session-id",
		"run",
		"--floating",
		"--close-on-exit",
		"--pinned=true",
		"--",
		"/bin/bash",
		"-c",
		". '/tmp/popup/env' && { 'env'\n}",
	})
}

// The group is what makes the gate cover a whole script: without it the "&&"
// would bind to the script's first command only, and the rest would run in the
// environment the sourcing failed to set.
func TestClient_RunCommand_envFileGatesTheWholeScript(t *testing.T) {
	_, args := New(Options{}).RunCommand(RunRequest{
		EnvFile: "/tmp/popup/env",
		Script:  "echo one; echo two",
	})
	if got, want := args[len(args)-1], ". '/tmp/popup/env' && { echo one; echo two\n}"; got != want {
		t.Errorf("payload = %q, want %q", got, want)
	}
}

func TestClient_RunCommand_scriptGoesThroughShell(t *testing.T) {
	path, args := New(Options{}).RunCommand(RunRequest{
		Title:  "pinentry-curses",
		Script: "echo $(tty) >> /tmp/popup/tty",
	})
	assertCommand(t, path, args, "zellij", []string{
		"run",
		"--name=pinentry-curses",
		"--floating",
		"--close-on-exit",
		"--pinned=true",
		"--",
		"sh",
		"-c",
		"echo $(tty) >> /tmp/popup/tty",
	})
}

func TestWriteEnvFile(t *testing.T) {
	dir := t.TempDir()

	path, err := WriteEnvFile(dir, map[string]string{
		"B":       "two",
		"A":       "it's one",
		"HOSTILE": "$(id) `id` \"; rm -rf /",
	})
	if err != nil {
		t.Fatalf("WriteEnvFile: %v", err)
	}
	if want := filepath.Join(dir, "env"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the env file: %v", err)
	}
	// Sorted, one export per line, every value a single quoted word: what the
	// popup's shell reads in must be the values and nothing the shell would act
	// on itself.
	want := "export A='it'\\''s one'\n" +
		"export B='two'\n" +
		"export HOSTILE='$(id) `id` \"; rm -rf /'\n"
	if string(got) != want {
		t.Errorf("env file =\n\t%q\nwant\n\t%q", got, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600: the file is what keeps the values private", perm)
	}
}
