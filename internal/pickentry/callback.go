package pickentry

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"text/template"
)

// RenderCallback renders a callback template with the given item.
func RenderCallback(tmpl string, item Item) (string, error) {
	t, err := template.New("callback").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("failed to parse callback template: %w", err)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, item); err != nil {
		return "", fmt.Errorf("failed to execute callback template: %w", err)
	}

	return buf.String(), nil
}

// ExecuteCallback executes a command in the specified shell.
func ExecuteCallback(shell, command string) error {
	cmd := exec.Command(shell, "-c", command)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// DetectShell returns the shell to use for callback execution.
// Priority: provided shell > $SHELL > /bin/sh
func DetectShell(provided string) string {
	if provided != "" {
		return provided
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}
