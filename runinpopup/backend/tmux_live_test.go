package backend

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

// scratchTmuxSeq keeps two scratch servers in one test binary apart.
var scratchTmuxSeq atomic.Uint64

// scratchTmux starts a private tmux server with one detached two-pane session
// and returns the backend addressing it, plus a func running raw tmux commands
// against it.
//
// The socket name is derived from the test, and every command carries -L, so the
// server is never the user's default one: this package's Prepare exists because
// the wrong move here crashes a whole tmux server, and these tests exercise
// exactly that move.
func scratchTmux(t *testing.T) (*TmuxFloatingPane, func(args ...string) string) {
	t.Helper()
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux is not installed: %v", err)
	}
	// Pid plus a counter: two concurrent "go test" invocations must not land on
	// one server, and neither may ever land on the default socket. Kept short
	// rather than descriptive because the socket path has to fit in a
	// sockaddr_un, which a test name blows past.
	socket := fmt.Sprintf("rip-test-%d-%d", os.Getpid(), scratchTmuxSeq.Add(1))
	t.Logf("scratch tmux socket: %s", socket)

	tmux := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(tmuxPath, append([]string{"-L", socket}, args...)...).Output()
		if execErr, ok := errors.AsType[*exec.ExitError](err); ok {
			t.Fatalf("tmux -L %s %s: %v: %s",
				socket, strings.Join(args, " "), err, execErr.Stderr)
		}
		if err != nil {
			t.Fatalf("tmux -L %s %s: %v", socket, strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out))
	}

	// socketPath is filled in below; the cleanup reads it when it runs, so a
	// failure between here and then still kills the server.
	var socketPath string
	t.Cleanup(func() {
		_ = exec.Command(tmuxPath, "-L", socket, "kill-server").Run()
		if socketPath != "" {
			_ = os.Remove(socketPath)
		}
	})

	tmux("new-session", "-d", "-s", "probe", "sleep 300")
	tmux("split-window", "-d", "-t", "probe", "sleep 300")
	socketPath = tmux("display-message", "-p", "-t", "probe", "#{socket_path}")

	// The backend reaches the scratch server the way it reaches a real one: via
	// $TMUX synthesized from SessionMeta, whose first field is the socket path.
	// TMUX is left empty on purpose so that path is the one under test — running
	// these tests inside tmux must not touch the surrounding server.
	b, err := NewTmuxFloatingPane(Options{
		BinaryPath:  tmuxPath,
		SessionId:   "probe",
		SessionMeta: socketPath + ",0,0",
	})
	if err != nil {
		t.Fatalf("NewTmuxFloatingPane: %v", err)
	}
	return b, tmux
}

// requireZoomCrashAffectedTmux skips when the installed tmux already carries the
// fix: Prepare then legitimately does nothing, so there is no de-zoom to assert.
func requireZoomCrashAffectedTmux(t *testing.T, tmuxPath string) {
	t.Helper()
	out, err := exec.Command(tmuxPath, "-V").Output()
	if err != nil {
		t.Fatalf("tmux -V: %v", err)
	}
	if !tmuxAffectedByZoomCrash(string(out)) {
		t.Skipf("%s is past the floating-pane zoom crash", strings.TrimSpace(string(out)))
	}
}

func TestTmuxFloatingPane_Prepare_live(t *testing.T) {
	b, tmux := scratchTmux(t)

	zoomState := func() string {
		t.Helper()
		return tmux("display-message", "-p", "-t", "probe", zoomStateFormat)
	}

	t.Run("unzoomed window is left alone", func(t *testing.T) {
		restore, err := b.Prepare(t.Context())
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if restore != nil {
			t.Error("restore must be nil: there was no zoom to undo")
		}
		if got := zoomState(); !strings.HasPrefix(got, "0:") {
			t.Errorf("zoom state = %q, want an unzoomed window", got)
		}
	})

	t.Run("zoomed window is de-zoomed and restored", func(t *testing.T) {
		requireZoomCrashAffectedTmux(t, b.tmuxPath)
		tmux("resize-pane", "-Z", "-t", "probe")
		zoomed := zoomState()
		if !strings.HasPrefix(zoomed, "1:") {
			t.Fatalf("zoom state = %q, want the window zoomed before Prepare", zoomed)
		}

		restore, err := b.Prepare(t.Context())
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if restore == nil {
			t.Fatal("restore must not be nil: the zoom has to be put back")
		}
		if got := zoomState(); got != "0:"+strings.TrimPrefix(zoomed, "1:") {
			t.Fatalf("zoom state = %q, want the same pane de-zoomed", got)
		}

		if err := restore(t.Context()); err != nil {
			t.Fatalf("restore: %v", err)
		}
		if got := zoomState(); got != zoomed {
			t.Errorf("zoom state = %q, want the pre-Prepare state %q", got, zoomed)
		}
	})
}

// Prepare must reach the server the popup will open in, not whichever one the
// ambient environment points at, or it reports the zoom state of the wrong
// window and the guard silently stops guarding.
func TestTmuxFloatingPane_Prepare_liveWrongServerIsNotConsulted(t *testing.T) {
	b, tmux := scratchTmux(t)
	requireZoomCrashAffectedTmux(t, b.tmuxPath)
	tmux("resize-pane", "-Z", "-t", "probe")

	misdirected, err := NewTmuxFloatingPane(Options{
		BinaryPath:  b.tmuxPath,
		SessionId:   "probe",
		SessionMeta: "/nonexistent/socket,0,0",
	})
	if err != nil {
		t.Fatalf("NewTmuxFloatingPane: %v", err)
	}
	if _, err := misdirected.Prepare(t.Context()); err == nil {
		t.Fatal("Prepare must fail when the zoom state cannot be read: it fails closed")
	}
}
