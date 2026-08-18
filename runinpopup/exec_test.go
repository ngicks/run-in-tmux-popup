package runinpopup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// execPayloadReexecEnv turns this test binary into the exec payload instead of a
// test runner. The exchange tests point the payload path at os.Args[0] and have
// their fake backend export the variable, so the round trip runs over the very
// argv the caller builds — asserted below — rather than over a hand-written stub.
const execPayloadReexecEnv = "RUNINPOPUP_TEST_EXEC_PAYLOAD"

func TestMain(m *testing.M) {
	if code, ok := runFakePinentry(); ok {
		os.Exit(code)
	}
	if os.Getenv(execPayloadReexecEnv) == "" {
		os.Exit(m.Run())
	}
	// The contract the caller promises the payload executable:
	//   <bin> exec-payload [--startup-timeout=<dur>] -- <command...>
	args := os.Args[1:]
	var opts ExecPayloadOptions
	if len(args) > 0 && args[0] == ExecPayloadCommandName {
		args = args[1:]
	}
	flagPrefix := "--" + ExecPayloadStartupTimeoutFlag + "="
	if len(args) > 0 && strings.HasPrefix(args[0], flagPrefix) {
		d, err := time.ParseDuration(strings.TrimPrefix(args[0], flagPrefix))
		if err != nil {
			fmt.Fprintf(os.Stderr, "bad %s: %v\n", ExecPayloadStartupTimeoutFlag, err)
			os.Exit(2)
		}
		opts.StartupTimeout = d
		args = args[1:]
	}
	if len(args) < 2 || args[0] != "--" || os.Args[1] != ExecPayloadCommandName {
		fmt.Fprintf(os.Stderr, "unexpected payload argv: %q\n", os.Args)
		os.Exit(2)
	}
	outcome, err := ExecPayload(context.Background(), args[1:], opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(outcome.Status)
}

// captureStd points os.Stdout and os.Stderr at files for the duration of the
// test: stdout is the result channel the caller reads, stderr is the popup
// terminal the command is watched on.
func captureStd(t *testing.T, dir string) func() (string, string) {
	t.Helper()
	open := func(name string, target **os.File) *os.File {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
		saved := *target
		*target = f
		t.Cleanup(func() { *target = saved; f.Close() })
		return f
	}
	outFile := open("stdout", &os.Stdout)
	errFile := open("stderr", &os.Stderr)
	return func() (string, string) {
		t.Helper()
		read := func(f *os.File) string {
			b, err := os.ReadFile(f.Name())
			if err != nil {
				t.Fatalf("reading %s: %v", f.Name(), err)
			}
			return string(b)
		}
		return read(outFile), read(errFile)
	}
}

// popupResultFifo points os.Stdout at a fifo drained in the background, the way
// the launch layer wires the payload's stdout. The returned func puts stdout
// back and hands over everything the caller read.
func popupResultFifo(t *testing.T) func() []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdout")
	if err := syscall.Mknod(path, syscall.S_IFIFO|0o600, 0); err != nil {
		t.Fatalf("mknod %q: %v", path, err)
	}

	type read struct {
		b   []byte
		err error
	}
	ch := make(chan read, 1)
	go func() {
		f, err := os.OpenFile(path, os.O_RDONLY, 0)
		if err != nil {
			ch <- read{err: err}
			return
		}
		defer f.Close()
		b, err := io.ReadAll(f)
		ch <- read{b: b, err: err}
	}()

	// This open blocks until the reader above arrives: that block is the
	// rendezvous the popup's command line makes on the payload's behalf.
	w, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening fifo %q: %v", path, err)
	}
	saved := os.Stdout
	os.Stdout = w
	restore := sync.OnceFunc(func() {
		os.Stdout = saved
		w.Close()
	})
	t.Cleanup(restore)

	return func() []byte {
		t.Helper()
		// The reader only reaches end-of-file once this end is closed for good.
		restore()
		r := <-ch
		if r.err != nil {
			t.Fatalf("reading fifo %q: %v", path, r.err)
		}
		return r.b
	}
}

func decodeExecResult(t *testing.T, b []byte) ExecResult {
	t.Helper()
	var result ExecResult
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("decoding the reported result from %q: %v", b, err)
	}
	return result
}

