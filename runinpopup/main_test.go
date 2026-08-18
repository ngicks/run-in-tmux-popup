package runinpopup

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// execPayloadReexecEnv turns this test binary into the exec payload instead of a
// test runner. The exchange tests point the payload path at os.Args[0] and have
// their fake backend export the variable, so the round trip runs over the very
// argv the caller builds — asserted below — rather than over a hand-written stub.
const execPayloadReexecEnv = "RUNINPOPUP_TEST_EXEC_PAYLOAD"

func TestMain(m *testing.M) {
	if code, ok := runFakePinentry(); ok {
		os.Exit(code)
	}
	if os.Getenv(execPayloadReexecEnv) == "" {
		os.Exit(m.Run())
	}
	// The contract the caller promises the payload executable:
	//   <bin> exec-payload [--startup-timeout=<dur>] -- <command...>
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
	if len(args) < 2 || args[0] != "--" || os.Args[1] != ExecPayloadCommandName {
		fmt.Fprintf(os.Stderr, "unexpected payload argv: %q\n", os.Args)
		os.Exit(2)
	}
	outcome, err := ExecPayload(context.Background(), args[1:], opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(outcome.Status)
}
