package legacyshim

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

// fakeBackend stands in for the multiplexer. Nothing on it is ever called: what
// these tests assert is what a run hands the exchange, not what the exchange
// then does with it.
type fakeBackend struct{}

func (fakeBackend) Name() string { return "fake" }

func (fakeBackend) Launch(context.Context, runinpopup.LaunchSpec) (runinpopup.PopupHandle, error) {
	panic("a test must not open a popup")
}

func (fakeBackend) Prepare(context.Context) (func(context.Context) error, error) {
	panic("a test must not touch the multiplexer")
}

// invocation is one run of a fixture shim standing in for a deprecated binary:
// the environment it finds, the arguments it was given, and whatever the test
// wants changed about the shim itself.
type invocation struct {
	userData string
	debugEnv string
	args     []string
	edit     func(*Shim)
}

// result is everything a run left behind: what the exchange was handed, what
// reached stderr, and the temporary directory the run had to itself.
type result struct {
	launcher *runinpopup.PinentryLauncher
	called   bool
	stderr   *bytes.Buffer
	tempDir  string
	err      error
}

func runShim(t *testing.T, in invocation) *result {
	t.Helper()

	r := &result{stderr: new(bytes.Buffer), tempDir: t.TempDir()}
	t.Setenv("TMPDIR", r.tempDir)
	t.Setenv(userDataEnvVar, in.userData)
	t.Setenv(debugEnvVar, in.debugEnv)

	s := Shim{
		Name:            "fake-popup-pinentry-curses",
		Replacement:     "run-in-popup pinentry --backend fake",
		UserDataFormat:  "FAKE_POPUP:fake_path:session_id:client_id",
		WorkspacePrefix: "legacyshim-test-",
		NewBackend: func(runinpopup.PinentryUserData) (runinpopup.Backend, error) {
			return fakeBackend{}, nil
		},
		call: func(_ context.Context, launcher *runinpopup.PinentryLauncher) error {
			r.launcher, r.called = launcher, true
			return nil
		},
	}
	if in.edit != nil {
		in.edit(&s)
	}
	r.err = s.Run(context.Background(), in.args, r.stderr)
	return r
}

// deprecationNotice is what every run announces, whatever it goes on to do.
const deprecationNotice = `fake-popup-pinentry-curses is deprecated;` +
	` run "run-in-popup pinentry --backend fake" instead.` + "\n"

const validUserData = "FAKE_POPUP:/usr/bin/fake:sess:cli:meta"

func TestShim_Run_announcesItsDeprecationBeforeAnythingElse(t *testing.T) {
	r := runShim(t, invocation{userData: validUserData})
	if !strings.HasPrefix(r.stderr.String(), deprecationNotice) {
		t.Errorf("stderr = %q, want it to open with %q", r.stderr, deprecationNotice)
	}

	failed := runShim(t, invocation{})
	if !strings.HasPrefix(failed.stderr.String(), deprecationNotice) {
		t.Errorf("stderr = %q, want the notice even when the run cannot go on", failed.stderr)
	}
}

func TestShim_Run_validatesUserData(t *testing.T) {
	for _, tc := range []struct {
		name     string
		userData string
	}{
		{name: "unset", userData: ""},
		{name: "kind only", userData: "FAKE_POPUP"},
		{name: "no session id", userData: "FAKE_POPUP:/usr/bin/fake"},
		{name: "no path", userData: "FAKE_POPUP::sess:cli"},
		{name: "not the wire format at all", userData: "/usr/bin/pinentry-curses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runShim(t, invocation{userData: tc.userData})

			want := `environment variable "PINENTRY_USER_DATA" must be formatted as` +
				` "FAKE_POPUP:fake_path:session_id:client_id" but is "` + tc.userData + `"`
			if r.err == nil || r.err.Error() != want {
				t.Errorf("err = %v, want %q", r.err, want)
			}
			if r.called {
				t.Error("the exchange must not run on an environment the binary rejected")
			}
			if entries, err := os.ReadDir(r.tempDir); err != nil || len(entries) != 0 {
				t.Errorf("temp dir holds %v (err %v), want nothing created", entries, err)
			}
		})
	}
}