func TestExecPayload(t *testing.T) {
	dir := t.TempDir()
	std := captureStd(t, dir)

	argv := []string{"sh", "-c", "printf 'out\nput'; printf 'err\nor' >&2; exit 3"}
	outcome, err := ExecPayload(t.Context(), argv, ExecPayloadOptions{})
	if err != nil {
		t.Fatalf("ExecPayload: %v", err)
	}

	reportedJSON, watched := std()
	reported := decodeExecResult(t, []byte(reportedJSON))
	if !slices.Equal(reported.Command, argv) {
		t.Errorf("command = %q, want %q", reported.Command, argv)
	}
	if reported.ExitCode != 3 {
		t.Errorf("exit_code = %d, want the command's own status 3", reported.ExitCode)
	}
	if reported.Stdout != "out\nput" || reported.Stderr != "err\nor" {
		t.Errorf("stdout = %q, stderr = %q; both must be captured whole",
			reported.Stdout, reported.Stderr)
	}
	if reported.Error != "" {
		t.Errorf("error = %q, want it empty: the command ran", reported.Error)
	}
	if !reflect.DeepEqual(reported, outcome.Result) {
		t.Errorf("returned %+v but reported %+v; they must agree", outcome.Result, reported)
	}
	if !outcome.Ran || outcome.Status != 3 {
		t.Errorf("outcome = %+v, want Ran with the command's status 3", outcome)
	}

	// The command is watched, not swallowed: it draws on the popup terminal too,
	// which this half's stdout no longer is. Both of its streams arrive there
	// through relays of their own, so only their presence is pinned.
	if !strings.Contains(watched, "out\nput") || !strings.Contains(watched, "err\nor") {
		t.Errorf("the popup terminal saw %q, want the command's own output on it", watched)
	}
}

func TestExecPayload_commandThatCannotStart(t *testing.T) {
	dir := t.TempDir()
	std := captureStd(t, dir)

	outcome, err := ExecPayload(
		t.Context(),
		[]string{filepath.Join(dir, "nope")},
		ExecPayloadOptions{},
	)
	if err != nil {
		t.Fatalf("ExecPayload: %v", err)
	}

	reportedJSON, _ := std()
	reported := decodeExecResult(t, []byte(reportedJSON))
	// A result must still reach the caller: it is blocked on the fifo and has no
	// other way to learn the run is over.
	if reported.ExitCode != -1 || reported.Error == "" {
		t.Errorf("result = %+v, want exit_code -1 and a filled error", reported)
	}
	if !outcome.Ran || outcome.Status != 1 {
		t.Errorf("outcome = %+v, want Ran with status 1: -1 is not an exit status", outcome)
	}
}

func TestExecPayload_noCommand(t *testing.T) {
	std := captureStd(t, t.TempDir())

	outcome, err := ExecPayload(t.Context(), nil, ExecPayloadOptions{})
	if err != nil {
		t.Fatalf("ExecPayload: %v", err)
	}

	reportedJSON, _ := std()
	reported := decodeExecResult(t, []byte(reportedJSON))
	if reported.ExitCode != -1 || reported.Error == "" {
		t.Errorf("result = %+v, want exit_code -1 and a filled error", reported)
	}
	if outcome.Status != 1 {
		t.Errorf("status = %d, want 1", outcome.Status)
	}
}

// Ctrl-C in the popup reaches the whole foreground process group: the command
// dies and this payload's own context is Done before the result is written. The
// run is over either way, so cancellation must not be allowed to eat the report
// — least of all a report too big for the pipe buffer, which can only land by
// blocking until the caller drains it.
func TestExecPayload_reportsEvenWhenTheRunWasCanceled(t *testing.T) {
	_ = captureStd(t, t.TempDir())
	reported := popupResultFifo(t)

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	outcome, err := ExecPayload(ctx, []string{"sh", "-c", "seq 1 40000; sleep 5"},
		ExecPayloadOptions{})
	if err != nil {
		t.Fatalf("ExecPayload: %v", err)
	}

	result := decodeExecResult(t, reported())
	if len(result.Stdout) <= 64*1024 {
		t.Fatalf("stdout is %d bytes, too small to outgrow the pipe buffer",
			len(result.Stdout))
	}
	if !reflect.DeepEqual(result, outcome.Result) {
		t.Error("the reported result does not match the one returned")
	}
}

// The caller made the rendezvous and let go again — waking its own blocked open
// is how it gives up — so this half finds a result channel with nobody behind
// it. The command must not run: nothing could ever be reported about it.
func TestExecPayload_callerAlreadyLetGo(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	r.Close()
	saved := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = saved; w.Close() })

	marker := filepath.Join(t.TempDir(), "ran")
	outcome, err := ExecPayload(t.Context(), []string{"touch", marker}, ExecPayloadOptions{})

	if err == nil {
		t.Fatal("a result channel nobody holds must not be reported on")
	}
	if outcome.Ran {
		t.Error("the command must not run when its result can never be reported")
	}
	if outcome.Status != 1 {
		t.Errorf("status = %d, want 1", outcome.Status)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the command ran anyway")
	}
}

