// The exec exchange's contract: what the two halves agree on before either
// exists. The payload half is ExecPayload in exec_payload.go; the caller half is
// a JsonIpcLauncher parameterized on ExecResult, wired up wherever the argv
// below is rendered. ExecPayloadOptions.StartupTimeout documents the bound the
// two halves share.

package runinpopup

// ExecResult is the outcome of one command run inside a popup, as the payload
// reports it back to the caller. The json tags are a documented wire format:
// the exec subcommand prints this value verbatim on its stdout.
type ExecResult struct {
	// Command is the argv the payload ran.
	Command []string `json:"command"`
	// ExitCode is the command's exit status, or -1 when it never started or was
	// killed by a signal — the convention os/exec itself uses.
	ExitCode int `json:"exit_code"`
	// Stdout and Stderr are everything the command wrote, captured in full while
	// it was drawing on the popup terminal.
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	// Error says why the command could not be run to a known status: it never
	// started, or its output could not be relayed. It stays empty whenever the
	// command ran and ExitCode is its own, however it exited.
	Error string `json:"error,omitzero"`
}

// ExecOutcome is what ExecPayload hands back to the popup process it runs in —
// as opposed to ExecResult, which is what travels to the caller.
type ExecOutcome struct {
	// Result is what was reported to the caller. It is the zero value when Ran is
	// false.
	Result ExecResult
	// Ran reports whether the command was attempted at all. It is false only when
	// the caller was already gone before anything could be tried, so there is no
	// status to stand in for.
	Ran bool
	// Status is the exit status the popup process should carry, so the
	// multiplexer sees what the command did. It is Result.ExitCode, except that a
	// signal death — -1 in the JSON, which no exit(2) can carry — becomes
	// 128+signal, and anything else unusable becomes 1.
	Status int
}

// ExecPayloadCommandName is the subcommand the popup re-invokes the payload
// executable with, and ExecPayloadStartupTimeoutFlag the one flag it may carry
// along with it:
//
//	<PayloadPath> exec-payload [--startup-timeout=<dur>] -- <Command...>
//
// The library owns both names because both halves have to agree on them: the
// caller renders that argv into the popup's command line, and the CLI registers
// a hidden leaf accepting it and calls ExecPayload.
const (
	ExecPayloadCommandName        = "exec-payload"
	ExecPayloadStartupTimeoutFlag = "startup-timeout"
)