func TestShim_Run_reportsWhatTheBackendAndTheExchangeReport(t *testing.T) {
	backendErr := errors.New("no multiplexer here")
	r := runShim(t, invocation{
		userData: validUserData,
		edit: func(s *Shim) {
			s.NewBackend = func(runinpopup.PinentryUserData) (runinpopup.Backend, error) {
				return nil, backendErr
			}
		},
	})
	if !errors.Is(r.err, backendErr) {
		t.Errorf("err = %v, want the backend's own error", r.err)
	}
	if r.called {
		t.Error("the exchange must not run without a backend")
	}

	exchangeErr := errors.New("pinentry failed")
	r = runShim(t, invocation{
		userData: validUserData,
		edit: func(s *Shim) {
			s.call = func(context.Context, *runinpopup.PinentryLauncher) error { return exchangeErr }
		},
	})
	if !errors.Is(r.err, exchangeErr) {
		t.Errorf("err = %v, want the exchange's own error", r.err)
	}
}

func TestShim_Run_ordinaryRunLeavesTheDirectoryToTheLaunch(t *testing.T) {
	args := []string{"--display", ":0"}
	r := runShim(t, invocation{userData: validUserData, args: args})

	if r.err != nil || !r.called {
		t.Fatalf("Run = %v, exchange ran: %t, want the exchange to have run", r.err, r.called)
	}
	want := runinpopup.WorkspaceOptions{NamePrefix: "legacyshim-test-"}
	if got := r.launcher.Popup.Workspace; got != want {
		t.Errorf("Workspace = %+v, want %+v: the launch owns the directory", got, want)
	}
	if _, ok := r.launcher.Popup.Backend.(fakeBackend); !ok {
		t.Errorf("Backend = %v, want the one the shim builds", r.launcher.Popup.Backend)
	}
	if !slices.Equal(r.launcher.PinentryArgs, args) {
		t.Errorf("PinentryArgs = %q, want %q passed through", r.launcher.PinentryArgs, args)
	}
	if entries, err := os.ReadDir(r.tempDir); err != nil || len(entries) != 0 {
		t.Errorf("temp dir holds %v (err %v), want nothing created up front", entries, err)
	}
	// The log goes to stderr, where it has always gone: stdout is the Assuan
	// channel and cannot take a byte of it.
	if !strings.Contains(r.stderr.String(), "msg=PINENTRY_USER_DATA") {
		t.Errorf("stderr = %q, want the user data logged to it", r.stderr)
	}
}

func TestShim_Run_debugRunKeepsItsDirectoryAndLog(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   invocation
	}{
		{
			name: "the kind asks for it",
			in:   invocation{userData: "FAKE_POPUP_DEBUG:/usr/bin/fake:sess:cli:meta"},
		},
		{
			// Both binaries answer to the tmux-spelled variable, whatever their kind.
			name: "the environment asks for it",
			in:   invocation{userData: validUserData, debugEnv: "1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runShim(t, tc.in)

			if r.err != nil || !r.called {
				t.Fatalf(
					"Run = %v, exchange ran: %t, want the exchange to have run",
					r.err,
					r.called,
				)
			}
			dir := r.launcher.Popup.Workspace.Dir
			if dir == "" {
				t.Fatal("Workspace.Dir must be set: a debug run keeps the directory it made")
			}
			if base := filepath.Base(dir); !strings.HasPrefix(base, "legacyshim-test-") {
				t.Errorf("dir base = %q, want the shim's prefix", base)
			}
			if _, err := os.Stat(dir); err != nil {
				t.Errorf("Stat(%q) after the run = %v, want the directory still there", dir, err)
			}

			b, err := os.ReadFile(filepath.Join(dir, "log.txt"))
			if err != nil {
				t.Fatalf("reading the debug log: %v", err)
			}
			if !strings.Contains(string(b), "msg=PINENTRY_USER_DATA") {
				t.Errorf("log.txt = %q, want the user data logged into it", b)
			}
			if got := r.stderr.String(); got != deprecationNotice {
				t.Errorf("stderr = %q, want only the notice: a debug run logs to its file", got)
			}
		})
	}
}
