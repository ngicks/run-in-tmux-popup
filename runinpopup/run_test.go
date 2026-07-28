package runinpopup

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// shellBackend stands in for a multiplexer: it "opens a popup" by running the
// payload in a local shell, so Run's launch path can be exercised for real.
type shellBackend struct {
	environ    []string
	prepareErr error
	prepared   int
	restored   int
	// seenOnRestore is what the popup had written by the time restore ran.
	seenOnRestore string
	stdout        *bytes.Buffer
}

func (b *shellBackend) Name() string { return "shell" }

func (b *shellBackend) PopupCommand(spec PopupSpec) (string, []string) {
	return "sh", []string{"-c", spec.shellCommandLine()}
}

func (b *shellBackend) Environ() []string { return b.environ }

func (b *shellBackend) Prepare(context.Context) (func(context.Context) error, error) {
	if b.prepareErr != nil {
		return nil, b.prepareErr
	}
	b.prepared++
	return func(context.Context) error {
		b.restored++
		b.seenOnRestore = b.stdout.String()
		return nil
	}, nil
}

func TestRun(t *testing.T) {
	stdout := new(bytes.Buffer)
	backend := &shellBackend{environ: []string{"POPUP_GREETING=hello"}, stdout: stdout}

	err := Run(t.Context(), backend, RunOptions{
		Spec:   PopupSpec{Script: `printf '%s from the popup' "$POPUP_GREETING"`},
		Stdout: stdout,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := stdout.String(); got != "hello from the popup" {
		t.Errorf("popup output = %q; Environ must reach the popup command", got)
	}
	if backend.prepared != 1 || backend.restored != 1 {
		t.Errorf("prepared = %d, restored = %d, want 1 each", backend.prepared, backend.restored)
	}
	// Restore undoes state the popup needed, so it may only run once the popup is
	// gone.
	if backend.seenOnRestore != "hello from the popup" {
		t.Errorf("restore ran before the popup finished, saw %q", backend.seenOnRestore)
	}
}

func TestRun_argvIsNotSplitByTheShell(t *testing.T) {
	stdout := new(bytes.Buffer)
	backend := &shellBackend{stdout: stdout}

	err := Run(t.Context(), backend, RunOptions{
		Spec:   PopupSpec{Command: []string{"printf", "%s|", "a b", "c"}},
		Stdout: stdout,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := stdout.String(); got != "a b|c|" {
		t.Errorf("popup output = %q, want %q", got, "a b|c|")
	}
}

func TestRun_popupFailure(t *testing.T) {
	backend := &shellBackend{stdout: new(bytes.Buffer)}

	err := Run(t.Context(), backend, RunOptions{
		Spec: PopupSpec{Script: "exit 3"},
	})
	if err == nil || !strings.Contains(err.Error(), "popup failed") {
		t.Fatalf("err = %v, want a popup failure", err)
	}
	if backend.restored != 1 {
		t.Errorf("restored = %d, want the state restored even on failure", backend.restored)
	}
}

func TestRun_prepareFailureAborts(t *testing.T) {
	prepareErr := errors.New("de-zoom failed")
	backend := &shellBackend{prepareErr: prepareErr, stdout: new(bytes.Buffer)}

	err := Run(t.Context(), backend, RunOptions{
		Spec: PopupSpec{Script: "exit 0"},
	})
	if !errors.Is(err, prepareErr) {
		t.Fatalf("err = %v, want it to wrap %v", err, prepareErr)
	}
}

func TestCallPinentry_requiresHandshaker(t *testing.T) {
	err := CallPinentry(t.Context(), &shellBackend{}, PinentryOptions{
		TempDir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "PinentryHandshaker") {
		t.Fatalf("err = %v, want it to name the missing interface", err)
	}
	if err := CallPinentry(t.Context(), tmuxBackend(t), PinentryOptions{}); err == nil {
		t.Fatal("an unset TempDir must be rejected")
	}
}
