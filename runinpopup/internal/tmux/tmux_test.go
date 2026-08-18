package tmux

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const sessionMeta = "/run/user/1000/tmux-1000/default,111,0"

// fakeTmux writes an executable standing in for tmux, running body under /bin/sh
// with the tmux argv, and returns its path.
func fakeTmux(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("writing the fake tmux: %v", err)
	}
	return path
}

func TestNew_sessionMeta(t *testing.T) {
	// Malformed meta only matters when it is the value that will be exported.
	if _, err := New(Options{SessionMeta: "not-a-meta"}); err == nil {
		t.Error("malformed session meta must be rejected when $TMUX is unset")
	}
	if _, err := New(Options{
		SessionMeta: "not-a-meta",
		TMUX:        "/tmp/tmux-1000/default,1,0",
	}); err != nil {
		t.Errorf("session meta is unused when $TMUX is set, got %v", err)
	}
}

func TestClient_Environ(t *testing.T) {
	c := testClient(t, Options{Path: "/usr/bin/tmux", SessionMeta: sessionMeta})
	if got := c.Environ(); !slices.Equal(got, []string{"TMUX=" + sessionMeta}) {
		t.Errorf("Environ = %#v, want TMUX set from the session meta", got)
	}

	inside := testClient(t, Options{SessionMeta: sessionMeta, TMUX: "/other,2,0"})
	if got := inside.Environ(); got != nil {
		t.Errorf("Environ = %#v, want nil when $TMUX is already set", got)
	}

	if got := testClient(t, Options{TMUX: "/other,2,0"}).Environ(); got != nil {
		t.Errorf("Environ = %#v, want nil without a session meta", got)
	}
}

// Every tmux command carries the session environ, or it would address the
// default socket's server while the popup opens on another.
func TestClient_run_carriesTheSessionEnviron(t *testing.T) {
	c := testClient(t, Options{
		Path:        fakeTmux(t, `printf '%s' "$TMUX"`),
		SessionMeta: sessionMeta,
	})

	got, err := c.Version(t.Context())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if got != sessionMeta {
		t.Errorf("$TMUX seen by tmux = %q, want %q", got, sessionMeta)
	}
}

func TestClient_ZoomedPane(t *testing.T) {
	for _, tc := range []struct {
		name       string
		out        string
		wantZoomed bool
		wantPane   string
		wantErr    bool
	}{
		{name: "zoomed", out: "1:%3", wantZoomed: true, wantPane: "%3"},
		{name: "not zoomed", out: "0:%3", wantPane: "%3"},
		{name: "trailing newline", out: "1:%3\n", wantZoomed: true, wantPane: "%3"},
		{name: "no pane id", out: "1:", wantErr: true},
		{name: "no separator", out: "1", wantErr: true},
		{name: "empty", out: "", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, Options{
				Path: fakeTmux(t, `printf '%s' '`+tc.out+`'`),
				TMUX: "/tmp/tmux-1000/default,1,0",
			})

			zoomed, paneId, err := c.ZoomedPane(t.Context(), "work")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ZoomedPane = %v, %q, want an error", zoomed, paneId)
				}
				return
			}
			if err != nil {
				t.Fatalf("ZoomedPane: %v", err)
			}
			if zoomed != tc.wantZoomed || paneId != tc.wantPane {
				t.Errorf("ZoomedPane = %v, %q, want %v, %q",
					zoomed, paneId, tc.wantZoomed, tc.wantPane)
			}
		})
	}
}

// tmux reports "can't find pane" and friends on stderr, so a failure has to
// carry it — together with the command that produced it.
func TestClient_run_decoratesFailures(t *testing.T) {
	fake := fakeTmux(t, "echo \"can't find pane: %9\" >&2\nexit 1")
	c := testClient(t, Options{Path: fake, TMUX: "/tmp/tmux-1000/default,1,0"})

	_, _, err := c.ZoomedPane(t.Context(), "work")
	want := "querying the zoomed pane: " + fake +
		" display-message -p -t work " + ZoomStateFormat +
		": exit status 1: can't find pane: %9"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}
