package pickentry

import (
	"encoding/json"
	"fmt"
	"os"
)

// Item represents a selectable item in the picker.
type Item struct {
	Id  string `json:"id"`
	Cmd string `json:"cmd"`
}

// Items is a slice of Item that implements fuzzy.Source.
type Items []Item

// String returns the display format for item at index i.
// Format: "id: value" (truncated with "..." if too long).
func (items Items) String(i int) string {
	return formatItemDisplay(items[i], 60)
}

// Len returns the number of items.
func (items Items) Len() int {
	return len(items)
}

// formatItemDisplay formats an item as "id: cmd" with truncation.
func formatItemDisplay(item Item, maxLen int) string {
	display := fmt.Sprintf("%s: %s", item.Id, item.Cmd)
	if len(display) > maxLen {
		return display[:maxLen-3] + "..."
	}
	return display
}

// LoadItemsFromFile loads items from a JSON file.
func LoadItemsFromFile(path string) (Items, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read items file: %w", err)
	}
	return ParseItems(data)
}

// ParseItems parses items from JSON bytes.
func ParseItems(data []byte) (Items, error) {
	var items Items
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("failed to parse items JSON: %w", err)
	}
	return items, nil
}

// ParseItemsFromString parses items from a JSON string.
func ParseItemsFromString(s string) (Items, error) {
	return ParseItems([]byte(s))
}
