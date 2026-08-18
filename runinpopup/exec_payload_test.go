package runinpopup

import (
	"context"
	"encoding/json"
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
