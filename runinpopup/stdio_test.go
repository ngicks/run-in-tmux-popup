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

// The endpoints are files rather than pipes so the input reaches its end on its
// own: a controller parked on an OS input that never ends is exactly the state
// this package cannot interrupt, and it has no place in a test's teardown.
func newStdioEndpoints(t *testing.T, input string) (*os.File, *os.File) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "assuan-in")
	if err := os.WriteFile(in, []byte(input), 0o600); err != nil {
		t.Fatalf("writing the assuan input: %v", err)
	}
	stdin, err := os.Open(in)
	if err != nil {
		t.Fatalf("opening the assuan input: %v", err)
	}
	t.Cleanup(func() { stdin.Close() })
	return stdin, createFile(t, dir, "assuan-out")
}

func TestProcessStdio(t *testing.T) {
	stdin, stdout := newStdioEndpoints(t, "GETPIN\n")

	stdio, err := newProcessStdio(t.Context(), stdin, stdout, io.Discard)
	if err != nil {
		t.Fatalf("newProcessStdio: %v", err)
	}

	got, err := io.ReadAll(stdio.stdin)
	if err != nil {
		t.Fatalf("reading the assuan input: %v", err)
	}
	if string(got) != "GETPIN\n" {
		t.Errorf("read %q from the input, want %q", got, "GETPIN\n")
	}
	if _, err := stdio.stdout.Write([]byte("OK\n")); err != nil {
		t.Fatalf("writing the assuan output: %v", err)
	}

	if err := stdio.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if b, err := os.ReadFile(stdout.Name()); err != nil || string(b) != "OK\n" {
		t.Errorf("the output endpoint holds %q (%v), want %q", b, err, "OK\n")
	}
	// Ending the exchange must not end the process's own streams: gpg-agent
	// speaks Assuan over them, and this process did not open them.
	if _, err := stdout.Write([]byte("still open\n")); err != nil {
		t.Errorf("writing the output endpoint = %v, want it left open", err)
	}
	if _, err := stdin.Seek(0, io.SeekStart); err != nil {
		t.Errorf("seeking the input endpoint = %v, want it left open", err)
	}
}

// An exchange ends its relays by closing them, so what that close reports is
// caused by the exchange itself and says nothing about how it went.
func TestProcessStdio_closeOfItsOwnEndsIsNotAFailure(t *testing.T) {
	stdin, stdout := newStdioEndpoints(t, strings.Repeat("D never read\n", 100))

	stdio, err := newProcessStdio(t.Context(), stdin, stdout, io.Discard)
	if err != nil {
		t.Fatalf("newProcessStdio: %v", err)
	}
	if err := stdio.close(); err != nil {
		t.Fatalf("close = %v, want an undelivered input to be nobody's failure", err)
	}
}

// An endpoint that failed on its own is a different matter, and each direction
// tells the exchange in its own way: an output endpoint answers the write that
// hit it — which is the pinentry child's relay, whose failure travels with the
// child — while a failed input has nobody to answer, so the close reports it.
func TestProcessStdio_endpointFailureReachesTheExchange(t *testing.T) {
	t.Run("the output endpoint answers the write", func(t *testing.T) {
		stdin, _ := newStdioEndpoints(t, "")
		writeErr := errors.New("the assuan output went away")

		stdio, err := newProcessStdio(t.Context(), stdin, errWriter{writeErr}, io.Discard)
		if err != nil {
			t.Fatalf("newProcessStdio: %v", err)
		}
		defer stdio.close()

		if _, err := stdio.stdout.Write([]byte("OK\n")); !errors.Is(err, writeErr) {
			t.Errorf("writing the failing endpoint = %v, want it to wrap %v", err, writeErr)
		}
	})

	t.Run("the failed input is reported by the close", func(t *testing.T) {
		_, stdout := newStdioEndpoints(t, "")
		readErr := errors.New("the assuan input went away")

		stdio, err := newProcessStdio(t.Context(), errReader{readErr}, stdout, io.Discard)
		if err != nil {
			t.Fatalf("newProcessStdio: %v", err)
		}
		if _, err := io.ReadAll(stdio.stdin); !errors.Is(err, readErr) {
			t.Fatalf("reading the failing endpoint = %v, want it to wrap %v", err, readErr)
		}

		err = stdio.close()

		if !errors.Is(err, readErr) {
			t.Fatalf("close = %v, want it to wrap %v", err, readErr)
		}
		if !strings.Contains(err.Error(), streamStdin) {
			t.Errorf("close = %v, want the stream that failed named", err)
		}
	})
}

// A canceled exchange has already said why it ended; the ends it takes down with
// it have nothing to add. The input is a pipe nobody writes to, which is the
// state a real exchange is canceled in: parked waiting for gpg-agent's next
// line.
func TestProcessStdio_cancellationEndsTheRelays(t *testing.T) {
	stdin, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating the assuan input pipe: %v", err)
	}
	t.Cleanup(func() { stdin.Close(); w.Close() })
	_, stdout := newStdioEndpoints(t, "")
	ctx, cancel := context.WithCancel(t.Context())

	stdio, err := newProcessStdio(ctx, stdin, stdout, io.Discard)
	if err != nil {
		t.Fatalf("newProcessStdio: %v", err)
	}
	cancel()

	if _, err := io.ReadAll(stdio.stdin); !errors.Is(err, context.Canceled) {
		t.Errorf("reading the input after cancellation = %v, want the cancellation", err)
	}
	if err := stdio.close(); err != nil {
		t.Errorf("close = %v, want the cancellation to be nobody's relay failure", err)
	}
}
