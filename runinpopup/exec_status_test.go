package runinpopup

import (
	"syscall"
	"testing"
)

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
