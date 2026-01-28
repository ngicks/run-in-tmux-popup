package pickentry

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

const (
	focusInput = iota
	focusSuggestions
)

// Model is the bubbletea model for the picker.
type Model struct {
	viewport    viewport.Model
	textInput   textinput.Model
	items       Items
	matches     fuzzy.Matches
	cursor      int
	Selected    *Item
	width       int
	height      int
	ready       bool
	focusIndex  int
	displayText string
	styles      Styles
}

// getMatches returns fuzzy matches for the given query.
// When query is empty, returns all items as matches (no filtering).
func (m Model) getMatches(query string) fuzzy.Matches {
	if query == "" {
		matches := make(fuzzy.Matches, len(m.items))
		for i := range m.items {
			matches[i] = fuzzy.Match{Index: i}
		}
		return matches
	}
	return fuzzy.FindFrom(query, m.items)
}

// NewModel creates a new picker model with the given items and display text.
func NewModel(items Items, displayText string) Model {
	ti := textinput.New()
	ti.Placeholder = "Type to search..."
	ti.Prompt = "> " // Use textinput's prompt instead of manual prefix
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 40

	// Initial match: show all items (empty query shows everything)
	matches := make(fuzzy.Matches, len(items))
	for i := range items {
		matches[i] = fuzzy.Match{Index: i}
	}

	return Model{
		textInput:   ti,
		items:       items,
		matches:     matches,
		cursor:      0,
		displayText: displayText,
		focusIndex:  focusInput,
		styles:      DefaultStyles(),
	}
}

// Init initializes the model.
func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles messages and updates the model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		viewportHeight := min(strings.Count(m.displayText, "\n")+1, m.height-15)

		if !m.ready {
			m.viewport = viewport.New(m.width-4, viewportHeight)
			m.viewport.SetContent(m.displayText)
			m.ready = true
		} else {
			m.viewport.Width = m.width - 4
			m.viewport.Height = viewportHeight
		}

		// Update text input width
		m.textInput.Width = m.width - 8

		return m, nil

	case tea.KeyMsg:
		// Handle Ctrl+A first (return to input) - must check before string switch
		// because textinput would consume it otherwise
		if msg.Type == tea.KeyCtrlA {
			if m.focusIndex != focusInput {
				m.focusIndex = focusInput
				m.textInput.Focus()
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit

		case "up", "ctrl+p":
			if m.focusIndex == focusSuggestions {
				if m.cursor == 0 {
					// At top of suggestions, move back to input
					m.focusIndex = focusInput
					m.textInput.Focus()
				} else {
					m.cursor--
				}
			} else if m.focusIndex == focusInput {
				// Scroll viewport up
				m.viewport.ScrollUp(1)
			}
			return m, nil

		case "down", "ctrl+n":
			if m.focusIndex == focusInput {
				// Move focus to suggestions
				if len(m.matches) > 0 {
					m.focusIndex = focusSuggestions
					m.textInput.Blur()
					m.cursor = 0
				}
			} else if m.focusIndex == focusSuggestions && len(m.matches) > 0 {
				m.cursor++
				if m.cursor >= len(m.matches) {
					m.cursor = 0 // wrap around
				}
			}
			return m, nil

		case "pgup":
			m.viewport.HalfPageUp()
			return m, nil

		case "pgdown":
			m.viewport.HalfPageDown()
			return m, nil

		case "enter":
			if m.focusIndex == focusInput {
				// Input focused: use input text as cmd
				inputText := m.textInput.Value()
				m.Selected = &Item{
					Id:  "",
					Cmd: inputText,
				}
				return m, tea.Quit
			} else if m.focusIndex == focusSuggestions && len(m.matches) > 0 {
				// Suggestions focused: select highlighted match
				idx := m.matches[m.cursor].Index
				selected := m.items[idx]
				m.Selected = &selected
				return m, tea.Quit
			}
			// No action if suggestions focused but no matches
			return m, nil
		}
	}

	// Handle text input updates
	if m.focusIndex == focusInput {
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		cmds = append(cmds, cmd)

		// Update fuzzy matches
		m.matches = m.getMatches(m.textInput.Value())
		// Reset cursor if out of bounds
		if m.cursor >= len(m.matches) {
			m.cursor = 0
		}
	}

	// Handle viewport updates
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// View renders the model.
func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	var sections []string

	// Display area (viewport with prompt text)
	displayArea := m.styles.DisplayArea.
		Width(m.width - 4).
		Render(m.viewport.View())
	sections = append(sections, displayArea)

	// Input area
	var inputStyle lipgloss.Style
	if m.focusIndex == focusInput {
		inputStyle = m.styles.InputAreaFocus
	} else {
		inputStyle = m.styles.InputArea
	}
	inputArea := inputStyle.
		Width(m.width - 4).
		Render(m.textInput.View())
	sections = append(sections, inputArea)

	// Suggestions list
	suggestions := m.renderSuggestions()
	var suggestionsStyle lipgloss.Style
	if m.focusIndex == focusSuggestions {
		suggestionsStyle = m.styles.SuggestionListAreaFocus
	} else {
		suggestionsStyle = m.styles.SuggestionListArea
	}
	suggestionsArea := suggestionsStyle.
		Width(m.width - 4).
		Render(suggestions)
	sections = append(sections, suggestionsArea)

	// Help bar
	helpBar := m.renderHelpBar()
	sections = append(sections, helpBar)

	content := lipgloss.JoinVertical(lipgloss.Left, sections...)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}

