package tmux

import (
	"slices"
	"testing"
)

func testClient(t *testing.T, opts Options) *Client {
	t.Helper()
	c, err := New(opts)
	if err != nil {
		t.Fatalf("New(%#v): %v", opts, err)
	}
	return c
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

func TestClient_PopupCommand(t *testing.T) {
	c := testClient(t, Options{Path: "/usr/bin/tmux", SessionMeta: sessionMeta})

	path, args := c.PopupCommand(PopupRequest{
		ClientId: "%1",
		Title:    "editor",
		Env:      map[string]string{"B": "2", "A": "1"},
		Command:  []string{"vim", "my file.txt"},
	})
	assertCommand(t, path, args, "/usr/bin/tmux", []string{
		"popup",
		"-c", "%1",
		"-T", "editor",
		"-e", "A=1",
		"-e", "B=2",
		"-E", `'vim' 'my file.txt'`,
	})
}

func TestClient_PopupCommand_noClient(t *testing.T) {
	c := testClient(t, Options{TMUX: "/tmp/tmux-1000/default,1,0"})

	path, args := c.PopupCommand(PopupRequest{Command: []string{"true"}})
	assertCommand(t, path, args, "tmux", []string{"popup", "-E", `'true'`})
}

// Geometry is display-popup's own vocabulary, so every value goes through
// untranslated — the specifier as much as the cells and the percentage. -y is
// therefore the bottom edge tmux reads it as; a caller placing a top edge has
// already done that arithmetic.
func TestClient_PopupCommand_geometry(t *testing.T) {
	c := testClient(t, Options{TMUX: "/tmp/tmux-1000/default,1,0"})

	path, args := c.PopupCommand(PopupRequest{
		Title:   "editor",
		X:       "C",
		Y:       "10",
		Width:   "80%",
		Height:  "20",
		Command: []string{"true"},
	})
	assertCommand(t, path, args, "tmux", []string{
		"popup",
		"-T", "editor",
		"-x", "C",
		"-y", "10",
		"-w", "80%",
		"-h", "20",
		"-E", `'true'`,
	})
}

// A field left empty is not a value: it emits no flag, and the popup lands
// wherever tmux would have put it.
func TestClient_PopupCommand_partialGeometry(t *testing.T) {
	c := testClient(t, Options{TMUX: "/tmp/tmux-1000/default,1,0"})

	path, args := c.PopupCommand(PopupRequest{Width: "80%", Command: []string{"true"}})
	assertCommand(t, path, args, "tmux", []string{"popup", "-w", "80%", "-E", `'true'`})
}

// "--" separates a payload that could start with "-"; -d must stay away or the
// popup never takes the keyboard.
func TestClient_NewPaneCommand(t *testing.T) {
	c := testClient(t, Options{Path: "/usr/bin/tmux", SessionMeta: sessionMeta})

	path, args := c.NewPaneCommand(PaneRequest{
		SessionId: "work",
		Env:       map[string]string{"B": "2", "A": "1"},
		Command:   []string{"vim", "my file.txt"},
	})
	assertCommand(t, path, args, "/usr/bin/tmux", []string{
		"new-pane",
		"-t", "work",
		"-e", "A=1",
		"-e", "B=2",
		"--", `'vim' 'my file.txt'`,
	})
	if slices.Contains(args, "-d") {
		t.Error("-d would leave the focus outside the popup")
	}
}

// new-pane names the same four values differently from display-popup: its
// lowercase -x/-y are the size and its uppercase -X/-Y the position, so a
// popup's -w/-h arrive here as -x/-y. Asserted literally because the mapping is
// the whole risk — swapped, it would still be a valid command line.
func TestClient_NewPaneCommand_geometry(t *testing.T) {
	c := testClient(t, Options{TMUX: "/tmp/tmux-1000/default,1,0"})

	path, args := c.NewPaneCommand(PaneRequest{
		X:       "C",
		Y:       "10",
		Width:   "80%",
		Height:  "20",
		Command: []string{"true"},
	})
	assertCommand(t, path, args, "tmux", []string{
		"new-pane",
		"-X", "C",
		"-Y", "10",
		"-x", "80%",
		"-y", "20",
		"--", `'true'`,
	})
}

func TestClient_NewPaneCommand_partialGeometry(t *testing.T) {
	c := testClient(t, Options{TMUX: "/tmp/tmux-1000/default,1,0"})

	path, args := c.NewPaneCommand(PaneRequest{Height: "20", Command: []string{"true"}})
	assertCommand(t, path, args, "tmux", []string{"new-pane", "-y", "20", "--", `'true'`})
}

func TestClient_NewPaneCommand_noSession(t *testing.T) {
	c := testClient(t, Options{TMUX: "/tmp/tmux-1000/default,1,0"})

	path, args := c.NewPaneCommand(PaneRequest{Command: []string{"true"}})
	assertCommand(t, path, args, "tmux", []string{"new-pane", "--", `'true'`})
}
