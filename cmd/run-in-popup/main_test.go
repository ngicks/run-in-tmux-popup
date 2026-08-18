package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
			reported := popupStdout(t)

			withArgs(t, "exec-payload", "--", "sh", "-c", tc.script)
			err := commands.Execute(context.Background())

			code, report := exitStatus(err)
			if code != tc.wantCode {
				t.Errorf("exit status = %d, want %d (err = %v)", code, tc.wantCode, err)
			}
			if report != tc.wantReport {
				t.Errorf("report = %v, want %v: a bare status has nothing to add",
					report, tc.wantReport)
			}
			if got := reported(); !bytes.Contains(got, []byte(tc.wantJSON)) {
				t.Errorf("reported %s, want it to contain %s", got, tc.wantJSON)
			}
		})
	}
}

// Nobody holds the other end of the result channel, so the payload never gets to
// run the command: no status to carry, and the failure has to be printed like
// any other.
func TestExitStatus_execPayloadWithNoCaller(t *testing.T) {
	abandonedStdout(t)
	withArgs(t, "exec-payload", "--", "true")

	err := commands.Execute(context.Background())
	if err == nil {
		t.Fatal("a payload with nobody listening must fail")
	}
	if code, report := exitStatus(err); code != 1 || !report {
		t.Errorf("exitStatus = %d, %v; want 1, true", code, report)
	}
}

// popupStdout stands in for the fifo exec wires the payload's stdout to, and
// hands over what the caller would have read.
func popupStdout(t *testing.T) func() []byte {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "stdout"))
	if err != nil {
		t.Fatalf("creating the stdout stand-in: %v", err)
	}
	saved := os.Stdout
	os.Stdout = f
	t.Cleanup(func() { os.Stdout = saved; f.Close() })

	return func() []byte {
		t.Helper()
		b, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", f.Name(), err)
		}
		return b
	}
}

// abandonedStdout points os.Stdout at a pipe nobody holds the other end of: the
// caller opened the popup and gave up on it.
func abandonedStdout(t *testing.T) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	r.Close()
	saved := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = saved; w.Close() })
}

func withArgs(t *testing.T, args ...string) {
	t.Helper()
	saved := os.Args
	t.Cleanup(func() { os.Args = saved })
	os.Args = append([]string{"run-in-popup"}, args...)
}
