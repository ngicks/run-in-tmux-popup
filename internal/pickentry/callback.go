package pickentry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"text/template"
)

var FuncMap = template.FuncMap{
	"json": func(v any) (string, error) {
		bin, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(bin), nil
	},
}

// RenderCallback renders a callback template with the given item.
func RenderCallback(tmpl string, item Item) (string, error) {
	t, err := template.New("callback").
		Funcs(FuncMap).
		Parse(tmpl)
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
