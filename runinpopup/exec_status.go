package runinpopup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
)

func runExecCommand(ctx context.Context, argv []string) (ExecResult, int) {
	result := ExecResult{Command: argv, ExitCode: -1}
	if len(argv) == 0 {
		result.Error = "no command to run"
		return result, 1
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	// Both streams are watched on stderr: this process's stdout is the result
	// channel, while its stderr is the popup's terminal — the exec exchange
	// allocates nothing for stderr, so it is still the one the user is looking
	// at.
	cmd.Stdout = io.MultiWriter(os.Stderr, &stdout)
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)

	err := cmd.Run()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()

	if err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			result.Error = err.Error()
			return result, 1
		}
	}
	result.ExitCode = cmd.ProcessState.ExitCode()
	return result, execProcessStatus(cmd.ProcessState)
}

// execProcessStatus turns a finished command's state into a status this process
// can exit with. A signal death is -1, which exit(2) cannot carry, so it becomes
// the 128+signal every shell reports; anything else unusable becomes 1. The
// reported ExecResult keeps the raw -1.
func execProcessStatus(state *os.ProcessState) int {
	if code := state.ExitCode(); code >= 0 {
		return code
	}
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return 1
}
