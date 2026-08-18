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
	"syscall"
	"testing"
	"time"
)

// execPayloadReexecEnv turns this test binary into the exec payload instead of a
// test runner. TestCallExec points ExecOptions.PayloadPath at os.Args[0] and has
// its fake backend export the variable, so the round trip runs over the very
// argv CallExec builds — asserted below — rather than over a hand-written stub.
const execPayloadReexecEnv = "RUNINPOPUP_TEST_EXEC_PAYLOAD"

func TestMain(m *testing.M) {
	if code, ok := runFakePinentry(); ok {
		os.Exit(code)
	}
	if os.Getenv(execPayloadReexecEnv) == "" {
		os.Exit(m.Run())
	}
	// The contract CallExec promises the payload executable:
	//   <bin> exec-payload [--startup-timeout=<dur>] <result-fifo> -- <command...>
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
	if len(args) < 3 || args[1] != "--" || os.Args[1] != ExecPayloadCommandName {
		fmt.Fprintf(os.Stderr, "unexpected payload argv: %q\n", os.Args)
		os.Exit(2)
	}
	outcome, err := ExecPayload(context.Background(), args[0], args[2:], opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(outcome.Status)
}

func mkfifo(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "result")
	if err := syscall.Mknod(path, syscall.S_IFIFO|0o600, 0); err != nil {
		t.Fatalf("mknod %q: %v", path, err)
	}
	return path
}

// readFifo drains path to EOF in the background, the way CallExec does. Opening
// read-only blocks until the payload opens its end, so the read has to start
// before ExecPayload is called — that block *is* the rendezvous.
func readFifo(t *testing.T, path string) func() []byte {
	t.Helper()
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
	return func() []byte {
		t.Helper()
		r := <-ch
		if r.err != nil {
			t.Fatalf("reading fifo %q: %v", path, r.err)
		}
		return r.b
	}
}

// captureStd points os.Stdout and os.Stderr at files for the duration of the
// test, so the payload's live tee can be asserted instead of scribbled over go
// test's own output.
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

func TestExecPayload(t *testing.T) {
	dir := t.TempDir()
	fifo := mkfifo(t, dir)
	wait := readFifo(t, fifo)
	std := captureStd(t, dir)

	argv := []string{"sh", "-c", "printf 'out\nput'; printf 'err\nor' >&2; exit 3"}
	outcome, err := ExecPayload(t.Context(), fifo, argv, ExecPayloadOptions{})
	if err != nil {
		t.Fatalf("ExecPayload: %v", err)
	}

	var reported ExecResult
	if err := json.Unmarshal(wait(), &reported); err != nil {
		t.Fatalf("decoding the reported result: %v", err)
	}
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

	// The command is watched, not swallowed: it draws on the popup terminal too.
	if gotOut, gotErr := std(); gotOut != "out\nput" || gotErr != "err\nor" {
		t.Errorf("live stdout = %q, stderr = %q; the popup must see them as well",
			gotOut, gotErr)
	}
}

func TestExecPayload_commandThatCannotStart(t *testing.T) {
	dir := t.TempDir()
	fifo := mkfifo(t, dir)
	wait := readFifo(t, fifo)

	outcome, err := ExecPayload(
		t.Context(),
		fifo,
		[]string{filepath.Join(dir, "nope")},
		ExecPayloadOptions{},
	)
	if err != nil {
		t.Fatalf("ExecPayload: %v", err)
	}

	var reported ExecResult
	if err := json.Unmarshal(wait(), &reported); err != nil {
		t.Fatalf("decoding the reported result: %v", err)
	}
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
	dir := t.TempDir()
	fifo := mkfifo(t, dir)
	wait := readFifo(t, fifo)

	outcome, err := ExecPayload(t.Context(), fifo, nil, ExecPayloadOptions{})
	if err != nil {
		t.Fatalf("ExecPayload: %v", err)
	}
	var reported ExecResult
	if err := json.Unmarshal(wait(), &reported); err != nil {
		t.Fatalf("decoding the reported result: %v", err)
	}
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
	dir := t.TempDir()
	fifo := mkfifo(t, dir)
	wait := readFifo(t, fifo)
	_ = captureStd(t, dir)

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	outcome, err := ExecPayload(ctx, fifo, []string{"sh", "-c", "seq 1 40000; sleep 5"},
		ExecPayloadOptions{})
	if err != nil {
		t.Fatalf("ExecPayload: %v", err)
	}

	var reported ExecResult
	if err := json.Unmarshal(wait(), &reported); err != nil {
		t.Fatalf("decoding the reported result: %v", err)
	}
	if len(reported.Stdout) <= 64*1024 {
		t.Fatalf("stdout is %d bytes, too small to outgrow the pipe buffer",
			len(reported.Stdout))
	}
	if !reflect.DeepEqual(reported, outcome.Result) {
		t.Error("the reported result does not match the one returned")
	}
}

