package loggerfactory

import (
	"log/slog"
	"testing"
)

func TestReadEnv(t *testing.T) {
	t.Run("hyphenated app name maps to underscored prefix", func(t *testing.T) {
		c := &Config{Format: "json", Level: slog.LevelInfo}
		err := ReadEnv(c, "my-tool", []string{
			"MY_TOOL_LOG_FORMAT=text",
			"MY_TOOL_LOG_LEVEL=debug",
		})
		if err != nil {
			t.Fatalf("ReadEnv: %v", err)
		}
		if !c.Enabled {
			t.Fatalf("Enabled = false, want true")
		}
		if c.Format != "text" {
			t.Fatalf("Format = %q, want %q", c.Format, "text")
		}
		if c.Level != slog.LevelDebug {
			t.Fatalf("Level = %v, want %v", c.Level, slog.LevelDebug)
		}
	})

	t.Run("missing vars leave config untouched", func(t *testing.T) {
		c := &Config{Format: "json", Level: slog.LevelInfo}
		if err := ReadEnv(c, "mytool", []string{"OTHER=1"}); err != nil {
			t.Fatalf("ReadEnv: %v", err)
		}
		if c.Enabled {
			t.Fatalf("Enabled = true, want false")
		}
		if c.Format != "json" {
			t.Fatalf("Format = %q, want %q", c.Format, "json")
		}
	})

	t.Run("empty value leaves field untouched", func(t *testing.T) {
		c := &Config{Format: "json", Level: slog.LevelInfo}
		if err := ReadEnv(c, "mytool", []string{"MYTOOL_LOG_FORMAT="}); err != nil {
			t.Fatalf("ReadEnv: %v", err)
		}
		if c.Enabled {
			t.Fatalf("Enabled = true, want false (empty value should not enable)")
		}
		if c.Format != "json" {
			t.Fatalf("Format = %q, want %q", c.Format, "json")
		}
	})

	t.Run("only level set still enables", func(t *testing.T) {
		c := &Config{Format: "json", Level: slog.LevelInfo}
		if err := ReadEnv(c, "mytool", []string{"MYTOOL_LOG_LEVEL=warn"}); err != nil {
			t.Fatalf("ReadEnv: %v", err)
		}
		if !c.Enabled {
			t.Fatalf("Enabled = false, want true")
		}
		if c.Level != slog.LevelWarn {
			t.Fatalf("Level = %v, want %v", c.Level, slog.LevelWarn)
		}
	})

	t.Run("invalid value returns error", func(t *testing.T) {
		c := &Config{Format: "json", Level: slog.LevelInfo}
		err := ReadEnv(c, "mytool", []string{"MYTOOL_LOG_FORMAT=xml"})
		if err == nil {
			t.Fatalf("expected error for invalid format")
		}
	})

	t.Run("case-insensitive values", func(t *testing.T) {
		c := &Config{Format: "json", Level: slog.LevelInfo}
		if err := ReadEnv(c, "mytool", []string{
			"MYTOOL_LOG_FORMAT=TEXT",
			"MYTOOL_LOG_LEVEL=Trace",
		}); err != nil {
			t.Fatalf("ReadEnv: %v", err)
		}
		if c.Format != "text" {
			t.Fatalf("Format = %q, want %q", c.Format, "text")
		}
		if c.Level != LevelTrace {
			t.Fatalf("Level = %v, want %v", c.Level, LevelTrace)
		}
	})

	t.Run("malformed entries are skipped", func(t *testing.T) {
		c := &Config{Format: "json", Level: slog.LevelInfo}
		if err := ReadEnv(c, "mytool", []string{
			"NOTANENV", // no '='
			"MYTOOL_LOG_LEVEL=info",
		}); err != nil {
			t.Fatalf("ReadEnv: %v", err)
		}
		if !c.Enabled {
			t.Fatalf("Enabled = false, want true")
		}
	})
}
