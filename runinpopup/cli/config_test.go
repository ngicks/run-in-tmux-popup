package cli

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

func TestRenderConfig(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cfg    runinpopup.Config
		format string
		want   string
	}{
		{
			name: "no format writes indented JSON carrying every key",
			cfg: runinpopup.Config{
				PinentryPath: "/usr/bin/pinentry-curses",
				Backend:      "tmux-popup",
				Timeouts: runinpopup.TimeoutsConfig{
					Overall:   2 * time.Minute,
					TTYRead:   20 * time.Second,
					DoneWrite: time.Second,
				},
			},
			want: `{
  "pinentry_path": "/usr/bin/pinentry-curses",
  "backend": "tmux-popup",
  "timeouts": {
    "overall": 120000000000,
    "tty_read": 20000000000,
    "done_write": 1000000000
  }
}
`,
		},
		{
			name: "a zero config spells its zeros out rather than dropping keys",
			want: `{
  "pinentry_path": "",
  "backend": "",
  "timeouts": {
    "overall": 0,
    "tty_read": 0,
    "done_write": 0
  }
}
`,
		},
		{
			name:   "a format template addresses fields by their Go names",
			cfg:    runinpopup.Config{PinentryPath: "/opt/pinentry"},
			format: "{{.PinentryPath}}",
			want:   "/opt/pinentry\n",
		},
		{
			name: "a format template sees the shared json helper",
			cfg: runinpopup.Config{
				Timeouts: runinpopup.TimeoutsConfig{Overall: time.Minute},
			},
			format: "{{json .Timeouts}}",
			want: `{
  "overall": 60000000000,
  "tty_read": 0,
  "done_write": 0
}
`,
		},
		{
			name:   "a template rendering nothing still ends in a newline",
			format: "{{.Backend}}",
			want:   "\n",
		},
		{
			name:   "literal text passes through",
			cfg:    runinpopup.Config{Backend: "zellij"},
			format: "backend={{.Backend}}",
			want:   "backend=zellij\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			if err := RenderConfig(&buf, tc.cfg, tc.format); err != nil {
				t.Fatalf("RenderConfig: %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("RenderConfig =\n\t%q\nwant\n\t%q", got, tc.want)
			}
		})
	}
}

func TestRenderConfig_formatFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format string
	}{
		{
			name:   "a malformed template fails to parse",
			format: "{{.PinentryPath",
		},
		{
			name:   "an unknown field fails at execution",
			format: "{{.Nonexistent}}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			err := RenderConfig(&buf, runinpopup.Config{}, tc.format)
			if err == nil || !strings.Contains(err.Error(), "--format") {
				t.Fatalf("err = %v, want one attributed to --format", err)
			}
		})
	}
}

// ConfigDocs is written by hand so the descriptions can say something a struct
// cannot; reflection lives here instead, where it holds the table to the type
// it documents. A field added to runinpopup.Config without a doc entry — or the
// other way round — fails this test rather than quietly vanishing from help.
func TestConfigDocs_documentEveryConfigField(t *testing.T) {
	want := configTypeLines(t, reflect.TypeFor[runinpopup.Config](), "", "")
	got := configDocLines(ConfigDocs(), "", "")

	if !slices.Equal(got, want) {
		t.Errorf(
			"ConfigDocs and runinpopup.Config disagree:\n\tdocumented:\n\t\t%s"+
				"\n\tdeclared:\n\t\t%s",
			strings.Join(got, "\n\t\t"),
			strings.Join(want, "\n\t\t"),
		)
	}
}

func TestConfigDocs_describeEveryField(t *testing.T) {
	var walk func(docs []ConfigFieldDoc, prefix string)
	walk = func(docs []ConfigFieldDoc, prefix string) {
		for _, d := range docs {
			if d.Desc == "" {
				t.Errorf("%s%s has no description, so help would show a bare row", prefix, d.Name)
			}
			walk(d.Fields, prefix+d.Name+".")
		}
	}
	walk(ConfigDocs(), "")
}

func TestConfigSchemaHelp_showsEveryDocumentedField(t *testing.T) {
	help := ConfigSchemaHelp()

	var walk func(docs []ConfigFieldDoc, keyPrefix string)
	walk = func(docs []ConfigFieldDoc, keyPrefix string) {
		for _, d := range docs {
			key := keyPrefix + d.Key
			for _, want := range []string{"." + d.Name, d.Type, "(" + key + ")", d.Desc} {
				if !strings.Contains(help, want) {
					t.Errorf("ConfigSchemaHelp is missing %q of %s:\n%s", want, key, help)
				}
			}
			walk(d.Fields, key+".")
		}
	}
	walk(ConfigDocs(), "")
}

// configDocLines renders a doc tree as one line per node: the path a --format
// template addresses, the key the config file spells, and the type. A grouping
// node has no type of its own, so its line ends after the key.
func configDocLines(docs []ConfigFieldDoc, goPrefix, jsonPrefix string) []string {
	var lines []string
	for _, d := range docs {
		lines = append(lines, configSchemaLine(goPrefix+"."+d.Name, jsonPrefix+d.Key, d.Type))
		lines = append(
			lines,
			configDocLines(d.Fields, goPrefix+"."+d.Name, jsonPrefix+d.Key+".")...)
	}
	return lines
}

// configTypeLines renders the same lines off the struct itself, so the two can
// be compared.
func configTypeLines(t *testing.T, typ reflect.Type, goPrefix, jsonPrefix string) []string {
	t.Helper()
	var lines []string
	for field := range typ.Fields() {
		key, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if key == "" {
			t.Errorf("%s.%s: no json tag to document", typ, field.Name)
		}
		goPath := goPrefix + "." + field.Name
		if field.Type.Kind() == reflect.Struct {
			lines = append(lines, configSchemaLine(goPath, jsonPrefix+key, ""))
			lines = append(lines, configTypeLines(t, field.Type, goPath, jsonPrefix+key+".")...)
			continue
		}
		lines = append(lines, configSchemaLine(goPath, jsonPrefix+key, field.Type.String()))
	}
	return lines
}

func configSchemaLine(goPath, jsonPath, typeName string) string {
	return strings.TrimSpace(goPath + " (" + jsonPath + ") " + typeName)
}
