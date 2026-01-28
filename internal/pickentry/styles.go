package pickentry

import "github.com/charmbracelet/lipgloss"

// Styles holds all the lipgloss styles for the picker UI.
type Styles struct {
	// Display area (top text/prompt)
	DisplayArea lipgloss.Style

	// Input area
	InputArea      lipgloss.Style
	InputAreaFocus lipgloss.Style

	// Suggestions list
	SuggestionItem          lipgloss.Style
	SuggestionItemSelected  lipgloss.Style
	SuggestionListArea      lipgloss.Style
	SuggestionListAreaFocus lipgloss.Style

	// Match highlighting
	MatchHighlight lipgloss.Style

	// Help bar
	HelpBar     lipgloss.Style
	HelpKey     lipgloss.Style
	HelpDesc    lipgloss.Style
	HelpDivider lipgloss.Style
}

// DefaultStyles returns the default style configuration.
func DefaultStyles() Styles {
	return Styles{
		DisplayArea: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1),

		InputArea: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1),

		InputAreaFocus: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205")).
			Padding(0, 1),

		SuggestionItem: lipgloss.NewStyle().
			PaddingLeft(2),

		SuggestionItemSelected: lipgloss.NewStyle().
			PaddingLeft(0).
			Foreground(lipgloss.Color("255")).
			Background(lipgloss.Color("62")),

		SuggestionListArea: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1),

		SuggestionListAreaFocus: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205")).
			Padding(0, 1),

		MatchHighlight: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212")),

		HelpBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")),

		HelpKey: lipgloss.NewStyle().
			Foreground(lipgloss.Color("205")),

		HelpDesc: lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")),

		HelpDivider: lipgloss.NewStyle().
			Foreground(lipgloss.Color("238")).
			SetString(" | "),
	}
}
