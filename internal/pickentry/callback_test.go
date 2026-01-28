package pickentry

import (
	"os"
	"testing"
)

func TestRenderCallback(t *testing.T) {
	item := Item{Id: "test-id", Cmd: "test-cmd"}

	result, err := RenderCallback("echo {{.Id}} {{.Cmd}}", item)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "echo test-id test-cmd" {
		t.Errorf("expected 'echo test-id test-cmd', got '%s'", result)
	}
}

func TestRenderCallbackError(t *testing.T) {
	item := Item{Id: "test", Cmd: "value"}
	_, err := RenderCallback("{{.Invalid}}", item)
	if err == nil {
		t.Error("expected error for invalid template field")
	}
}

func TestDetectShell(t *testing.T) {
	// Test with provided shell
	if shell := DetectShell("/bin/zsh"); shell != "/bin/zsh" {
		t.Errorf("expected '/bin/zsh', got '%s'", shell)
	}

	// Test with $SHELL env
	oldShell := os.Getenv("SHELL")
	os.Setenv("SHELL", "/bin/bash")
	defer os.Setenv("SHELL", oldShell)

	if shell := DetectShell(""); shell != "/bin/bash" {
		t.Errorf("expected '/bin/bash', got '%s'", shell)
	}

	// Test fallback
	os.Unsetenv("SHELL")
	if shell := DetectShell(""); shell != "/bin/sh" {
		t.Errorf("expected '/bin/sh', got '%s'", shell)
	}
}
