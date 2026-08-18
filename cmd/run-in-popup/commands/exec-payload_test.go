package commands

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// popupStdout points os.Stdout at a file for the duration of the test: the
// payload reports on its own stdout, which exec has wired to a FIFO of its own.
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

// abandonedStdout points os.Stdout at a pipe nobody holds the other end of,
// which is what a caller that gave up on the exchange leaves behind.
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

func payloadCmd(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{Use: "exec-payload"}
	cmd.SetContext(ctx)
	return cmd
}

// The command ran, so its status is the leaf's own: an exit-code error, bare,
// because the command already wrote whatever it had on the popup terminal.
func TestRunExecPayload_carriesTheCommandStatus(t *testing.T) {
	reported := popupStdout(t)

	err := runExecPayload(payloadCmd(t.Context()), []string{"sh", "-c", "exit 5"}, 0)

	coded, ok := errors.AsType[*exitCodeError](err)
	if !ok {
		t.Fatalf("err = %v, want the command's status carried", err)
	}
	if coded.ExitCode() != 5 {
		t.Errorf("ExitCode() = %d, want 5", coded.ExitCode())
	}
	if coded.Unwrap() != nil {
		t.Errorf("Unwrap() = %v, want nil: a successful report has nothing to print",
			coded.Unwrap())
	}
	if got := reported(); !bytes.Contains(got, []byte(`"exit_code":5`)) {
		t.Errorf("reported %s, want the status in the JSON too", got)
	}
}

// Nothing is holding the result channel and the run is already over, so the
// command never runs: there is no status to stand in for, and the failure is
// reported plainly.
func TestRunExecPayload_withNoCallerListening(t *testing.T) {
	abandonedStdout(t)
	marker := filepath.Join(t.TempDir(), "ran")

	err := runExecPayload(payloadCmd(t.Context()), []string{"touch", marker}, 0)

	if err == nil {
		t.Fatal("a payload with nobody listening must fail")
	}
	if _, ok := errors.AsType[*exitCodeError](err); ok {
		t.Error("nothing ran, so no status may be carried")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the command ran anyway")
	}
}
