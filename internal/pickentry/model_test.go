package pickentry

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestView_NoSelectionHighlightOnStartup(t *testing.T) {
	// This test catches the bug where first item was highlighted
	// even when focus was on input
	items := Items{
		{Id: "apple", Cmd: "echo apple"},
		{Id: "banana", Cmd: "echo banana"},
	}
	m := setupTestModel(items, "Select:")

	output := stripAnsi(m.View())

	// On startup, focus is on input, so no "> " prefix should appear
	if strings.Contains(output, "> apple") || strings.Contains(output, "> banana") {
		t.Error("No item should be highlighted when focus is on input")
	}

	// But items should still be visible (without selection marker)
	if !strings.Contains(output, "apple") {
		t.Error("Items should be visible in suggestions")
	}
}

func TestView_SelectionHighlightWhenFocusOnSuggestions(t *testing.T) {
	items := Items{
		{Id: "apple", Cmd: "echo apple"},
		{Id: "banana", Cmd: "echo banana"},
	}
	m := setupTestModel(items, "Select:")

	// Press down to move focus to suggestions
	m = sendKey(m, tea.KeyDown)

	output := stripAnsi(m.View())

	// Now first item should have "> " prefix
	if !strings.Contains(output, "> apple") {
		t.Error("First item should be highlighted when focus moves to suggestions")
	}
}

func TestView_SelectionMovesWithCursor(t *testing.T) {
	items := Items{
		{Id: "apple", Cmd: "echo apple"},
		{Id: "banana", Cmd: "echo banana"},
	}
	m := setupTestModel(items, "Select:")

	// Move to suggestions
	m = sendKey(m, tea.KeyDown)
	// Move to second item
	m = sendKey(m, tea.KeyDown)

	output := stripAnsi(m.View())

	if !strings.Contains(output, "> banana") {
		t.Error("Second item should be highlighted after pressing down twice")
	}
	if strings.Contains(output, "> apple") {
		t.Error("First item should not be highlighted")
	}
}

func TestView_FocusReturnsToInput(t *testing.T) {
	items := Items{
		{Id: "apple", Cmd: "echo apple"},
	}
	m := setupTestModel(items, "Select:")

	// Move to suggestions
	m = sendKey(m, tea.KeyDown)
	// Move back to input (up from first item)
	m = sendKey(m, tea.KeyUp)

	output := stripAnsi(m.View())

	// No selection highlight should appear
	if strings.Contains(output, "> apple") {
		t.Error("No item should be highlighted when focus returns to input")
	}
}

func TestView_NoMatchesMessage(t *testing.T) {
	items := Items{
		{Id: "apple", Cmd: "echo apple"},
	}
	m := setupTestModel(items, "Select:")

	// Type something that won't match
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z', 'z', 'z'}})
	m = updated.(Model)

	output := stripAnsi(m.View())

	if !strings.Contains(output, "(no matches)") {
		t.Error("Should show 'no matches' message when filter has no results")
	}
}

func TestView_ItemsVisibleOnStartup(t *testing.T) {
	items := Items{
		{Id: "apple", Cmd: "echo apple"},
		{Id: "banana", Cmd: "echo banana"},
		{Id: "cherry", Cmd: "echo cherry"},
	}
	m := setupTestModel(items, "Test prompt")

	output := stripAnsi(m.View())

	// All items should be visible
	for _, item := range items {
		if !strings.Contains(output, item.Id) {
			t.Errorf("Item %q should be visible in initial view", item.Id)
		}
	}

	// Prompt should be visible
	if !strings.Contains(output, "Test prompt") {
		t.Error("Display text should be visible")
	}
}
