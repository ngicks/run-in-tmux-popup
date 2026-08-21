package runinpopup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ngicks/run-in-tmux-popup/runinpopup/internal/shellargv"
)

// shellBackend stands in for a multiplexer: it "opens a popup" by running the
// payload in a local shell, so the launch path can be exercised for real.
type shellBackend struct {
	// environ is what the launcher process runs with, the way the tmux backends
	// pass $TMUX to theirs.
	environ    []string
	prepareErr error
	launchErr  error
	prepared   int
	restored   int
	// launched records every spec the launcher completed and handed over.
	launched []LaunchSpec
	// output reports what the popup has written so far, for tests that pin when
	// the state may be restored. nil reports nothing.
	output func() string
	// seenOnRestore is what output reported by the time restore ran.
	seenOnRestore string
	// stdio stands in for the pane the payload would be looking at: whatever it
	// writes without naming a descriptor lands here. Readable once the launcher
	// has been waited on, which joins the copier filling it.
	stdio bytes.Buffer
}

func (b *shellBackend) Name() string { return "shell" }

func (b *shellBackend) Launch(ctx context.Context, spec LaunchSpec) (PopupHandle, error) {
	b.launched = append(b.launched, spec)
	if b.launchErr != nil {
		return nil, b.launchErr
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", shellPayload(spec))
	// One writer for both, which os/exec answers with one descriptor and one
	// copier — the pane the payload draws on does not tell them apart either.
	cmd.Stdout, cmd.Stderr = &b.stdio, &b.stdio
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

// Dismiss stands in for closing the popup: a real one takes the payload with
// it, and killing the shell it runs in is what that looks like here. A popup
// that has already closed itself is nothing to report.
func (h shellHandle) Dismiss(context.Context) error {
	if err := h.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// emptyPopupBackend opens a popup that never reaches its payload: its launcher
// does its part and exits, but the command line wiring the payload's streams is
// never run, so nothing opens their other end.
type emptyPopupBackend struct{ *shellBackend }

func (b *emptyPopupBackend) Launch(ctx context.Context, spec LaunchSpec) (PopupHandle, error) {
	spec.Command, spec.Script = nil, "true"
	return b.shellBackend.Launch(ctx, spec)
}

// failingLauncherBackend opens a popup that goes nowhere: its launcher starts
// and then fails, the way a mechanism that could not create its pane does, so
// nothing ever opens the payload's end of the streams.
type failingLauncherBackend struct {
	*shellBackend
	err error
}

func (b *failingLauncherBackend) Launch(_ context.Context, spec LaunchSpec) (PopupHandle, error) {
	b.launched = append(b.launched, spec)
	return failedHandle{b.err}, nil
}

type failedHandle struct{ err error }

func (h failedHandle) Wait() error { return h.err }

// Dismiss has nothing to close: the mechanism never got as far as a popup.
func (h failedHandle) Dismiss(context.Context) error { return nil }

// detachedBackend is a floating-pane-style mechanism: its launcher returns as
// soon as the popup exists, long before the payload running in it is done.
type detachedBackend struct{ *shellBackend }

func (b *detachedBackend) Launch(ctx context.Context, spec LaunchSpec) (PopupHandle, error) {
	handle, err := b.shellBackend.Launch(ctx, spec)
	if err != nil {
		return nil, err
	}
	// The popup outlives the launcher this stands in for, so the process behind
	// it is reaped on the side.
	go func() { _ = handle.Wait() }()
	return detachedHandle{handle}, nil
}

// detachedHandle is the launcher's half of such a mechanism: waiting on it says
// nothing, while dismissing it still has to reach the popup the launcher left
// behind.
type detachedHandle struct{ popup PopupHandle }

func (detachedHandle) Wait() error { return nil }

func (h detachedHandle) Dismiss(ctx context.Context) error { return h.popup.Dismiss(ctx) }

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
	if spec.WorkDir != "" {
		t.Errorf("WorkDir = %q, want none: the launch needed no directory", spec.WorkDir)
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

// Where an allocated fifo lands is the difference between the two modes, and it
// is the payload's plain stdout that shows it: taken over, it is the caller's
// stream; left alone, it is the pane, and the caller's stream is the descriptor
// beside it.
func TestPopupLauncher_Exec_stdioAttachment(t *testing.T) {
	for _, tc := range []struct {
		name      string
		keepStdio bool
		script    string
		wantOut   string
		wantPane  string
	}{
		{
			name:    "the fifo takes the stdio it stands for",
			script:  `printf 'plain stdout'`,
			wantOut: "plain stdout",
		},
		{
			name:      "the fifo arrives beside the stdio it stands for",
			keepStdio: true,
			script:    `printf 'plain stdout'; printf 'named fd 4' >&4`,
			wantOut:   "named fd 4",
			wantPane:  "plain stdout",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &shellBackend{}
			out := new(popupOutput)
			launcher := &PopupLauncher{Backend: backend}

			popup, err := launcher.Exec(
				t.Context(),
				PopupSpec{Script: tc.script},
				PopupStreams{Stdout: out, KeepStdio: tc.keepStdio},
			)
			if err != nil {
				t.Fatalf("Exec: %v", err)
			}
			if err := popup.Wait(); err != nil {
				t.Fatalf("Wait: %v", err)
			}

			if got := out.String(); got != tc.wantOut {
				t.Errorf("the endpoint got %q, want %q", got, tc.wantOut)
			}
			if got := backend.stdio.String(); got != tc.wantPane {
				t.Errorf("the popup's own stdio got %q, want %q", got, tc.wantPane)
			}
		})
	}
}

// Beside the payload's stdio the fifos are a channel it opts into, both ways:
// fd 3 carries what the caller sent, fd 4 and fd 5 what the payload answers, and
// the exported paths open the same fifos for a payload that would rather name
// them than inherit them.
func TestPopupLauncher_Exec_keepStdio(t *testing.T) {
	backend := &shellBackend{}
	out, errOut := new(popupOutput), new(popupOutput)
	launcher := &PopupLauncher{Backend: backend}

	popup, err := launcher.Exec(
		t.Context(),
		PopupSpec{Script: `printf 'on the pane'
cat <&3 >&4
printf ' and by name' > "$TTY_OUT"
printf 'to the caller' >&5`},
		PopupStreams{
			Stdin:     io.NopCloser(strings.NewReader("sent by the caller")),
			Stdout:    out,
			Stderr:    errOut,
			KeepStdio: true,
		},
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := popup.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if got, want := out.String(), "sent by the caller and by name"; got != want {
		t.Errorf("stdout endpoint = %q, want %q", got, want)
	}
	if got, want := errOut.String(), "to the caller"; got != want {
		t.Errorf("stderr endpoint = %q, want %q", got, want)
	}
	if got, want := backend.stdio.String(), "on the pane"; got != want {
		t.Errorf("the popup's own stdio = %q, want %q", got, want)
	}
}

// Nothing obliges a payload to use the descriptors beside its stdio, so the
// streams cannot depend on it doing so: the group holding them open is what
// closes them, and the payload exiting is therefore their end.
func TestPopupLauncher_Exec_keepStdio_untouchedStreamsEndWithThePayload(t *testing.T) {
	out, errOut := new(popupOutput), new(popupOutput)
	launcher := &PopupLauncher{Backend: &shellBackend{}}

	popup, err := launcher.Exec(
		t.Context(),
		PopupSpec{Script: `printf 'only on the pane'`},
		PopupStreams{Stdout: out, Stderr: errOut, KeepStdio: true},
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := popup.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if out.String() != "" || errOut.String() != "" {
		t.Errorf("stdout = %q, stderr = %q; want nothing: the payload wrote to neither",
			out.String(), errOut.String())
	}
	if !out.closed || !errOut.closed {
		t.Error("both streams must have ended with the payload")
	}
}

// The command line a launch builds is the whole of its contract with the
// payload, so it is pinned as text in both directions: the redirections that
// take the payload's stdio to the fifos, and — when its stdio is to be left
// alone — the descriptors and the exported paths that carry the fifos beside it.
func TestLaunchCommandLine(t *testing.T) {
	for _, tc := range []struct {
		name        string
		spec        PopupSpec
		streams     PopupStreams
		wantCommand []string
		wantScript  string
	}{
		{
			name:        "nothing allocated leaves the argv alone",
			spec:        PopupSpec{Command: []string{"make", "test"}},
			streams:     PopupStreams{},
			wantCommand: []string{"make", "test"},
		},
		{
			name:       "nothing allocated leaves a script alone",
			spec:       PopupSpec{Script: "make test"},
			streams:    PopupStreams{},
			wantScript: "make test",
		},
		{
			// The mode says where an allocated fifo lands, so on its own it must
			// not put a wrapper around a payload that was allocated none.
			name:        "the mode alone allocates nothing",
			spec:        PopupSpec{Command: []string{"make", "test"}},
			streams:     PopupStreams{KeepStdio: true},
			wantCommand: []string{"make", "test"},
		},
		{
			name: "an argv payload is quoted into the group",
			spec: PopupSpec{Command: []string{"make", "test a"}},
			streams: PopupStreams{
				Stdin:  io.NopCloser(strings.NewReader("")),
				Stdout: new(popupOutput),
				Stderr: new(popupOutput),
			},
			wantScript: "{ 'make' 'test a'\n" +
				`} < '/w/stdin' > '/w/stdout' 2> '/w/stderr'`,
		},
		{
			name: "one stream is redirected, the rest stays on the terminal",
			spec: PopupSpec{Script: "make test"},
			streams: PopupStreams{
				Stdout: new(popupOutput),
			},
			wantScript: "{ make test\n" +
				`} > '/w/stdout'`,
		},
		{
			name:       "a piped stream is allocated like any other",
			spec:       PopupSpec{Script: "make test"},
			streams:    PopupStreams{StderrPipe: true},
			wantScript: "{ make test\n" + `} 2> '/w/stderr'`,
		},
		{
			name: "the payload keeps its stdio and gets the fifos beside it",
			spec: PopupSpec{Command: []string{"make", "test a"}},
			streams: PopupStreams{
				Stdin:     io.NopCloser(strings.NewReader("")),
				Stdout:    new(popupOutput),
				Stderr:    new(popupOutput),
				KeepStdio: true,
			},
			wantScript: `export TTY_IN='/w/stdin' TTY_OUT='/w/stdout' TTY_ERR='/w/stderr'` + "\n" +
				"{ 'make' 'test a'\n" +
				`} 3< '/w/stdin' 4> '/w/stdout' 5> '/w/stderr'`,
		},
		{
			// A stream nobody asked for is not there to be found: no descriptor
			// under it, and no variable naming it either.
			name:    "an unallocated stream gets neither a descriptor nor a name",
			spec:    PopupSpec{Script: "make test"},
			streams: PopupStreams{StderrPipe: true, KeepStdio: true},
			wantScript: `export TTY_ERR='/w/stderr'` + "\n" +
				"{ make test\n" + `} 5> '/w/stderr'`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set, _, _ := payloadStreams(tc.streams)
			for _, s := range set {
				s.path = "/w/" + s.name
			}

			command, script := launchCommandLine(tc.spec, set)
			if !slices.Equal(command, tc.wantCommand) {
				t.Errorf("command = %q, want %q", command, tc.wantCommand)
			}
			if script != tc.wantScript {
				t.Errorf("script =\n%s\n\nwant\n%s", script, tc.wantScript)
			}
		})
	}
}

// A fifo that took the payload's stdio is the whole of what that payload is
// given: there is no descriptor beside it and no variable naming it, so a
// payload written for its own streams finds nothing else to reach for.
func TestPopupLauncher_Exec_redirectedStdioComesAlone(t *testing.T) {
	out := new(popupOutput)
	launcher := &PopupLauncher{Backend: &shellBackend{}}

	popup, err := launcher.Exec(
		t.Context(),
		PopupSpec{Script: `printf 'in=[%s] out=[%s] err=[%s] ' "$TTY_IN" "$TTY_OUT" "$TTY_ERR"
if (exec 0<&3) 2>/dev/null; then printf 'fd3=open'; else printf 'fd3=absent'; fi`},
		PopupStreams{Stdout: out},
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := popup.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got, want := out.String(), "in=[] out=[] err=[] fd3=absent"; got != want {
		t.Errorf("the payload saw %q, want %q", got, want)
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

// dismissalBackend records what its popups were asked to close, and on what
// kind of context. Everything else is the shell fake's: the launch has to run
// for real for the cancellation to reach it the way one does.
type dismissalBackend struct {
	*shellBackend
	mu    sync.Mutex
	calls []dismissal
}

// dismissal is one Dismiss call as the handle saw it.
type dismissal struct {
	// liveCtx is whether the context it arrived on was still usable — a
	// dismissal handed the cancellation it answers to could not reach any
	// multiplexer.
	liveCtx bool
	// bounded is whether that context carried a deadline, so a multiplexer that
	// never answers cannot hold the launch open.
	bounded bool
}

func (b *dismissalBackend) Launch(ctx context.Context, spec LaunchSpec) (PopupHandle, error) {
	handle, err := b.shellBackend.Launch(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &dismissalHandle{PopupHandle: handle, backend: b}, nil
}

func (b *dismissalBackend) dismissals() []dismissal {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.calls)
}

type dismissalHandle struct {
	PopupHandle
	backend *dismissalBackend
}

func (h *dismissalHandle) Dismiss(ctx context.Context) error {
	_, bounded := ctx.Deadline()
	h.backend.mu.Lock()
	h.backend.calls = append(
		h.backend.calls,
		dismissal{liveCtx: ctx.Err() == nil, bounded: bounded},
	)
	h.backend.mu.Unlock()
	return h.PopupHandle.Dismiss(ctx)
}

// Canceling a launch closes its popup, which is the only thing that ends a
// payload the launcher cannot reach: interrupting the launcher is a backstop,
// not a dismissal. It happens once however many paths notice the cancellation,
// and the popup is gone by the time the wait returns.
func TestPopupLauncher_Exec_cancellationDismissesThePopup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	backend := &dismissalBackend{shellBackend: &shellBackend{}}

	popup, err := (&PopupLauncher{Backend: backend}).Exec(
		ctx,
		PopupSpec{Script: "sleep 30"},
		PopupStreams{},
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	cancel()
	_ = popup.Wait()

	got := backend.dismissals()
	if len(got) != 1 {
		t.Fatalf("the popup was dismissed %d times, want exactly once: %+v", len(got), got)
	}
	if !got[0].liveCtx {
		t.Error("the dismissal was handed the cancellation it answers to and could not act")
	}
	if !got[0].bounded {
		t.Error(
			"the dismissal was unbounded: a multiplexer that never answers would hold the launch",
		)
	}
}

// A launch that ended on its own leaves the popup alone: exec's closes itself
// when the command exits, and an exchange that outlives its launcher dismisses
// its own. A context canceled afterwards — every caller's is, eventually —
// must not reach back for a popup that is long gone.
func TestPopupLauncher_Exec_normalCompletionDismissesNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	backend := &dismissalBackend{shellBackend: &shellBackend{}}

	popup, err := (&PopupLauncher{Backend: backend}).Exec(
		ctx,
		PopupSpec{Script: "true"},
		PopupStreams{},
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := popup.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := backend.dismissals(); len(got) != 0 {
		t.Fatalf("the popup was dismissed %d times, want none: %+v", len(got), got)
	}

	cancel()
	// Nothing here waits on the cancellation because nothing may act on it: the
	// launch is over, and the popup's own end was the payload exiting.
	if got := backend.dismissals(); len(got) != 0 {
		t.Errorf("a canceled context reached back for a popup that was already gone: %+v", got)
	}
}

// The shell fake exits with its payload's status, exactly as tmux
// display-popup's launcher does, so a command that failed inside the popup must
// not come back as a failed launch — the streams it wrote ran to their end.
func TestPopupCommand_WaitStreams_streamsDecide(t *testing.T) {
	out, errOut := new(popupOutput), new(popupOutput)
	launcher := &PopupLauncher{Backend: &shellBackend{}}

	popup, err := launcher.Exec(
		t.Context(),
		PopupSpec{Script: `printf out; printf err >&2; exit 3`},
		PopupStreams{Stdout: out, Stderr: errOut},
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := popup.WaitStreams(); err != nil {
		t.Fatalf("WaitStreams: %v", err)
	}
	if out.String() != "out" || errOut.String() != "err" {
		t.Errorf("stdout = %q, stderr = %q; want %q and %q",
			out.String(), errOut.String(), "out", "err")
	}
	// Wait is the other half of the contrast: there the launcher's exit is the
	// launch's own verdict.
	if err := popup.Wait(); err == nil || !strings.Contains(err.Error(), "popup failed") {
		t.Errorf("Wait = %v, want the launcher's status reported there", err)
	}
}

// A popup that never appears leaves its streams waiting for a payload that will
// never open them, and the launcher is the only one that knows why.
func TestPopupCommand_WaitStreams_launcherExplainsABrokenStream(t *testing.T) {
	launchErr := errors.New("no pane could be opened")
	launcher := &PopupLauncher{
		Backend: &failingLauncherBackend{
			shellBackend: &shellBackend{},
			err:          launchErr,
		},
		StartupTimeout: 200 * time.Millisecond,
	}

	popup, err := launcher.Exec(
		t.Context(),
		PopupSpec{Script: "true"},
		PopupStreams{Stdout: new(popupOutput)},
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := popup.WaitStreams(); !errors.Is(err, launchErr) {
		t.Fatalf("WaitStreams = %v, want it to wrap %v", err, launchErr)
	}
}

// A launcher that did its part says nothing about a popup that never ran the
// command line, so the rendezvous bound is what ends the wait and what is
// reported.
func TestPopupCommand_WaitStreams_rendezvousTimeout(t *testing.T) {
	launcher := &PopupLauncher{
		Backend:        &emptyPopupBackend{shellBackend: &shellBackend{}},
		StartupTimeout: 200 * time.Millisecond,
	}

	popup, err := launcher.Exec(
		t.Context(),
		PopupSpec{Script: "true"},
		PopupStreams{Stdout: new(popupOutput)},
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	err = popup.WaitStreams()
	if err == nil || !strings.Contains(err.Error(), "did not reach its payload") {
		t.Fatalf("WaitStreams = %v, want the rendezvous bound to end the wait", err)
	}
}

// The launcher of a floating-pane mechanism is gone while the payload is still
// writing, so the streams — not it — are what the wait is for.
func TestPopupCommand_WaitStreams_launcherExitsWhileThePayloadRuns(t *testing.T) {
	out := new(popupOutput)
	launcher := &PopupLauncher{Backend: &detachedBackend{shellBackend: &shellBackend{}}}

	popup, err := launcher.Exec(
		t.Context(),
		PopupSpec{Script: `sleep 0.2; printf late`},
		PopupStreams{Stdout: out},
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := popup.WaitStreams(); err != nil {
		t.Fatalf("WaitStreams: %v", err)
	}
	if out.String() != "late" {
		t.Errorf("popup output = %q, want %q: the wait ended before the payload did",
			out.String(), "late")
	}
}

// Without an output endpoint WaitStreams has nothing to decide by, so a failed
// launcher must not vanish into a vacuously clean verdict: such a launch waits
// through Wait, launcher failure included.
func TestPopupCommand_WaitStreams_noEndpointFallsBackToWait(t *testing.T) {
	launchErr := errors.New("no pane could be opened")
	launcher := &PopupLauncher{
		Backend: &failingLauncherBackend{
			shellBackend: &shellBackend{},
			err:          launchErr,
		},
	}

	popup, err := launcher.Exec(t.Context(), PopupSpec{Script: "true"}, PopupStreams{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := popup.WaitStreams(); !errors.Is(err, launchErr) {
		t.Fatalf("WaitStreams = %v, want the launcher failure through the Wait fallback", err)
	}
}

// A caller-provided workspace is where the pinentry handshake's FIFOs go, so a
// directory anyone else could write into — swapping a FIFO for their own — is
// refused before anything is placed in it. Only ownership and the directory's
// own mode are checkable without root, so those are what the test drives.
func TestWorkspaceOptions_callerDirMustBeSafe(t *testing.T) {
	launch := func(dir string) error {
		launcher := &PopupLauncher{
			Backend:   &shellBackend{},
			Workspace: WorkspaceOptions{Dir: dir},
		}
		popup, err := launcher.Exec(
			t.Context(),
			PopupSpec{Script: "cat"},
			PopupStreams{Stdin: io.NopCloser(strings.NewReader(""))},
		)
		if err == nil {
			if werr := popup.Wait(); werr != nil {
				t.Errorf("Wait: %v", werr)
			}
		}
		return err
	}

	t.Run("a private directory passes", func(t *testing.T) {
		if err := launch(t.TempDir()); err != nil {
			t.Fatalf("Exec: %v", err)
		}
	})

	for _, tc := range []struct {
		name string
		perm os.FileMode
	}{
		{name: "group-writable", perm: 0o770},
		{name: "other-writable", perm: 0o707},
	} {
		t.Run(tc.name+" is refused", func(t *testing.T) {
			dir := t.TempDir()
			// Chmod rather than Mkdir with the mode, which the umask would strip
			// the interesting bits from.
			if err := os.Chmod(dir, tc.perm); err != nil {
				t.Fatalf("chmod: %v", err)
			}
			err := launch(dir)
			if err == nil || !strings.Contains(err.Error(), "writable by other users") {
				t.Fatalf("err = %v, want the unsafe mode refused", err)
			}
		})
	}

	t.Run("a plain file is refused", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("writing the file: %v", err)
		}
		err := launch(path)
		if err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Fatalf("err = %v, want the non-directory refused", err)
		}
	})

	t.Run("a missing directory is refused", func(t *testing.T) {
		err := launch(filepath.Join(t.TempDir(), "never-created"))
		if err == nil || !strings.Contains(err.Error(), "checking the popup workspace") {
			t.Fatalf("err = %v, want the stat failure surfaced", err)
		}
	})
}

func TestPopupLauncher_Exec_prepareFailureAborts(t *testing.T) {
	prepareErr := errors.New("de-zoom failed")
	launcher := &PopupLauncher{Backend: &shellBackend{prepareErr: prepareErr}}

	_, err := launcher.Exec(t.Context(), PopupSpec{Script: "exit 0"}, PopupStreams{})
	if !errors.Is(err, prepareErr) {
		t.Fatalf("err = %v, want it to wrap %v", err, prepareErr)
	}
}

// The geometry is the backend's to translate, so the launch hands it over as
// given — all four values, whatever mixture of cells, percentages and positions
// they are.
func TestPopupLauncher_Exec_geometryReachesTheBackend(t *testing.T) {
	backend := &shellBackend{}
	launcher := &PopupLauncher{Backend: backend}

	popup, err := launcher.Exec(
		t.Context(),
		PopupSpec{X: "C", Y: "10", Width: "80%", Height: "20", Command: []string{"true"}},
		PopupStreams{},
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := popup.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	spec := backend.launched[0]
	got := [4]string{spec.X, spec.Y, spec.Width, spec.Height}
	if want := [4]string{"C", "10", "80%", "20"}; got != want {
		t.Errorf("geometry = %q, want %q", got, want)
	}
}

// A geometry nobody can act on is a typo, and finding out must cost no popup:
// the launch is refused before the backend is prepared, let alone launched.
func TestPopupLauncher_Exec_malformedGeometryOpensNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spec  PopupSpec
		field string
		value string
	}{
		{name: "x", spec: PopupSpec{X: "middle"}, field: "X", value: "middle"},
		{name: "y", spec: PopupSpec{Y: "-5"}, field: "Y", value: "-5"},
		{name: "width", spec: PopupSpec{Width: "80 %"}, field: "Width", value: "80 %"},
		{
			// A position places a popup; it cannot say how big one is.
			name: "a specifier on a size",
			spec: PopupSpec{Height: "C"}, field: "Height", value: "C",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &shellBackend{}
			launcher := &PopupLauncher{Backend: backend}
			tc.spec.Command = []string{"true"}

			_, err := launcher.Exec(t.Context(), tc.spec, PopupStreams{})
			if err == nil {
				t.Fatal("Exec must fail: the geometry says nothing a backend could act on")
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("err = %v, want the field %q named in it", err, tc.field)
			}
			if !strings.Contains(err.Error(), tc.value) {
				t.Errorf("err = %v, want the value %q quoted in it", err, tc.value)
			}
			if backend.prepared != 0 || len(backend.launched) != 0 {
				t.Errorf("backend prepared %d times and launched %d specs, want neither",
					backend.prepared, len(backend.launched))
			}
		})
	}
}

// The work directory is not only for fifos: a backend whose multiplexer has no
// environment flag has nowhere else to put one out of the argv, so a spec
// carrying an environment gets a directory even when no stream asked for one.
func TestPopupLauncher_Exec_envAloneOpensTheWorkspace(t *testing.T) {
	backend := &shellBackend{}
	launcher := &PopupLauncher{Backend: backend}

	popup, err := launcher.Exec(
		t.Context(),
		PopupSpec{Env: map[string]string{"A": "one"}, Command: []string{"true"}},
		PopupStreams{},
	)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	dir := backend.launched[0].WorkDir
	if dir == "" {
		t.Fatal("a spec carrying an environment must be handed a work directory")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("stat %q = %v, want the directory there for the whole launch", dir, err)
	}

	if err := popup.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stat %q = %v, want the directory given back with the launch", dir, err)
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
