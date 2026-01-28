package pickentry

import (
	"regexp"

	tea "github.com/charmbracelet/bubbletea"
)

// stripAnsi removes ANSI escape codes from a string
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripAnsi(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

// setupTestModel creates a model initialized with window size
func setupTestModel(items Items, displayText string) Model {
	m := NewModel(items, displayText)
	// Initialize with window size to make it ready
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(Model)
}

// sendKey simulates a key press
func sendKey(m Model, key tea.KeyType) Model {
	updated, _ := m.Update(tea.KeyMsg{Type: key})
	return updated.(Model)
}