// A signal death is -1 in the JSON, which no exit(2) can carry; the popup
// process reports it the way a shell does instead.
func TestRunExecCommand_signalDeathBecomes128PlusSignal(t *testing.T) {
	std := captureStd(t, t.TempDir())
	defer std()

	result, status := runExecCommand(t.Context(), []string{"sh", "-c", "kill -TERM $$"})

	if result.ExitCode != -1 {
		t.Errorf("exit_code = %d, want the raw -1 in the reported result", result.ExitCode)
	}
	if status != 128+int(syscall.SIGTERM) {
		t.Errorf("status = %d, want %d", status, 128+int(syscall.SIGTERM))
	}
}

// execBackend is shellBackend wired to re-exec the test binary as the payload.
func execBackend() *shellBackend {
	return &shellBackend{environ: []string{execPayloadReexecEnv + "=1"}}
}

// stubPayload writes an executable stand-in for the payload. Its stdout is the
// caller's result channel, opened for it by the popup's command line, so a stub
// needs nothing but a body: the argv it is handed is the payload contract's, and
// the round-trip tests are what pin that.
func stubPayload(t *testing.T, dir, script string) string {
	t.Helper()
	path := filepath.Join(dir, "payload")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatalf("writing the stub payload: %v", err)
	}
	return path
}

// execExchange is the caller half of the exec exchange, built the way the CLI
// builds it: the command travels in the payload's argv, nothing is streamed, and
// the result comes back on the payload's stdout.
func execExchange(
	backend Backend,
	payloadPath string,
	startupTimeout time.Duration,
) *JsonIpcLauncher[[]string, ExecResult] {
	return &JsonIpcLauncher[[]string, ExecResult]{
		Popup: &PopupLauncher{Backend: backend, StartupTimeout: startupTimeout},
		AddPayload: func(command []string, spec PopupSpec) PopupSpec {
			argv := []string{payloadPath, ExecPayloadCommandName}
			if startupTimeout != 0 {
				argv = append(argv, fmt.Sprintf(
					"--%s=%s", ExecPayloadStartupTimeoutFlag, startupTimeout,
				))
			}
			spec.Command = slices.Concat(argv, []string{"--"}, command)
			return spec
		},
	}
}

// runExecExchange runs one exchange to its end the way the CLI does: one result,
// then whatever the exchange itself has to report.
func runExecExchange(
	ctx context.Context,
	launcher *JsonIpcLauncher[[]string, ExecResult],
	command []string,
) (result ExecResult, reported bool, err error) {
	conn, err := launcher.Exec(ctx, command)
	if err != nil {
		return ExecResult{}, false, err
	}
	result, reported = <-conn.Results()
	for range conn.Results() {
	}
	return result, reported, conn.Wait()
}

func TestExecExchange(t *testing.T) {
	backend := execBackend()

	argv := []string{"sh", "-c", "printf 'out\nput'; printf 'err\nor' >&2; exit 3"}
	result, reported, err := runExecExchange(
		t.Context(), execExchange(backend, os.Args[0], 0), argv,
	)
	// A command that fails is not a transport failure: the launcher exiting
	// non-zero — which is what "sh -c" does here, and what tmux display-popup
	// does in the real thing — must not be reported as one.
	if err != nil {
		t.Fatalf("exec exchange: %v", err)
	}
	if !reported {
		t.Fatal("no result was reported")
	}
	if !slices.Equal(result.Command, argv) {
		t.Errorf("command = %q, want %q", result.Command, argv)
	}
	if result.ExitCode != 3 {
		t.Errorf("exit_code = %d, want 3", result.ExitCode)
	}
	if result.Stdout != "out\nput" || result.Stderr != "err\nor" {
		t.Errorf("stdout = %q, stderr = %q", result.Stdout, result.Stderr)
	}
	if backend.prepared != 1 || backend.restored != 1 {
		t.Errorf("prepared = %d, restored = %d, want 1 each", backend.prepared, backend.restored)
	}
}

// The two halves have to give up at the same time, so a caller that chose its
// own bound has to get it across — and the only channel it has is the argv. The
// re-exec payload rejects an argv it did not expect, so a round trip with a
// non-default bound proves both halves agree on the shape.
func TestExecExchange_startupTimeoutReachesThePayload(t *testing.T) {
	result, reported, err := runExecExchange(
		t.Context(),
		execExchange(execBackend(), os.Args[0], 7*time.Second),
		[]string{"true"},
	)
	if err != nil {
		t.Fatalf("exec exchange: %v", err)
	}
	if !reported || result.ExitCode != 0 {
		t.Errorf("result = %+v (reported %v), want exit_code 0", result, reported)
	}
}

