package commands

import (
	"errors"
	"fmt"
	"testing"
)

// The interface main.go looks the status up through is deliberately not
// mirrored here — cmd/run-in-popup/main_test.go drives the real dispatch, so
// there is nothing to drift. What is asserted here is the type's own behaviour.
func TestExitCodeError(t *testing.T) {
	reportFailed := errors.New("writing result fifo: broken pipe")

	for _, tc := range []struct {
		name      string
		err       error
		wantCode  int
		wantPrint bool
		wantMsg   string
	}{
		{
			// The command already had its say on the popup terminal.
			name:     "a bare status prints nothing",
			err:      &exitCodeError{code: 2},
			wantCode: 2,
			wantMsg:  "exit status 2",
		},
		{
			name:      "a wrapped failure keeps the status and is printed",
			err:       &exitCodeError{code: 3, err: reportFailed},
			wantCode:  3,
			wantPrint: true,
			wantMsg:   reportFailed.Error(),
		},
		{
			name:     "success is still a status",
			err:      &exitCodeError{code: 0},
			wantCode: 0,
			wantMsg:  "exit status 0",
		},
		{
			name:      "found through an outer wrap",
			err:       fmt.Errorf("payload: %w", &exitCodeError{code: 4, err: reportFailed}),
			wantCode:  4,
			wantPrint: true,
			wantMsg:   "payload: " + reportFailed.Error(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.wantMsg {
				t.Errorf("Error() = %q, want %q", got, tc.wantMsg)
			}

			coded, ok := errors.AsType[*exitCodeError](tc.err)
			if !ok {
				t.Fatal("the status is not reachable on this error")
			}
			if coded.ExitCode() != tc.wantCode {
				t.Errorf("ExitCode() = %d, want %d", coded.ExitCode(), tc.wantCode)
			}
			if gotPrint := coded.Unwrap() != nil; gotPrint != tc.wantPrint {
				t.Errorf("printed = %v, want %v", gotPrint, tc.wantPrint)
			}
		})
	}

	// The wrapped error stays reachable, so a caller can still tell what failed.
	wrapped := &exitCodeError{code: 3, err: reportFailed}
	if !errors.Is(wrapped, reportFailed) {
		t.Error("the wrapped error must stay in the chain")
	}
	if errors.Is(&exitCodeError{code: 3}, reportFailed) {
		t.Error("a bare status must not match an unrelated error")
	}
}
