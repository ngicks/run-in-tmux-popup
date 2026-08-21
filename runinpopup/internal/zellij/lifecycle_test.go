package zellij

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeZellij writes an executable standing in for zellij, running body under
// /bin/sh with the zellij argv, and returns its path. Every invocation appends
// its argv to log first, so a test can assert what zellij was asked.
func fakeZellij(t *testing.T, body string) (path, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "log")
	path = filepath.Join(dir, "zellij")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the fake zellij: %v", err)
	}
	return path, log
}

func loggedCalls(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("reading the fake zellij's log: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// The full round trip of a run without an environment: the launcher exits the
// moment the pane exists, and the dismissal closes the pane by the id the
// launcher printed.
func TestClient_StartRun_lifecycle(t *testing.T) {
	path, log := fakeZellij(t, `case "$*" in *close-pane*) : ;; *) echo terminal_7 ;; esac`)
	c := New(Options{Path: path})

	l, err := c.StartRun(t.Context(), RunRequest{SessionId: "work", Command: []string{"true"}})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := l.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if err := l.Dismiss(t.Context()); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}

	calls := loggedCalls(t, log)
	if len(calls) != 2 {
		t.Fatalf("zellij was invoked %d times %q, want the run and the close", len(calls), calls)
	}
	if want := "--session=work action close-pane --pane-id=terminal_7"; calls[1] != want {
		t.Errorf("dismissal ran %q, want %q", calls[1], want)
	}
}

// A launcher that failed carries its own diagnostics: the command line and
// whatever zellij printed are the only trace a pane that never appeared leaves.
func TestClient_StartRun_waitReportsTheLauncherFailure(t *testing.T) {
	path, _ := fakeZellij(t, `echo "No session named work found" >&2; exit 1`)
	c := New(Options{Path: path})

	l, err := c.StartRun(t.Context(), RunRequest{SessionId: "work", Command: []string{"true"}})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	err = l.Wait()
	if err == nil || !strings.Contains(err.Error(), "No session named work") {
		t.Errorf("Wait = %v, want zellij's own message in it", err)
	}
}

// Cancellation interrupts the launcher; the wait must end with it rather than
// sitting out a zellij that would have stayed for as long as the pane.
func TestClient_StartRun_cancellation(t *testing.T) {
	path, _ := fakeZellij(t, `sleep 30`)
	c := New(Options{Path: path})

	ctx, cancel := context.WithCancel(t.Context())
	l, err := c.StartRun(ctx, RunRequest{Command: []string{"true"}})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
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

// The environment travels over the env FIFO and Wait joins the delivery, so by
// the time Wait returns the pane has read what it will source — the workspace
// holding the FIFO can only then be taken away. The fake runs the real payload
// under a real sh, so this is also where sourcing a FIFO is proven against the
// actual shell.
func TestClient_StartRun_envReachesThePane(t *testing.T) {
	// The pane outlives "zellij run": the payload is run in the background with
	// the launcher's pipes released, and the launcher exits as soon as the
	// "pane" exists.
	path, _ := fakeZellij(t, `while [ "$1" != "--" ]; do shift; done; shift
( "$@" ) >/dev/null 2>&1 &
echo terminal_3`)
	c := New(Options{Path: path})
	dir := t.TempDir()
	result := filepath.Join(dir, "result")

	l, err := c.StartRun(t.Context(), RunRequest{
		Env:     map[string]string{"GREETING": "hello popup"},
		WorkDir: dir,
		Script:  `printf '%s' "$GREETING" > ` + result,
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := l.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Wait pins the sourcing, not the payload's completion; the write behind the
	// sourcing gate lands a moment later.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(result); err == nil {
			if got := string(b); got != "hello popup" {
				t.Errorf("the payload saw GREETING=%q, want %q", got, "hello popup")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the payload never wrote its result")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A pane that never comes for its environment must not hold Wait forever: the
// delivery is bounded, and its timeout is the launch's answer.
func TestClient_StartRun_envDeliveryTimesOutWithoutAPane(t *testing.T) {
	path, _ := fakeZellij(t, `echo terminal_3`)
	c := New(Options{Path: path})

	l, err := c.StartRun(t.Context(), RunRequest{
		Env:            map[string]string{"KEY": "value"},
		WorkDir:        t.TempDir(),
		StartupTimeout: 100 * time.Millisecond,
		Command:        []string{"true"},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	start := time.Now()
	err = l.Wait()
	if err == nil || !strings.Contains(err.Error(), "delivering the popup environment") {
		t.Errorf("Wait = %v, want the abandoned delivery named", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Wait took %v, far past the delivery bound", elapsed)
	}
}

// A launcher that failed created no pane, so nobody is coming for the
// environment: its delivery is ended rather than sat out, and the launcher's
// error is the one reported.
func TestClient_StartRun_launcherFailureEndsTheEnvDelivery(t *testing.T) {
	path, _ := fakeZellij(t, `echo "No session named work found" >&2; exit 1`)
	c := New(Options{Path: path})

	l, err := c.StartRun(t.Context(), RunRequest{
		SessionId:      "work",
		Env:            map[string]string{"KEY": "value"},
		WorkDir:        t.TempDir(),
		StartupTimeout: 30 * time.Second,
		Command:        []string{"true"},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	start := time.Now()
	err = l.Wait()
	if err == nil || !strings.Contains(err.Error(), "No session named work") {
		t.Errorf("Wait = %v, want the launcher failure, not the delivery", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Wait took %v, want the delivery ended with the launcher", elapsed)
	}
}

// Output without a pane id is a launcher that never got as far as creating a
// pane, and a dismissal must say so instead of guessing at a pane to close.
func TestClient_StartRun_dismissalWithoutAPaneId(t *testing.T) {
	path, log := fakeZellij(t, `echo "created a pane"`)
	c := New(Options{Path: path})

	l, err := c.StartRun(t.Context(), RunRequest{Command: []string{"true"}})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	err = l.Dismiss(t.Context())
	if err == nil || !strings.Contains(err.Error(), "no pane id to close") {
		t.Errorf("Dismiss = %v, want the missing pane id reported", err)
	}
	if !strings.Contains(err.Error(), "created a pane") {
		t.Errorf("Dismiss = %v, want what the launcher printed in it", err)
	}
	if calls := loggedCalls(t, log); len(calls) != 1 {
		t.Errorf("zellij was invoked %d times %q, want no close attempted", len(calls), calls)
	}
}

// A close-pane the multiplexer refuses is reported as it answered — the caller
// logs it; nothing here invents a cleaner story.
func TestClient_StartRun_dismissalFailureIsReported(t *testing.T) {
	path, _ := fakeZellij(t,
		`case "$*" in *close-pane*) echo "no pane with id terminal_9" >&2; exit 1 ;; *) echo terminal_9 ;; esac`)
	c := New(Options{Path: path})

	l, err := c.StartRun(t.Context(), RunRequest{Command: []string{"true"}})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	err = l.Dismiss(t.Context())
	if err == nil || !strings.Contains(err.Error(), "no pane with id terminal_9") {
		t.Errorf("Dismiss = %v, want zellij's refusal in it", err)
	}
}
