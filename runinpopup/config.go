package runinpopup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config is the materialized configuration the service consumes, after every
// layer (defaults < file < env < flags) is applied. Its fields are value types
// (the merged config is always concrete) and carry json tags so the `config`
// subcommand can marshal it. The config file is JSON; the yaml tags ride along
// unused, kept field-for-field in sync so adopting YAML stays a decoder swap
// rather than a retagging. The file is NOT decoded into Config —
// PartialConfig is the decode target; Config only ever holds a fully-merged
// result.
type Config struct {
	// PinentryPath is the pinentry binary the proxy executes outside the popup.
	PinentryPath string `json:"pinentry_path" yaml:"pinentry_path"`
	// DefaultBackend names the popup backend used when no backend is given
	// explicitly. Valid values are "tmux-popup" and "zellij"; empty means
	// auto-detect from the environment.
	DefaultBackend string `json:"default_backend" yaml:"default_backend"`
	// Timeouts bounds the popup/pinentry handshake (nested sub-config:
	// deep-merged).
	Timeouts TimeoutsConfig `json:"timeouts" yaml:"timeouts"`
}

// TimeoutsConfig bounds each stage of the popup/pinentry handshake. A
// time.Duration serializes as an integer nanosecond count in JSON, while the
// env layer accepts Go duration strings ("2m", "20s").
type TimeoutsConfig struct {
	// Overall bounds the whole popup/pinentry exchange.
	Overall time.Duration `json:"overall" yaml:"overall"`
	// TTYRead bounds reading the popup's tty name from the handshake FIFO.
	TTYRead time.Duration `json:"tty_read" yaml:"tty_read"`
	// DoneWrite bounds signalling the popup to close once pinentry exits.
	DoneWrite time.Duration `json:"done_write" yaml:"done_write"`
}

// DefaultConfig is the lowest-precedence layer. Initialize maps and sub-configs
// here so later layers deep-merge into a populated base.
//
// DefaultBackend stays empty on purpose: an unset backend means "detect from
// PINENTRY_USER_DATA / $TMUX / $ZELLIJ", so materializing a concrete backend
// here would make that detection unreachable.
func DefaultConfig() Config {
	return Config{
		PinentryPath:   "/usr/bin/pinentry-curses",
		DefaultBackend: "",
		Timeouts: TimeoutsConfig{
			Overall:   2 * time.Minute,
			TTYRead:   20 * time.Second,
			DoneWrite: time.Second,
		},
	}
}

// PartialConfig is the exported sparse mirror of Config's serialized shape (the
// same keys): a nil/zero field means "absent, leave the lower layer"; a set
// field is an explicit value, including an explicit zero. It is the decode
// target for the config file AND the struct LoadConfig fills via caarlos0/env,
// so file and env merge through one method, Apply. Exported so other code can
// build or inspect partial overrides.
//
// Carries three tag sets, kept in sync with Config field-for-field: json for
// the file decode, yaml unused (see Config), and env / envPrefix for
// caarlos0/env. The RUN_IN_POPUP_ prefix is applied once via envOptions, so the
// tags hold only the bare names (PINENTRY_PATH -> RUN_IN_POPUP_PINENTRY_PATH,
// TIMEOUTS_ + OVERALL -> RUN_IN_POPUP_TIMEOUTS_OVERALL).
//
// JSON tags use ",omitzero" (Go 1.24+) so a marshaled partial stays sparse
// while preserving an explicit zero; YAML has no omitzero, so its tags use
// ",omitempty".
//
//nolint:lll // triple json/yaml/env tags; one field per line, never wrap tags
type PartialConfig struct {
	PinentryPath   *string               `json:"pinentry_path,omitzero" yaml:"pinentry_path,omitempty" env:"PINENTRY_PATH"`
	DefaultBackend *string               `json:"default_backend,omitzero" yaml:"default_backend,omitempty" env:"DEFAULT_BACKEND"`
	Timeouts       PartialTimeoutsConfig `json:"timeouts,omitzero" yaml:"timeouts,omitempty" envPrefix:"TIMEOUTS_"`
}

