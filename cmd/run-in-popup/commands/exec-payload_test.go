package commands

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// payloadFifo makes the fifo and stands in for the process that opened the
// popup: the payload waits for this read end before it runs anything.
func payloadFifo(t *testing.T) (path string, drained func() []byte) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "result")
	if err := syscall.Mknod(path, syscall.S_IFIFO|0o600, 0); err != nil {
		t.Fatalf("mknod %q: %v", path, err)
	}
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
	return path, func() []byte {
		t.Helper()
		return <-ch
	}
}

func payloadCmd(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{Use: "exec-payload"}
	cmd.SetContext(ctx)
	return cmd
}

// The command ran, so its status is the leaf's own: an exit-code error, bare,
// because the command already wrote whatever it had on the popup terminal.
func TestRunExecPayload_carriesTheCommandStatus(t *testing.T) {
	fifo, drained := payloadFifo(t)

	err := runExecPayload(
		payloadCmd(t.Context()),
		[]string{fifo, "sh", "-c", "exit 5"},
		0,
	)

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
	if got := drained(); !bytes.Contains(got, []byte(`"exit_code":5`)) {
		t.Errorf("reported %s, want the status in the JSON too", got)
	}
}

// Nothing is reading and the run is already over, so the command never runs:
// there is no status to stand in for, and the failure is reported plainly.
func TestRunExecPayload_withNoCallerListening(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "result")
	if err := syscall.Mknod(fifo, syscall.S_IFIFO|0o600, 0); err != nil {
		t.Fatalf("mknod %q: %v", fifo, err)
	}
	marker := filepath.Join(filepath.Dir(fifo), "ran")

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	err := runExecPayload(payloadCmd(ctx), []string{fifo, "touch", marker}, 0)

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

func TestRunExecPayload_rejectsAnEmptyFifoPath(t *testing.T) {
	if err := runExecPayload(payloadCmd(t.Context()), []string{"", "true"}, 0); err == nil {
		t.Fatal("an empty fifo path must be rejected")
	}
}