// renderSuggestions renders the suggestions list with fuzzy match highlighting.
func (m Model) renderSuggestions() string {
	if len(m.matches) == 0 {
		return m.styles.SuggestionItem.Render("(no matches)")
	}

	maxVisible := 8
	if len(m.matches) < maxVisible {
		maxVisible = len(m.matches)
	}

	// Calculate visible window around cursor
	start := 0
	if m.cursor >= maxVisible {
		start = m.cursor - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(m.matches) {
		end = len(m.matches)
		start = end - maxVisible
		if start < 0 {
			start = 0
		}
	}

	var lines []string
	for i := start; i < end; i++ {
		match := m.matches[i]
		item := m.items[match.Index]
		display := formatItemDisplay(item, m.width-10)

		// Apply match highlighting (pass selection state for proper background)
		isSelected := m.focusIndex == focusSuggestions && i == m.cursor
		highlighted := m.highlightMatches(display, match.MatchedIndexes, isSelected)

		if isSelected {
			lines = append(lines, m.styles.SuggestionItemSelected.Render("> "+highlighted))
		} else {
			lines = append(lines, m.styles.SuggestionItem.Render(highlighted))
		}
	}

	return strings.Join(lines, "\n")
}

// highlightMatches applies highlighting to matched characters.
// When isSelected is true, the highlight style includes the selection background.
func (m Model) highlightMatches(s string, indexes []int, isSelected bool) string {
	if len(indexes) == 0 {
		return s
	}

	// Create a map for quick lookup
	matchSet := make(map[int]bool)
	for _, idx := range indexes {
		matchSet[idx] = true
	}

	var result strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if matchSet[i] {
			style := m.styles.MatchHighlight
			if isSelected {
				// Include selection background so highlight doesn't break the selection appearance
				style = style.Background(lipgloss.Color("62"))
			}
			result.WriteString(style.Render(string(r)))
		} else {
			result.WriteRune(r)
		}
	}

	return result.String()
}

// renderHelpBar renders the help bar at the bottom.
func (m Model) renderHelpBar() string {
	helpItems := []struct {
		key  string
		desc string
	}{
		{"Ctrl+C/Esc", "quit"},
		{"↑/↓", "navigate"},
		{"Ctrl+A", "input"},
		{"Enter", "select"},
		{"PgUp/PgDn", "scroll"},
	}

	var parts []string
	for _, item := range helpItems {
		parts = append(parts, m.styles.HelpKey.Render(item.key)+": "+m.styles.HelpDesc.Render(item.desc))
	}

	return m.styles.HelpBar.Render(strings.Join(parts, m.styles.HelpDivider.String()))
}
