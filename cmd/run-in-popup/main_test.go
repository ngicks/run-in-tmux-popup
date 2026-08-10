package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/ngicks/run-in-tmux-popup/cmd/run-in-popup/commands"
)

func TestExitStatus_ordinaryErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "plain", err: errors.New("boom")},
		{name: "wrapped", err: fmt.Errorf("context: %w", errors.New("boom"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, report := exitStatus(tc.err)
			if code != 1 || !report {
				t.Errorf("exitStatus = %d, %v; want 1, true", code, report)
			}
		})
	}
}

// The status the exec payload asks for has to survive the whole path — cobra's
// RunE, the error commands.Execute hands back, and the resolution here — so this
// drives that path instead of a stand-in error, and would break if either side
// changed the shape it agrees on.
func TestExitStatus_fromTheExecPayloadLeaf(t *testing.T) {
	for _, tc := range []struct {
		name       string
		script     string
		wantCode   int
		wantReport bool
		wantJSON   string
	}{
		{
			name:     "a non-zero command is carried, not reported",
			script:   "exit 7",
			wantCode: 7,
			wantJSON: `"exit_code":7`,
		},
		{
			name:     "so is success",
			script:   "exit 0",
			wantCode: 0,
			wantJSON: `"exit_code":0`,
		},
		{
			// -1 is what the JSON says; 128+SIGTERM is what a process can exit with.
			name:     "a signal death becomes a shell status",
			script:   "kill -TERM $$",
			wantCode: 128 + int(syscall.SIGTERM),
			wantJSON: `"exit_code":-1`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fifo := mkfifo(t)
			drained := drain(t, fifo)

			withArgs(t, "exec-payload", fifo, "--", "sh", "-c", tc.script)
			err := commands.Execute(context.Background())

			code, report := exitStatus(err)
			if code != tc.wantCode {
				t.Errorf("exit status = %d, want %d (err = %v)", code, tc.wantCode, err)
			}
			if report != tc.wantReport {
				t.Errorf("report = %v, want %v: a bare status has nothing to add",
					report, tc.wantReport)
			}
			if got := drained(); !bytes.Contains(got, []byte(tc.wantJSON)) {
				t.Errorf("reported %s, want it to contain %s", got, tc.wantJSON)
			}
		})
	}
}

// Nothing is reading the fifo and the run is already cancelled, so the payload
// never gets to run the command: no status to carry, and the failure has to be
// printed like any other.
func TestExitStatus_execPayloadWithNoCaller(t *testing.T) {
	withArgs(t, "exec-payload", mkfifo(t), "--", "true")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := commands.Execute(ctx)
	if err == nil {
		t.Fatal("a payload with nobody listening must fail")
	}
	if code, report := exitStatus(err); code != 1 || !report {
		t.Errorf("exitStatus = %d, %v; want 1, true", code, report)
	}
}

func mkfifo(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "result")
	if err := syscall.Mknod(path, syscall.S_IFIFO|0o600, 0); err != nil {
		t.Fatalf("mknod %q: %v", path, err)
	}
	return path
}

// drain stands in for the process that opened the popup: the payload waits for
// this end before it runs anything.
func drain(t *testing.T, path string) func() []byte {
	t.Helper()
	ch := make(chan []byte, 1)
	go func() {
		f, err := os.OpenFile(path, os.O_RDONLY, 0)
		if err != nil {
			ch <- nil
			return
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		ch <- b
	}()
	return func() []byte {
		t.Helper()
		return <-ch
	}
}

func withArgs(t *testing.T, args ...string) {
	t.Helper()
	saved := os.Args
	t.Cleanup(func() { os.Args = saved })
	os.Args = append([]string{"run-in-popup"}, args...)
}