// Nobody ever reads the fifo, so the payload must give up instead of wedging in
// open(2) — and must not run the command it could never report on.
func TestExecPayload_callerNeverArrives(t *testing.T) {
	dir := t.TempDir()
	fifo := mkfifo(t, dir)
	marker := filepath.Join(dir, "ran")

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	outcome, err := ExecPayload(ctx, fifo, []string{"touch", marker}, ExecPayloadOptions{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a fifo nobody reads must not be waited on forever")
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
	if elapsed > 5*time.Second {
		t.Errorf("ExecPayload took %v to give up", elapsed)
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

// stubPayload writes an executable stand-in for the payload. Its preamble walks
// the argv CallExec builds — the optional startup bound included — and leaves
// the result fifo in $fifo for script to use.
func stubPayload(t *testing.T, dir, script string) string {
	t.Helper()
	const preamble = `#!/bin/sh
shift
case "$1" in --` + ExecPayloadStartupTimeoutFlag + `=*) shift ;; esac
fifo=$1
`
	path := filepath.Join(dir, "payload")
	if err := os.WriteFile(path, []byte(preamble+script), 0o700); err != nil {
		t.Fatalf("writing the stub payload: %v", err)
	}
	return path
}

func TestCallExec(t *testing.T) {
	backend := execBackend()

	argv := []string{"sh", "-c", "printf 'out\nput'; printf 'err\nor' >&2; exit 3"}
	result, err := CallExec(t.Context(), backend, ExecOptions{
		TempDir:     t.TempDir(),
		PayloadPath: os.Args[0],
		Command:     argv,
	})
	// A command that fails is not a transport failure: the launcher exiting
	// non-zero — which is what "sh -c" does here, and what tmux display-popup
	// does in the real thing — must not be reported as one.
	if err != nil {
		t.Fatalf("CallExec: %v", err)
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
// own bound has to get it across — and the only channel it has is the argv.
func TestExecPayloadArgv(t *testing.T) {
	base := ExecOptions{PayloadPath: "/bin/run-in-popup"}

	def := execPayloadArgv(
		ExecOptions{PayloadPath: base.PayloadPath, StartupTimeout: defaultExecStartupTimeout},
		"/tmp/x/result",
	)
	want := []string{"/bin/run-in-popup", ExecPayloadCommandName, "/tmp/x/result", "--"}
	if !slices.Equal(def, want) {
		t.Errorf("argv = %q, want %q: the default bound needs no flag", def, want)
	}

	custom := execPayloadArgv(
		ExecOptions{PayloadPath: base.PayloadPath, StartupTimeout: 5 * time.Second},
		"/tmp/x/result",
	)
	want = []string{
		"/bin/run-in-popup", ExecPayloadCommandName,
		"--" + ExecPayloadStartupTimeoutFlag + "=5s",
		"/tmp/x/result", "--",
	}
	if !slices.Equal(custom, want) {
		t.Errorf("argv = %q, want %q", custom, want)
	}
}

// And the flag has to be one the payload accepts: the re-exec payload rejects an
// argv it did not expect, so a round trip with a non-default bound proves both
// halves agree on the shape.
func TestCallExec_startupTimeoutReachesThePayload(t *testing.T) {
	result, err := CallExec(t.Context(), execBackend(), ExecOptions{
		TempDir:        t.TempDir(),
		PayloadPath:    os.Args[0],
		Command:        []string{"true"},
		StartupTimeout: 7 * time.Second,
	})
	if err != nil {
		t.Fatalf("CallExec: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", result.ExitCode)
	}
}

// The result carries whatever the command printed, so it outgrows both a pipe
// buffer and bufio.Scanner's line limit.
func TestCallExec_resultBiggerThanAPipeBuffer(t *testing.T) {
	result, err := CallExec(t.Context(), execBackend(), ExecOptions{
		TempDir:     t.TempDir(),
		PayloadPath: os.Args[0],
		Command:     []string{"sh", "-c", "seq 1 40000"},
	})
	if err != nil {
		t.Fatalf("CallExec: %v", err)
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

// The launcher failing is the loud way a popup can fail to appear.
func TestCallExec_launcherFails(t *testing.T) {
	backend := execBackend()
	backend.environ = nil // the re-exec never happens, so no result is written

	_, err := CallExec(t.Context(), backend, ExecOptions{
		TempDir:     t.TempDir(),
		PayloadPath: "/nonexistent/run-in-popup",
		Command:     []string{"true"},
	})
	if err == nil || !strings.Contains(err.Error(), "popup failed") {
		t.Fatalf("err = %v, want the launcher failure surfaced", err)
	}
}

// And the quiet way: the launcher exits 0 — which the floating-pane backends do
// while the payload still runs, so it proves nothing — and the payload never
// shows up. Only the startup bound can end this one.
func TestCallExec_launcherSucceedsButPayloadNeverConnects(t *testing.T) {
	dir := t.TempDir()

	start := time.Now()
	_, err := CallExec(t.Context(), execBackend(), ExecOptions{
		TempDir:        dir,
		PayloadPath:    stubPayload(t, dir, "exit 0\n"),
		Command:        []string{"true"},
		StartupTimeout: 300 * time.Millisecond,
	})
	elapsed := time.Since(start)

	if err == nil || !strings.Contains(err.Error(), "did not reach the exec payload") {
		t.Fatalf("err = %v, want the startup bound to end the wait", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("CallExec took %v to give up", elapsed)
	}
}

// Once the payload has connected there is no timer left, so its death has to be
// the thing that ends the wait: it holds the only write end, and closing it is
// an EOF with nothing in it.
func TestCallExec_payloadDiesAfterConnecting(t *testing.T) {
	dir := t.TempDir()

	start := time.Now()
	_, err := CallExec(t.Context(), execBackend(), ExecOptions{
		TempDir: dir,
		// Opens its end of the fifo — completing the rendezvous — then dies
		// without writing anything.
		PayloadPath:    stubPayload(t, dir, `exec 3>"$fifo"`+"\nexit 0\n"),
		Command:        []string{"true"},
		StartupTimeout: 10 * time.Second,
	})
	elapsed := time.Since(start)

	if err == nil || !strings.Contains(err.Error(), "without reporting a result") {
		t.Fatalf("err = %v, want the payload's death reported", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("CallExec took %v to notice, want the EOF to be immediate", elapsed)
	}
}

// Cancelling before the rendezvous: the open is the blocking step, and nothing
// but ctx can unblock it.
func TestCallExec_canceledBeforeRendezvous(t *testing.T) {
	dir := t.TempDir()

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := CallExec(ctx, execBackend(), ExecOptions{
		TempDir:        dir,
		PayloadPath:    stubPayload(t, dir, "sleep 5\n"),
		Command:        []string{"true"},
		StartupTimeout: 10 * time.Second,
	})
	elapsed := time.Since(start)

	// Only the timing is the invariant: the cancellation reaches the blocked open
	// and the launcher's own watchdog at once, so which of the two reports it is
	// a race.
	if err == nil {
		t.Fatal("a canceled context must abort the rendezvous")
	}
	if elapsed > 2*time.Second {
		t.Errorf("CallExec took %v to notice the cancellation", elapsed)
	}
}

// Cancelling after it: the command may run for hours, so the read deadline is
// the only way out.
func TestCallExec_canceledWhileTheCommandRuns(t *testing.T) {
	dir := t.TempDir()

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := CallExec(ctx, execBackend(), ExecOptions{
		TempDir:        dir,
		PayloadPath:    stubPayload(t, dir, `exec 3>"$fifo"`+"\nsleep 5\n"),
		Command:        []string{"true"},
		StartupTimeout: 10 * time.Second,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a canceled context must end the wait for the result")
	}
	if elapsed > 2*time.Second {
		t.Errorf("CallExec took %v to notice the cancellation", elapsed)
	}
}

func TestCallExec_optionsAreValidated(t *testing.T) {
	if _, err := CallExec(t.Context(), execBackend(), ExecOptions{
		Command: []string{"true"},
	}); err == nil {
		t.Error("an unset TempDir must be rejected")
	}
	if _, err := CallExec(t.Context(), execBackend(), ExecOptions{
		TempDir: t.TempDir(),
	}); err == nil {
		t.Error("an empty Command must be rejected")
	}
}
