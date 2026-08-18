package runinpopup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngicks/run-in-tmux-popup/runinpopup/internal/shellargv"
)

// shellBackend stands in for a multiplexer: it "opens a popup" by running the
// payload in a local shell, so the launch path can be exercised for real.
type shellBackend struct {
	// environ is what the launcher process runs with, the way the tmux backends
	// pass $TMUX to theirs.
	environ    []string
	prepareErr error
	prepared   int
	restored   int
	// launched records every spec the launcher completed and handed over.
	launched []LaunchSpec
	// output reports what the popup has written so far, for tests that pin when
	// the state may be restored. nil reports nothing.
	output func() string
	// seenOnRestore is what output reported by the time restore ran.
	seenOnRestore string
}

func (b *shellBackend) Name() string { return "shell" }

func (b *shellBackend) Launch(ctx context.Context, spec LaunchSpec) (PopupHandle, error) {
	b.launched = append(b.launched, spec)

	cmd := exec.CommandContext(ctx, "sh", "-c", shellPayload(spec))
	if len(b.environ) > 0 {
		cmd.Env = append(os.Environ(), b.environ...)
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return shellHandle{cmd}, nil
}

// shellPayload renders the spec the way a real mechanism does: one shell command
// line, an argv payload quoted into it.
func shellPayload(spec LaunchSpec) string {
	if spec.Script != "" {
		return spec.Script
	}
	return shellargv.Join(spec.Command)
}

type shellHandle struct{ cmd *exec.Cmd }

func (h shellHandle) Wait() error { return h.cmd.Wait() }

func (b *shellBackend) Prepare(context.Context) (func(context.Context) error, error) {
	if b.prepareErr != nil {
		return nil, b.prepareErr
	}
	b.prepared++
	return func(context.Context) error {
		b.restored++
		if b.output != nil {
			b.seenOnRestore = b.output()
		}
		return nil
	}, nil
}

// popupOutput receives a payload stream the way a caller would, and remembers
// having been closed: the launch owns the endpoints it is handed.
type popupOutput struct {
	bytes.Buffer
	closed bool
}

func (o *popupOutput) Close() error {
	o.closed = true
	return nil
}

func TestPopupLauncher_Exec(t *testing.T) {
	out := new(popupOutput)
	backend := &shellBackend{environ: []string{"POPUP_GREETING=hello"}, output: out.String}
	launcher := &PopupLauncher{Backend: backend}

	popup, err := launcher.Exec(
		t.Context(),
		PopupSpec{Script: `printf '%s from the popup' "$POPUP_GREETING"`},
		PopupStreams{Stdout: out},
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := popup.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if got := out.String(); got != "hello from the popup" {
		t.Errorf("popup output = %q; the backend's environ must reach the popup", got)
	}
	if !out.closed {
		t.Error("the launch must close the endpoint it was handed")
	}
	if backend.prepared != 1 || backend.restored != 1 {
		t.Errorf("prepared = %d, restored = %d, want 1 each", backend.prepared, backend.restored)
	}
	// Restore undoes state the popup needed, so it may only run once the popup is
	// gone and everything it wrote has arrived.
	if backend.seenOnRestore != "hello from the popup" {
		t.Errorf("restore ran before the popup finished, saw %q", backend.seenOnRestore)
	}
}

func TestPopupLauncher_Exec_argvIsNotSplitByTheShell(t *testing.T) {
	out := new(popupOutput)
	launcher := &PopupLauncher{Backend: &shellBackend{}}

	popup, err := launcher.Exec(
		t.Context(),
		PopupSpec{Command: []string{"printf", "%s|", "a b", "c"}},
		PopupStreams{Stdout: out},
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := popup.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := out.String(); got != "a b|c|" {
		t.Errorf("popup output = %q, want %q", got, "a b|c|")
	}
}

// A launch that names no stream must hand the backend the spec it was given:
// a backend able to run an argv directly is not to be pushed through a shell,
// and no directory is created for fifos nobody asked for.
func TestPopupLauncher_Exec_withoutStreams(t *testing.T) {
	backend := &shellBackend{}
	launcher := &PopupLauncher{
		Backend:   backend,
		Workspace: WorkspaceOptions{Dir: filepath.Join(t.TempDir(), "never-created")},
	}

	popup, err := launcher.Exec(t.Context(), PopupSpec{Command: []string{"true"}}, PopupStreams{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := popup.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	spec := backend.launched[0]
	if spec.Script != "" || len(spec.Command) != 1 || spec.Command[0] != "true" {
		t.Errorf("launched %+v, want the spec's own argv untouched", spec)
	}
	if spec.Stdin != nil || spec.Stdout != nil || spec.Stderr != nil {
		t.Error("nothing was allocated, so every stdio must stay on the popup's terminal")
	}
	if r, ok := popup.StdoutPipe(); ok || r != nil {
		t.Error("StdoutPipe must report that nothing was piped")
	}
	if r, ok := popup.StderrPipe(); ok || r != nil {
		t.Error("StderrPipe must report that nothing was piped")
	}
}

// The pipe request is the os/exec-shaped way to read a stream instead of being
// written to; the reader must see the payload's bytes and then a plain EOF.
func TestPopupLauncher_Exec_pipedStreams(t *testing.T) {
	backend := &shellBackend{}
	launcher := &PopupLauncher{Backend: backend}

	popup, err := launcher.Exec(
		t.Context(),
		PopupSpec{Script: `printf out; printf err >&2`},
		PopupStreams{StdoutPipe: true, StderrPipe: true},
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if spec := backend.launched[0]; spec.Stdout == nil || spec.Stderr == nil {
		t.Error("the one-shot spec must name what the payload's stdio was wired to")
	}

	stdout, ok := popup.StdoutPipe()
	if !ok {
		t.Fatal("StdoutPipe was requested but not handed out")
	}
	stderr, ok := popup.StderrPipe()
	if !ok {
		t.Fatal("StderrPipe was requested but not handed out")
	}
	for _, tc := range []struct {
		name string
		r    io.ReadCloser
		want string
	}{
		{"stdout", stdout, "out"},
		{"err", stderr, "err"},
	} {
		b, err := io.ReadAll(tc.r)
		if err != nil {
			t.Fatalf("reading the %s pipe: %v", tc.name, err)
		}
		if string(b) != tc.want {
			t.Errorf("%s pipe carried %q, want %q", tc.name, b, tc.want)
		}
	}
	if err := popup.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

// A non-nil endpoint is the caller's answer to what a stream is for, so it wins
// over a request for a reader of the same stream.
func TestPopupLauncher_Exec_endpointOverridesPipeRequest(t *testing.T) {
	out := new(popupOutput)
	launcher := &PopupLauncher{Backend: &shellBackend{}}

	popup, err := launcher.Exec(
		t.Context(),
		PopupSpec{Script: `printf out`},
		PopupStreams{Stdout: out, StdoutPipe: true},
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if r, ok := popup.StdoutPipe(); ok || r != nil {
		t.Error("StdoutPipe must report false: the endpoint took the stream")
	}
	if err := popup.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := out.String(); got != "out" {
		t.Errorf("popup output = %q, want %q", got, "out")
	}
}

func TestPopupLauncher_Exec_stdinReachesThePayload(t *testing.T) {
	out := new(popupOutput)
	launcher := &PopupLauncher{Backend: &shellBackend{}}

	popup, err := launcher.Exec(
		t.Context(),
		PopupSpec{Script: `cat`},
		PopupStreams{
			Stdin:  io.NopCloser(strings.NewReader("typed at the popup")),
			Stdout: out,
		},
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := popup.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := out.String(); got != "typed at the popup" {
		t.Errorf("the payload read %q, want %q", got, "typed at the popup")
	}
}

func TestPopupLauncher_Exec_popupFailure(t *testing.T) {
	backend := &shellBackend{}
	launcher := &PopupLauncher{Backend: backend}

	popup, err := launcher.Exec(t.Context(), PopupSpec{Script: "exit 3"}, PopupStreams{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	err = popup.Wait()
	if err == nil || !strings.Contains(err.Error(), "popup failed") {
		t.Fatalf("err = %v, want a popup failure", err)
	}
	if backend.restored != 1 {
		t.Errorf("restored = %d, want the state restored even on failure", backend.restored)
	}
}

func TestPopupLauncher_Exec_prepareFailureAborts(t *testing.T) {
	prepareErr := errors.New("de-zoom failed")
	launcher := &PopupLauncher{Backend: &shellBackend{prepareErr: prepareErr}}

	_, err := launcher.Exec(t.Context(), PopupSpec{Script: "exit 0"}, PopupStreams{})
	if !errors.Is(err, prepareErr) {
		t.Fatalf("err = %v, want it to wrap %v", err, prepareErr)
	}
}

// The workspace is the launch's own unless the caller named one, in which case
// the caller keeps it — it may be the directory it holds its own fifos in.
func TestPopupLauncher_Exec_workspace(t *testing.T) {
	run := func(t *testing.T, opts WorkspaceOptions) string {
		t.Helper()
		backend := &shellBackend{}
		launcher := &PopupLauncher{Backend: backend, Workspace: opts}

		popup, err := launcher.Exec(
			t.Context(),
			PopupSpec{Script: "true"},
			PopupStreams{Stdout: new(popupOutput)},
		)
		if err != nil {
			t.Fatalf("Exec: %v", err)
		}
		if err := popup.Wait(); err != nil {
			t.Fatalf("Wait: %v", err)
		}
		// The fifo is only ever named in the command line the payload runs.
		_, fifo, ok := strings.Cut(backend.launched[0].Script, "> ")
		if !ok {
			t.Fatalf("no redirection in %q", backend.launched[0].Script)
		}
		return filepath.Dir(strings.Trim(fifo, "'"))
	}

	t.Run("a launch-created one is removed", func(t *testing.T) {
		dir := run(t, WorkspaceOptions{NamePrefix: "launch-test-"})
		if !strings.Contains(filepath.Base(dir), "launch-test-") {
			t.Errorf("workspace %q does not carry the requested prefix", dir)
		}
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("stat %q = %v, want the workspace removed", dir, err)
		}
	})

	t.Run("the caller's is left alone", func(t *testing.T) {
		own := t.TempDir()
		dir := run(t, WorkspaceOptions{Dir: own})
		if dir != own {
			t.Fatalf("fifos landed in %q, want the caller's %q", dir, own)
		}
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("stat %q = %v, want the caller's directory untouched", dir, err)
		}
	})
}
