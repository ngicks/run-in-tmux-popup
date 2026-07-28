// Package loggerfactory builds an opt-in slog.Logger configured by two
// pflag.BoolFunc flags ("--log" and "--log-level") and registers those flags
// on a Cobra command's persistent flag set.
//
// Logging is opt-in: when neither flag is given (and no env-var override
// enables it), BuildLogger returns a logger backed by slog.DiscardHandler.
// The presence of either flag (or either env-var override) enables logging.
package loggerfactory

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

const (
	LevelTrace = slog.Level(-8)
	LevelFatal = slog.Level(12)
)

// Config holds the logger configuration populated by the registered flags.
type Config struct {
	Enabled bool
	Format  string
	Level   slog.Level
}

// RegisterFlags registers "--log" and "--log-level" as persistent flags on cmd
// and returns a *Config that the flag callbacks populate during parsing.
//
// The defaults applied when a flag is given without a value are "json" for
// --log and "info" for --log-level. Both flag values are case-insensitive.
func RegisterFlags(cmd *cobra.Command) *Config {
	config := &Config{
		Format: "json",
		Level:  slog.LevelInfo,
	}
	f := cmd.PersistentFlags()

	f.BoolFunc(
		"log",
		`enable logging; format "text" or "json" (case-insensitive; default "json")`,
		func(s string) error {
			config.Enabled = true
			switch v := strings.ToLower(s); v {
			case "true": // presence only
				return nil
			}
			format, err := parseFormat(s)
			if err != nil {
				return fmt.Errorf("--log: %w", err)
			}
			config.Format = format
			return nil
		},
	)

	f.BoolFunc(
		"log-level",
		`enable logging; level "trace" | "debug" | "info" | "warn" | "error" | "fatal"`+
			` (case-insensitive; default "info")`,
		func(s string) error {
			config.Enabled = true
			switch strings.ToLower(s) {
			case "true": // presence only
				return nil
			}
			level, err := parseLevel(s)
			if err != nil {
				return fmt.Errorf("--log-level: %w", err)
			}
			config.Level = level
			return nil
		},
	)

	return config
}

// ReadEnv overrides config from environment variables.
//
// env is a slice of "KEY=VALUE" entries (the form returned by os.Environ).
// Two variables are recognized, derived from appName by upper-casing it and
// converting hyphens to underscores ("my-tool" → "MY_TOOL"):
//
//   - <NAME>_LOG_FORMAT — overrides Format. Accepts "text" or "json"
//     (case-insensitive). Sets Enabled = true.
//   - <NAME>_LOG_LEVEL — overrides Level. Accepts "trace" | "debug" |
//     "info" | "warn" | "error" | "fatal" (case-insensitive). Sets
//     Enabled = true.
//
// Empty values are treated as "unset" and leave the corresponding field
// alone. Unrecognized values return an error and the config is left
// unchanged for the offending field. Variables not in env are ignored.
//
// Call ReadEnv after RegisterFlags has populated the config from CLI flags
// to let env vars override flag values, or before flag parsing to let
// flags override env vars; the order is the caller's choice.
func ReadEnv(config *Config, appName string, env []string) error {
	prefix := envPrefix(appName)
	values := lookupEnv(env, prefix+"_LOG_FORMAT", prefix+"_LOG_LEVEL")

	if v := values[prefix+"_LOG_FORMAT"]; v != "" {
		format, err := parseFormat(v)
		if err != nil {
			return fmt.Errorf("%s_LOG_FORMAT: %w", prefix, err)
		}
		config.Format = format
		config.Enabled = true
	}

	if v := values[prefix+"_LOG_LEVEL"]; v != "" {
		level, err := parseLevel(v)
		if err != nil {
			return fmt.Errorf("%s_LOG_LEVEL: %w", prefix, err)
		}
		config.Level = level
		config.Enabled = true
	}

	return nil
}

func envPrefix(appName string) string {
	return strings.ReplaceAll(strings.ToUpper(appName), "-", "_")
}

func lookupEnv(env []string, keys ...string) map[string]string {
	want := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		want[k] = struct{}{}
	}
	out := make(map[string]string, len(keys))
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		if _, ok := want[k]; !ok {
			continue
		}
		out[k] = v
	}
	return out
}

func parseFormat(s string) (string, error) {
	switch v := strings.ToLower(s); v {
	case "text", "json":
		return v, nil
	}
	return "", fmt.Errorf(`must be "text" or "json" (case-insensitive), got %q`, s)
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "trace":
		return LevelTrace, nil
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	case "fatal":
		return LevelFatal, nil
	}
	return 0, fmt.Errorf(
		`must be one of "trace", "debug", "info", "warn", "error", "fatal" (case-insensitive); got %q`,
		s,
	)
}

// BuildLogger constructs the slog.Logger described by config. When
// config.Enabled is false the logger discards all records.
//
// Output is written to os.Stderr. Pass BuildLoggerTo to redirect.
func BuildLogger(config *Config) *slog.Logger {
	return BuildLoggerTo(config, os.Stderr)
}

// BuildLoggerTo is BuildLogger with an explicit io.Writer destination.
func BuildLoggerTo(config *Config, w io.Writer) *slog.Logger {
	if !config.Enabled {
		return slog.New(slog.DiscardHandler)
	}
	opts := &slog.HandlerOptions{
		AddSource: true,
		Level:     config.Level,
	}
	var h slog.Handler
	switch config.Format {
	case "text":
		h = slog.NewTextHandler(w, opts)
	default: // "json"
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h)
}
