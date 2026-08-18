package runinpopup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

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
