package zellij

import (
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

func TestClient_RunCommand_envGoesThroughShell(t *testing.T) {
	c := testClient()

	path, args := c.RunCommand(RunRequest{
		SessionId: "session-id",
		Env:       map[string]string{"B": "two", "A": "it's one"},
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
		`export A='it'\''s one'; export B='two'; 'env'`,
	})
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
