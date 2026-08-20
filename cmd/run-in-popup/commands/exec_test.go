package commands

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

// popupShell stands in for a multiplexer: it "opens a popup" by running the
// command line the launch built in a local shell, which is all a backend has to
// do for the bridge to run end to end.
type popupShell struct {
	// launchErr fails the launch the way a multiplexer that could not open a
	// popup does.
	launchErr error
	// empty opens a popup that never runs the command line it was handed, the way
	// one that dies on the way to it does.
	empty bool
}

func (b *popupShell) Name() string { return "shell" }

func (b *popupShell) Prepare(context.Context) (func(context.Context) error, error) {
	return nil, nil
}

func (b *popupShell) Launch(
	ctx context.Context,
	spec runinpopup.LaunchSpec,
) (runinpopup.PopupHandle, error) {
	if b.launchErr != nil {
		return nil, b.launchErr
	}
	// The bridge names both output streams, so what it hands over is always a
	// shell command line with the redirections already in it.
	script := spec.Script
	if b.empty {
		script = "true"
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// A dismissed popup takes everything running inside it along; killing the
	// shell alone would leave the command it started holding the streams open.
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return popupShellHandle{cmd}, nil
}

type popupShellHandle struct{ cmd *exec.Cmd }

func (h popupShellHandle) Wait() error { return h.cmd.Wait() }

// Dismiss closes the popup the way a multiplexer does: the whole group goes,
// command included, which is what ends the streams the bridge is waiting on.
// A group that is already gone is nothing to report — the popup closed itself.
func (h popupShellHandle) Dismiss(context.Context) error {
	if err := syscall.Kill(-h.cmd.Process.Pid, syscall.SIGKILL); err != nil &&
		!errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// popupOutput stands in for one of this process's own output streams: it takes
// what the popup writes, remembers having been closed, and says when the first
// byte arrived so a test can act while the command is still running.
type popupOutput struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	closed  bool
	started chan struct{}
	begin   func()
}

func newPopupOutput() *popupOutput {
	started := make(chan struct{})
	return &popupOutput{started: started, begin: sync.OnceFunc(func() { close(started) })}
}

func (o *popupOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.begin()
	return o.buf.Write(p)
}

func (o *popupOutput) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
	return nil
}

func (o *popupOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

func (o *popupOutput) wasClosed() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.closed
}

func popupLauncher(backend runinpopup.Backend) *runinpopup.PopupLauncher {
	return &runinpopup.PopupLauncher{Backend: backend}
}

func shellSpec(script string) runinpopup.PopupSpec {
	return runinpopup.PopupSpec{Command: []string{"sh", "-c", script}}
}

// noStdin stands in for a caller with nothing to pipe in: the command's stdin is
// there and ends the moment it is read.
func noStdin() io.ReadCloser { return io.NopCloser(strings.NewReader("")) }

// Each stream arrives whole, in its own order, and the command's own exit status
// is none of the bridge's business: it says nothing about whether the two
// streams got here. What the command prints on the popup's own terminal is none
// of the bridge's business either — that is the pane's, and it stays there.
func TestExecBridge_relaysBothStreams(t *testing.T) {
	stdout, stderr := newPopupOutput(), newPopupOutput()

	err := execBridge(
		t.Context(),
		popupLauncher(&popupShell{}),
		shellSpec(`printf 'out one\n' >&4
printf 'err one\n' >&5
printf 'shown in the popup\n'
printf 'and this too\n' >&2
printf 'out two\n' >&4
printf 'err two\n' >&5
exit 3`),
		noStdin(), stdout, stderr,
	)
	if err != nil {
		t.Fatalf("execBridge: %v", err)
	}
	if got, want := stdout.String(), "out one\nout two\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "err one\nerr two\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
	if !stdout.wasClosed() || !stderr.wasClosed() {
		t.Error("the launch owns the endpoints it was handed and must close them")
	}
}

// The third stream goes the other way: what a caller pipes into exec is what the
// command reads, so a pipeline works through the popup.
func TestExecBridge_relaysStdin(t *testing.T) {
	stdout := newPopupOutput()

	err := execBridge(
		t.Context(),
		popupLauncher(&popupShell{}),
		shellSpec("cat <&3 >&4"),
		io.NopCloser(strings.NewReader("piped in by the caller")),
		stdout, newPopupOutput(),
	)
	if err != nil {
		t.Fatalf("execBridge: %v", err)
	}
	if got, want := stdout.String(), "piped in by the caller"; got != want {
		t.Errorf("the command read %q, want %q", got, want)
	}
}

