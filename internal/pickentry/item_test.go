package pickentry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseItemsFromString(t *testing.T) {
	jsonStr := `[{"id":"apple","cmd":"echo apple"},{"id":"banana","cmd":"echo banana"}]`
	items, err := ParseItemsFromString(jsonStr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Id != "apple" || items[0].Cmd != "echo apple" {
		t.Errorf("item 0 mismatch: got %+v", items[0])
	}
	if items[1].Id != "banana" || items[1].Cmd != "echo banana" {
		t.Errorf("item 1 mismatch: got %+v", items[1])
	}
}

func TestItemsSource(t *testing.T) {
	items := Items{
		{Id: "test", Cmd: "cmd"},
		{Id: "another", Cmd: "data"},
	}

	if items.Len() != 2 {
		t.Errorf("expected Len() = 2, got %d", items.Len())
	}

	str := items.String(0)
	if str != "test: cmd" {
		t.Errorf("expected 'test: cmd', got '%s'", str)
	}
}

func TestFormatItemDisplay(t *testing.T) {
	item := Item{Id: "id", Cmd: "cmd"}
	display := formatItemDisplay(item, 100)
	if display != "id: cmd" {
		t.Errorf("expected 'id: cmd', got '%s'", display)
	}

	// Test truncation
	longItem := Item{Id: "longid", Cmd: "this is a very long cmd that should be truncated"}
	truncated := formatItemDisplay(longItem, 20)
	if len(truncated) != 20 || truncated[len(truncated)-3:] != "..." {
		t.Errorf("expected truncated string ending with '...', got '%s' (len=%d)", truncated, len(truncated))
	}
}

func TestLoadItemsFromFile(t *testing.T) {
	// Create temp file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "items.json")
	content := `[{"id":"file-item","cmd":"file-cmd"}]`
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	items, err := LoadItemsFromFile(tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].Id != "file-item" {
		t.Errorf("item mismatch: got %+v", items)
	}
}
