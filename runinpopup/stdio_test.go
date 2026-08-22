package runinpopup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The endpoint is a file rather than a pipe so the input reaches its end on its
// own: a controller parked on an OS input that never ends is exactly the state
// this package cannot interrupt, and it has no place in a test's teardown.
func newStdinEndpoint(t *testing.T, input string) *os.File {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "assuan-in")
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatalf("writing the assuan input: %v", err)
	}
	stdin, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening the assuan input: %v", err)
	}
	t.Cleanup(func() { stdin.Close() })
	return stdin
}

func TestAssuanInput(t *testing.T) {
	stdin := newStdinEndpoint(t, "GETPIN\n")

	input, err := newAssuanInput(t.Context(), stdin)
	if err != nil {
		t.Fatalf("newAssuanInput: %v", err)
	}

	got, err := io.ReadAll(input.end)
	if err != nil {
		t.Fatalf("reading the assuan input: %v", err)
	}
	if string(got) != "GETPIN\n" {
		t.Errorf("read %q from the input, want %q", got, "GETPIN\n")
	}

	if err := input.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Ending the exchange must not end the process's own stream: gpg-agent speaks
	// Assuan over it, and this process did not open it.
	if _, err := stdin.Seek(0, io.SeekStart); err != nil {
		t.Errorf("seeking the input endpoint = %v, want it left open", err)
	}
}

// An exchange ends its relay by closing it, so what that close reports is caused
// by the exchange itself and says nothing about how it went.
func TestAssuanInput_closeOfItsOwnEndIsNotAFailure(t *testing.T) {
	stdin := newStdinEndpoint(t, strings.Repeat("D never read\n", 100))

	input, err := newAssuanInput(t.Context(), stdin)
	if err != nil {
		t.Fatalf("newAssuanInput: %v", err)
	}
	if err := input.close(); err != nil {
		t.Fatalf("close = %v, want an undelivered input to be nobody's failure", err)
	}
}

// An endpoint that failed on its own is a different matter, and a failed input
// has nobody to answer it: the close is what reports it.
func TestAssuanInput_endpointFailureReachesTheExchange(t *testing.T) {
	readErr := errors.New("the assuan input went away")

	input, err := newAssuanInput(t.Context(), errReader{readErr})
	if err != nil {
		t.Fatalf("newAssuanInput: %v", err)
	}
	if _, err := io.ReadAll(input.end); !errors.Is(err, readErr) {
		t.Fatalf("reading the failing endpoint = %v, want it to wrap %v", err, readErr)
	}

	err = input.close()

	if !errors.Is(err, readErr) {
		t.Fatalf("close = %v, want it to wrap %v", err, readErr)
	}
	if !strings.Contains(err.Error(), streamStdin) {
		t.Errorf("close = %v, want the stream that failed named", err)
	}
}

// A canceled exchange has already said why it ended; the end it takes down with
// it has nothing to add. The input is a pipe nobody writes to, which is the state
// a real exchange is canceled in: parked waiting for gpg-agent's next line.
func TestAssuanInput_cancellationEndsTheRelay(t *testing.T) {
	stdin, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating the assuan input pipe: %v", err)
	}
	t.Cleanup(func() { stdin.Close(); w.Close() })
	ctx, cancel := context.WithCancel(t.Context())

	input, err := newAssuanInput(ctx, stdin)
	if err != nil {
		t.Fatalf("newAssuanInput: %v", err)
	}
	cancel()

	if _, err := io.ReadAll(input.end); !errors.Is(err, context.Canceled) {
		t.Errorf("reading the input after cancellation = %v, want the cancellation", err)
	}
	if err := input.close(); err != nil {
		t.Errorf("close = %v, want the cancellation to be nobody's relay failure", err)
	}
}