// A caller's stdin is usually its terminal, and a terminal does not end because
// the popup did. The bridge is over when the command's output is here, so the
// read still parked on that terminal must be no part of the wait.
func TestExecBridge_returnsWhileTheCallerStdinStaysOpen(t *testing.T) {
	stdin, neverWritten := io.Pipe()
	// Only so the relay left behind lets go before the test binary does; the
	// bridge must have returned long before this runs.
	t.Cleanup(func() { _ = neverWritten.Close() })

	stdout := newPopupOutput()
	done := make(chan error, 1)
	go func() {
		done <- execBridge(
			t.Context(),
			popupLauncher(&popupShell{}),
			shellSpec("printf 'done without reading stdin' >&4"),
			stdin, stdout, newPopupOutput(),
		)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("execBridge: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the bridge waited on an input relay whose source nobody was going to end")
	}
	if got, want := stdout.String(), "done without reading stdin"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// Nothing collects the output on the way through, so nothing bounds how much of
// it there may be.
func TestExecBridge_outputOutgrowsAPipeBuffer(t *testing.T) {
	stdout := newPopupOutput()

	err := execBridge(
		t.Context(),
		popupLauncher(&popupShell{}),
		shellSpec("seq 1 40000 >&4"),
		noStdin(), stdout, newPopupOutput(),
	)
	if err != nil {
		t.Fatalf("execBridge: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != 40000 || lines[39999] != "40000" {
		t.Errorf("got %d lines ending %q, want 40000 ending \"40000\"",
			len(lines), lines[len(lines)-1])
	}
	if len(stdout.String()) <= 64*1024 {
		t.Errorf("stdout is %d bytes, too small to prove the relay is unbounded",
			len(stdout.String()))
	}
}

// The streams the bridge hands over are this process's own stdout and stderr,
// which it was handed by whoever ran it: the launch closing them at the end of
// the relay must reach no further than the wrapper.
func TestExecBridge_leavesTheProcessStreamsOpen(t *testing.T) {
	stdout := newPopupOutput()

	err := execBridge(
		t.Context(),
		popupLauncher(&popupShell{}),
		shellSpec("printf 'from the popup' >&4"),
		noStdin(), unclosableWriter{stdout}, newPopupOutput(),
	)
	if err != nil {
		t.Fatalf("execBridge: %v", err)
	}
	if stdout.wasClosed() {
		t.Error("the stream behind the wrapper was closed")
	}
	if _, err := stdout.Write([]byte(" and after it")); err != nil {
		t.Fatalf("writing after the bridge: %v", err)
	}
	if got, want := stdout.String(), "from the popup and after it"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// The user closes the popup while the command is still running: the multiplexer
// takes down everything in it, which ends the streams, and the bridge returns
// with what had already arrived rather than waiting on a command that is gone.
func TestExecBridge_popupDismissedWhileTheCommandRuns(t *testing.T) {
	ctx, dismiss := context.WithCancel(t.Context())
	defer dismiss()

	stdout := newPopupOutput()
	done := make(chan error, 1)
	go func() {
		done <- execBridge(
			ctx,
			popupLauncher(&popupShell{}),
			shellSpec("printf started >&4; sleep 30"),
			noStdin(), stdout, newPopupOutput(),
		)
	}()

	<-stdout.started
	dismiss()

	select {
	case <-done:
		// The verdict is deliberately not asserted: the dismissal is modeled by
		// cancellation, and the FIFOs EOFing races the cancellation cause — a nil
		// (normal end) and a failed-relay error are both faithful ends here. The
		// prompt return and the delivered bytes are what the bridge owes either way.
	case <-time.After(10 * time.Second):
		t.Fatal("the bridge did not return after the popup was dismissed")
	}
	if got, want := stdout.String(), "started"; got != want {
		t.Errorf("stdout = %q, want %q: what arrived before the dismissal is still owed",
			got, want)
	}
}

func TestExecBridge_popupThatCannotBeOpened(t *testing.T) {
	launchErr := errors.New("no pane could be opened")

	err := execBridge(
		t.Context(),
		popupLauncher(&popupShell{launchErr: launchErr}),
		shellSpec("true"),
		noStdin(), newPopupOutput(), newPopupOutput(),
	)
	if !errors.Is(err, launchErr) || !strings.Contains(err.Error(), "popup failed") {
		t.Fatalf("execBridge = %v, want a popup failure wrapping %v", err, launchErr)
	}
}

// A popup that never runs the command opens neither stream, and only the bound
// on that rendezvous can end the wait.
func TestExecBridge_popupThatNeverRunsTheCommand(t *testing.T) {
	launcher := popupLauncher(&popupShell{empty: true})
	launcher.StartupTimeout = 200 * time.Millisecond

	start := time.Now()
	err := execBridge(
		t.Context(),
		launcher,
		shellSpec("true"),
		noStdin(), newPopupOutput(), newPopupOutput(),
	)
	elapsed := time.Since(start)

	if err == nil || !strings.Contains(err.Error(), "did not reach its payload") {
		t.Fatalf("execBridge = %v, want the rendezvous bound to end the wait", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("the bridge took %v to give up", elapsed)
	}
}

// parseExecFlags mirrors what execCmd builds — the flags bound to locals — and
// parses argv into them, so Changed and ArgsLenAtDash reflect a real
// invocation.
func parseExecFlags(
	t *testing.T,
	argv []string,
) (*cobra.Command, string, string, execGeometry) {
	t.Helper()
	var (
		flagBackend  string
		flagTitle    string
		flagGeometry execGeometry
	)
	cmd := &cobra.Command{Use: "exec"}
	cmd.Flags().StringVar(&flagBackend, "backend", "", "")
	cmd.Flags().StringVar(&flagTitle, "title", "", "")
	cmd.Flags().StringVar(&flagGeometry.x, "x", "", "")
	cmd.Flags().StringVar(&flagGeometry.y, "y", "", "")
	cmd.Flags().StringVarP(&flagGeometry.width, "width", "w", "", "")
	cmd.Flags().StringVar(&flagGeometry.height, "height", "", "")
	if err := cmd.ParseFlags(argv); err != nil {
		t.Fatalf("ParseFlags(%q): %v", argv, err)
	}
	return cmd, flagBackend, flagTitle, flagGeometry
}

func TestExecCommandArgs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		argv    []string
		want    []string
		wantErr bool
	}{
		{
			name: "everything after the separator",
			argv: []string{"--", "make", "test"},
			want: []string{"make", "test"},
		},
		{
			name: "flags of our own stay ours",
			argv: []string{"--title", "build", "--", "go", "build", "./..."},
			want: []string{"go", "build", "./..."},
		},
		{
			name: "the command keeps its own flags",
			argv: []string{"--", "ls", "-l", "--color=always"},
			want: []string{"ls", "-l", "--color=always"},
		},
		{
			// pflag stops at the first "--", so a command using "--" itself
			// arrives whole.
			name: "a second separator belongs to the command",
			argv: []string{"--", "git", "log", "--", "some/path"},
			want: []string{"git", "log", "--", "some/path"},
		},
		{
			name: "the separator may be left out for a flagless command",
			argv: []string{"make", "test"},
			want: []string{"make", "test"},
		},
		{
			name:    "nothing to run",
			argv:    nil,
			wantErr: true,
		},
		{
			name:    "a separator with nothing behind it",
			argv:    []string{"--title", "build", "--"},
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _, _, _ := parseExecFlags(t, tc.argv)

			got, err := execCommandArgs(cmd, cmd.Flags().Args())
			if tc.wantErr {
				if err == nil {
					t.Fatalf("command = %q, want an error naming the missing command", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("execCommandArgs(%q): %v", tc.argv, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("command = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExecFlagOverrides(t *testing.T) {
	ptr := func(s string) *string { return &s }

	for _, tc := range []struct {
		name string
		argv []string
		want runinpopup.PartialConfig
	}{
		{
			name: "flags left alone stay absent",
			argv: nil,
			want: runinpopup.PartialConfig{},
		},
		{
			name: "backend overlays",
			argv: []string{"--backend=zellij"},
			want: runinpopup.PartialConfig{Backend: ptr("zellij")},
		},
		{
			name: "an explicitly empty flag is a value, not an absence",
			argv: []string{"--backend="},
			want: runinpopup.PartialConfig{Backend: ptr("")},
		},
		{
			// The popup title belongs to one run, so it never reaches the config.
			name: "title feeds nothing",
			argv: []string{"--title", "build", "--", "make"},
			want: runinpopup.PartialConfig{},
		},
		{
			// Neither does where the popup sits or how big it is.
			name: "geometry feeds nothing",
			argv: []string{"--x", "C", "-w", "80%", "--", "make"},
			want: runinpopup.PartialConfig{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, backend, _, _ := parseExecFlags(t, tc.argv)

			got := execFlagOverrides(cmd, backend)
			assertStringPtr(t, "Backend", got.Backend, tc.want.Backend)
			if got.PinentryPath != nil {
				t.Errorf("PinentryPath = %q, want it absent: no exec flag feeds it",
					*got.PinentryPath)
			}
			if got.Timeouts != (runinpopup.PartialTimeoutsConfig{}) {
				t.Errorf("Timeouts = %+v, want the zero partial: no flag feeds it", got.Timeouts)
			}
		})
	}
}

// What the flags describe is the popup, so they reach the spec it is opened
// with — as typed, since only the library says what a geometry value means.
func TestExecSpec(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want runinpopup.PopupSpec
	}{
		{
			name: "flags left alone leave the backend its own",
			argv: []string{"--", "htop"},
			want: runinpopup.PopupSpec{Command: []string{"htop"}},
		},
		{
			name: "title",
			argv: []string{"--title", "build", "--", "make"},
			want: runinpopup.PopupSpec{Title: "build", Command: []string{"make"}},
		},
		{
			name: "the four geometry flags",
			argv: []string{"--x", "C", "--y", "10", "--width", "80%", "--height", "20", "--", "htop"},
			want: runinpopup.PopupSpec{
				X: "C", Y: "10", Width: "80%", Height: "20",
				Command: []string{"htop"},
			},
		},
		{
			name: "width takes the shorthand -h cannot",
			argv: []string{"-w", "80%", "--", "htop"},
			want: runinpopup.PopupSpec{Width: "80%", Command: []string{"htop"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _, title, geometry := parseExecFlags(t, tc.argv)

			command, err := execCommandArgs(cmd, cmd.Flags().Args())
			if err != nil {
				t.Fatalf("execCommandArgs(%q): %v", tc.argv, err)
			}
			got := execSpec(title, geometry, command)
			if got.Title != tc.want.Title {
				t.Errorf("Title = %q, want %q", got.Title, tc.want.Title)
			}
			gotGeometry := [4]string{got.X, got.Y, got.Width, got.Height}
			wantGeometry := [4]string{tc.want.X, tc.want.Y, tc.want.Width, tc.want.Height}
			if gotGeometry != wantGeometry {
				t.Errorf("geometry = %q, want %q", gotGeometry, wantGeometry)
			}
			if !slices.Equal(got.Command, tc.want.Command) {
				t.Errorf("Command = %q, want %q", got.Command, tc.want.Command)
			}
		})
	}
}

// -h belongs to --help, so --height goes without the shorthand its tmux flag
// would suggest; --width keeps the one that is free.
func TestExecCommand_geometryShorthands(t *testing.T) {
	root := rootCmd()

	cmd, _, err := root.Find([]string{"exec"})
	if err != nil {
		t.Fatalf("Find(exec): %v", err)
	}
	cmd.InitDefaultHelpFlag()

	if got := cmd.Flags().Lookup("width").Shorthand; got != "w" {
		t.Errorf("--width shorthand = %q, want %q", got, "w")
	}
	if got := cmd.Flags().Lookup("height").Shorthand; got != "" {
		t.Errorf("--height shorthand = %q, want none", got)
	}
	if got := cmd.Flags().ShorthandLookup("h"); got == nil || got.Name != "help" {
		t.Errorf("-h = %v, want it left to --help", got)
	}
}

// exec runs the user's command in the popup itself, so nothing internal stands
// behind it: every leaf the root carries is one a user is meant to type.
func TestExecCommandIsWired(t *testing.T) {
	root := rootCmd()

	cmd, _, err := root.Find([]string{"exec"})
	if err != nil || cmd.Name() != "exec" {
		t.Fatalf("Find(exec) = %v, %v; want the leaf itself", cmd.Name(), err)
	}
	for _, c := range root.Commands() {
		if c.Hidden {
			t.Errorf("%s is hidden, so it is a leaf nobody can be told about", c.Name())
		}
	}
}