//nolint:lll // triple json/yaml/env tags; one field per line, never wrap tags
type PartialTimeoutsConfig struct {
	Overall   *time.Duration `json:"overall,omitzero" yaml:"overall,omitempty" env:"OVERALL"`
	TTYRead   *time.Duration `json:"tty_read,omitzero" yaml:"tty_read,omitempty" env:"TTY_READ"`
	DoneWrite *time.Duration `json:"done_write,omitzero" yaml:"done_write,omitempty" env:"DONE_WRITE"`
}

// Apply overlays p's present fields onto base and returns the merged Config.
// Merge rules by field kind:
//   - scalar:        non-nil pointer overwrites (explicit zero included).
//   - nested struct: deep-merged via the sub-partial's Apply — always called; a
//     zero sub-partial (all fields nil) merges nothing.
func (p PartialConfig) Apply(base Config) Config {
	if p.PinentryPath != nil {
		base.PinentryPath = *p.PinentryPath
	}
	if p.DefaultBackend != nil {
		base.DefaultBackend = *p.DefaultBackend
	}
	base.Timeouts = p.Timeouts.Apply(base.Timeouts)
	return base
}

func (p PartialTimeoutsConfig) Apply(base TimeoutsConfig) TimeoutsConfig {
	if p.Overall != nil {
		base.Overall = *p.Overall
	}
	if p.TTYRead != nil {
		base.TTYRead = *p.TTYRead
	}
	if p.DoneWrite != nil {
		base.DoneWrite = *p.DoneWrite
	}
	return base
}

// envOptions configures caarlos0/env for the env layer in LoadConfig. The
// variable names live in the env: / envPrefix: tags on PartialConfig; the
// RUN_IN_POPUP_ prefix is applied here, yielding RUN_IN_POPUP_PINENTRY_PATH,
// RUN_IN_POPUP_TIMEOUTS_OVERALL, etc.
var envOptions = env.Options{Prefix: "RUN_IN_POPUP_"}

// LoadConfig assembles defaults < config file < environment through Apply. The
// ./cmd layer applies explicitly-set flags on top (flags win). flagPath is the
// --config value ("" when the flag is unset).
//
// The env layer fills a PartialConfig with caarlos0/env: a scalar is set
// (non-nil) only when its variable is present; absent ones stay nil so Apply
// leaves the lower layer untouched. caarlos0/env recurses into the value nested
// sub-config via envPrefix and parses durations natively ("2m", "20s").
// ParseWithOptions errors when a present value fails to parse — a hard error
// that aborts startup.
func LoadConfig(flagPath string) (Config, error) {
	cfg := DefaultConfig()

	path, err := configPath(flagPath)
	if err != nil {
		return cfg, err
	}
	filePartial, err := unmarshalConfigFile(path)
	if err != nil {
		return cfg, err
	}
	cfg = filePartial.Apply(cfg)

	var envPartial PartialConfig
	if err := env.ParseWithOptions(&envPartial, envOptions); err != nil {
		return cfg, err
	}
	cfg = envPartial.Apply(cfg)

	return cfg, nil
}

// unmarshalConfigFile only reads + decodes; it never merges. It decodes into a
// fresh zero PartialConfig (all nil) and returns the zero value when the file
// does not exist. A non-ENOENT read error or a JSON parse error aborts.
//
// Decoding into a zero value — never a defaults-populated struct — sidesteps the
// v1 encoding/json merge edge cases that decoding into a populated struct hits;
// Apply does the merge afterward.
func unmarshalConfigFile(path string) (PartialConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return PartialConfig{}, nil
		}
		return PartialConfig{}, fmt.Errorf("read config %q: %w", path, err)
	}
	var p PartialConfig
	if err := json.Unmarshal(b, &p); err != nil {
		return PartialConfig{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	return p, nil
}

// envConfVar names the config-file-path override. It is the one env var read by
// hand (the file path is needed before parsing, and is not a Config field); every
// other variable lives in PartialConfig's env tags. MixedCaps, so no naming-lint
// directive is needed.
const envConfVar = "RUN_IN_POPUP_CONF"

// configPath resolves the file path: --config (flagPath), else $envConfVar, else
// os.UserConfigDir()/run-in-popup/config.json.
func configPath(flagPath string) (string, error) {
	if flagPath != "" {
		return flagPath, nil
	}
	if p, ok := os.LookupEnv(envConfVar); ok {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "run-in-popup", "config.json"), nil
}