// The result carries whatever the command printed, so it outgrows both a pipe
// buffer and bufio.Scanner's line limit.
func TestExecExchange_resultBiggerThanAPipeBuffer(t *testing.T) {
	result, _, err := runExecExchange(
		t.Context(),
		execExchange(execBackend(), os.Args[0], 0),
		[]string{"sh", "-c", "seq 1 40000"},
	)
	if err != nil {
		t.Fatalf("exec exchange: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit_code = %d, want 0", result.ExitCode)
	}
	lines := strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n")
	if len(lines) != 40000 || lines[39999] != "40000" {
		t.Errorf("got %d lines ending %q, want 40000 ending \"40000\"",
			len(lines), lines[len(lines)-1])
	}
	if len(result.Stdout) <= 64*1024 {
		t.Errorf("stdout is %d bytes, too small to prove the transport is unbounded",
			len(result.Stdout))
	}
}

// The loud way a popup can fail to appear: the payload is not there to run, so
// the popup's own shell reports it and dies. The result channel it opened on the
// payload's behalf ends with nothing in it, and only the launcher can say why.
func TestExecExchange_launcherFailsWithoutAPayload(t *testing.T) {
	backend := execBackend()
	backend.environ = nil // the re-exec never happens, so no result is written

	_, reported, err := runExecExchange(
		t.Context(),
		execExchange(backend, "/nonexistent/run-in-popup", 0),
		[]string{"true"},
	)
	if reported {
		t.Error("a payload that never ran cannot have reported a result")
	}
	if err == nil || !strings.Contains(err.Error(), "popup failed") {
		t.Fatalf("err = %v, want the launcher failure surfaced", err)
	}
}

// The quiet way: the popup never reaches its payload at all, so nothing ever
// opens the payload's end of the result channel. Only the startup bound can end
// this one.
func TestExecExchange_popupNeverReachesThePayload(t *testing.T) {
	start := time.Now()
	_, _, err := runExecExchange(
		t.Context(),
		execExchange(&stalledBackend{shellBackend: execBackend()}, os.Args[0],
			300*time.Millisecond),
		[]string{"true"},
	)
	elapsed := time.Since(start)

	if err == nil || !strings.Contains(err.Error(), "did not reach its payload") {
		t.Fatalf("err = %v, want the startup bound to end the wait", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("the exchange took %v to give up", elapsed)
	}
}

// Once the payload has connected there is no timer left, so its death has to be
// the thing that ends the wait: it holds the only write end, and closing it is
// an end-of-file with nothing in it.
func TestExecExchange_payloadReportsNothing(t *testing.T) {
	dir := t.TempDir()

	start := time.Now()
	result, reported, err := runExecExchange(
		t.Context(),
		// Connected by the popup's command line, then gone without a word.
		execExchange(execBackend(), stubPayload(t, dir, "exit 0\n"), 10*time.Second),
		[]string{"true"},
	)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("err = %v; nothing failed here, the payload simply said nothing", err)
	}
	if reported {
		t.Errorf("result = %+v, want the stream to end without one", result)
	}
	if elapsed > 5*time.Second {
		t.Errorf("the exchange took %v to notice, want the end-of-file to be immediate",
			elapsed)
	}
}

// A launcher that returns as soon as the pane exists — what the floating-pane
// mechanisms do — says nothing about the payload, which keeps running and
// answering after it.
func TestExecExchange_launcherExitsWhileThePayloadRuns(t *testing.T) {
	result, reported, err := runExecExchange(
		t.Context(),
		execExchange(&detachedBackend{shellBackend: execBackend()}, os.Args[0], 0),
		[]string{"sh", "-c", "sleep 0.2; exit 5"},
	)
	if err != nil {
		t.Fatalf("exec exchange: %v", err)
	}
	if !reported || result.ExitCode != 5 {
		t.Errorf("result = %+v (reported %v), want the command's own status 5",
			result, reported)
	}
}

// Cancelling before the rendezvous: opening the result channel is the blocking
// step, and nothing but ctx can unblock it.
func TestExecExchange_canceledBeforeRendezvous(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := runExecExchange(
		ctx,
		execExchange(&stalledBackend{shellBackend: execBackend()}, os.Args[0],
			10*time.Second),
		[]string{"true"},
	)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a canceled context must abort the rendezvous")
	}
	if elapsed > 2*time.Second {
		t.Errorf("the exchange took %v to notice the cancellation", elapsed)
	}
}

// Cancelling after it: the command may run for hours, so cutting the read short
// is the only way out.
func TestExecExchange_canceledWhileTheCommandRuns(t *testing.T) {
	dir := t.TempDir()

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := runExecExchange(
		ctx,
		// Connected by the popup's command line, then busy for far longer than
		// the caller is willing to wait.
		execExchange(execBackend(), stubPayload(t, dir, "sleep 5\n"), 10*time.Second),
		[]string{"true"},
	)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a canceled context must end the wait for the result")
	}
	if elapsed > 2*time.Second {
		t.Errorf("the exchange took %v to notice the cancellation", elapsed)
	}
}
