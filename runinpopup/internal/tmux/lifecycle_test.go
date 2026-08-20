package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testTMUX = "/tmp/tmux-1000/default,1,0"

// fakeTmuxLogged is fakeTmux with every invocation's argv appended to the
// returned log first, so a test can assert what tmux was asked.
func fakeTmuxLogged(t *testing.T, body string) (path, log string) {
	t.Helper()
	log = filepath.Join(t.TempDir(), "log")
	path = fakeTmux(t, `echo "$@" >> `+log+"\n"+body)
	return path, log
}

func loggedCalls(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("reading the fake tmux's log: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// The full round trip of a display-popup launch: the launcher stays for as
// long as the popup, and the dismissal closes the popup on the client the
// launch named.
func TestClient_StartPopup_lifecycle(t *testing.T) {
	path, log := fakeTmuxLogged(t, ``)
	c := testClient(t, Options{Path: path, TMUX: testTMUX})

	l, err := c.StartPopup(t.Context(), PopupRequest{ClientId: "%1", Command: []string{"true"}})
	if err != nil {
		t.Fatalf("StartPopup: %v", err)
	}
	if err := l.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if err := l.Dismiss(t.Context()); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}

	calls := loggedCalls(t, log)
	if len(calls) != 2 {
		t.Fatalf("tmux was invoked %d times %q, want the popup and the close", len(calls), calls)
	}
	if want := "popup -C -c %1"; calls[1] != want {
		t.Errorf("dismissal ran %q, want %q", calls[1], want)
	}
}

// A launcher that failed carries its own diagnostics: the command line and
// whatever tmux printed are the only trace a popup that never appeared leaves.
func TestClient_StartPopup_waitReportsTheLauncherFailure(t *testing.T) {
	path, _ := fakeTmuxLogged(t, `echo "no server running on /tmp/tmux-1000/default" >&2; exit 1`)
	c := testClient(t, Options{Path: path, TMUX: testTMUX})

	l, err := c.StartPopup(t.Context(), PopupRequest{Command: []string{"true"}})
	if err != nil {
		t.Fatalf("StartPopup: %v", err)
	}
	err = l.Wait()
	if err == nil || !strings.Contains(err.Error(), "no server running") {
		t.Errorf("Wait = %v, want tmux's own message in it", err)
	}
}

// Cancellation interrupts the launcher; the wait must end with it rather than
// sitting out a display-popup that stays for as long as the popup.
func TestClient_StartPopup_cancellation(t *testing.T) {
	path, _ := fakeTmuxLogged(t, `sleep 30`)
	c := testClient(t, Options{Path: path, TMUX: testTMUX})

	ctx, cancel := context.WithCancel(t.Context())
	l, err := c.StartPopup(ctx, PopupRequest{Command: []string{"true"}})
	if err != nil {
		t.Fatalf("StartPopup: %v", err)
	}
	cancel()

	start := time.Now()
	if err := l.Wait(); err == nil {
		t.Error("Wait = nil, want the interrupted launcher reported")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Wait took %v, want it ended by the cancellation", elapsed)
	}
}

// The full round trip of a new-pane launch: the launcher exits the moment the
// pane exists, and the dismissal kills the pane by the id the launcher printed.
func TestClient_StartNewPane_lifecycle(t *testing.T) {
	path, log := fakeTmuxLogged(t, `case "$1" in kill-pane) : ;; *) echo '%5' ;; esac`)
	c := testClient(t, Options{Path: path, TMUX: testTMUX})

	l, err := c.StartNewPane(t.Context(), PaneRequest{SessionId: "work", Command: []string{"true"}})
	if err != nil {
		t.Fatalf("StartNewPane: %v", err)
	}
	if err := l.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if err := l.Dismiss(t.Context()); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}

	calls := loggedCalls(t, log)
	if len(calls) != 2 {
		t.Fatalf("tmux was invoked %d times %q, want the new-pane and the kill", len(calls), calls)
	}
	if want := "kill-pane -t %5"; calls[1] != want {
		t.Errorf("dismissal ran %q, want %q", calls[1], want)
	}
}

// Output without a pane id is a launcher that never got as far as creating a
// pane, and a dismissal must say so instead of guessing at a pane to kill.
func TestClient_StartNewPane_dismissalWithoutAPaneId(t *testing.T) {
	path, log := fakeTmuxLogged(t, `echo "usage: new-pane ..."`)
	c := testClient(t, Options{Path: path, TMUX: testTMUX})

	l, err := c.StartNewPane(t.Context(), PaneRequest{Command: []string{"true"}})
	if err != nil {
		t.Fatalf("StartNewPane: %v", err)
	}
	err = l.Dismiss(t.Context())
	if err == nil || !strings.Contains(err.Error(), "no pane id to close") {
		t.Errorf("Dismiss = %v, want the missing pane id reported", err)
	}
	if !strings.Contains(err.Error(), "usage: new-pane") {
		t.Errorf("Dismiss = %v, want what the launcher printed in it", err)
	}
	if calls := loggedCalls(t, log); len(calls) != 1 {
		t.Errorf("tmux was invoked %d times %q, want no kill attempted", len(calls), calls)
	}
}

// A kill-pane tmux refuses is reported as tmux answered — the caller logs it;
// nothing here invents a cleaner story.
func TestClient_StartNewPane_dismissalFailureIsReported(t *testing.T) {
	path, _ := fakeTmuxLogged(t,
		`case "$1" in kill-pane) echo "can't find pane: %5" >&2; exit 1 ;; *) echo '%5' ;; esac`)
	c := testClient(t, Options{Path: path, TMUX: testTMUX})

	l, err := c.StartNewPane(t.Context(), PaneRequest{Command: []string{"true"}})
	if err != nil {
		t.Fatalf("StartNewPane: %v", err)
	}
	err = l.Dismiss(t.Context())
	if err == nil || !strings.Contains(err.Error(), "can't find pane: %5") {
		t.Errorf("Dismiss = %v, want tmux's refusal in it", err)
	}
}
