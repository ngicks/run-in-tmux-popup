// Package cli holds run-in-popup's CLI-presentation code — rendering resolved
// configuration, the vocabulary command help is written from, and other
// terminal output. The ./cmd wiring layer calls into this package and returns
// its error; presentation logic never lives under ./cmd itself.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/template"

	"github.com/ngicks/run-in-tmux-popup/internal/templateutil"
	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

// TemplateFuncHelp returns the aligned help block for the helper functions
// available to a --format template, the same set every template renderer
// exposes. Command help embeds it so the docs cannot drift from
// templateutil.FuncMap.
func TemplateFuncHelp() string {
	return templateutil.FuncHelp()
}

// RenderConfig writes the resolved configuration to w.
//
// With format == "" it writes indented JSON. Otherwise format is parsed as a Go
// text/template and executed against cfg (field paths use the Go field names,
// e.g. {{.PinentryPath}}); it sees the shared templateutil.FuncMap helpers
// (json, ...). Either form is terminated with a trailing newline. A malformed or
// failing template is returned as an error attributed to the --format flag.
func RenderConfig(w io.Writer, cfg runinpopup.Config, format string) error {
	if format != "" {
		tmpl, err := template.New("config").
			Funcs(templateutil.FuncMap()).
			Parse(format)
		if err != nil {
			return fmt.Errorf("--format: %w", err)
		}
		if err := tmpl.Execute(w, cfg); err != nil {
			return fmt.Errorf("--format: %w", err)
		}
		fmt.Fprintln(w)
		return nil
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(b))
	return nil
}

// ConfigFieldDoc documents one field of [runinpopup.Config].
type ConfigFieldDoc struct {
	Name   string           // Go field name, as a --format template addresses it
	Type   string           // Go type, e.g. "time.Duration"; empty on a sub-config
	Key    string           // JSON key of this field alone, not the path to it
	Desc   string           // one-line human description
	Fields []ConfigFieldDoc // a sub-config's own fields; nil on a scalar
}

// ConfigDocs returns documentation for every field of [runinpopup.Config], in
// declaration order. It is the single source of truth behind ConfigSchemaHelp
// and every command help text describing the configuration; keep it in sync
// with the struct (guarded by a test).
func ConfigDocs() []ConfigFieldDoc {
	return []ConfigFieldDoc{
		{Name: "PinentryPath", Type: "string", Key: "pinentry_path", Desc: "pinentry binary"},
		{Name: "DefaultBackend", Type: "string", Key: "default_backend", Desc: "backend to use"},
		{
			Name: "Timeouts",
			Key:  "timeouts",
			Desc: "handshake timeouts",
			Fields: []ConfigFieldDoc{
				{Name: "Overall", Type: "time.Duration", Key: "overall", Desc: "whole exchange"},
				{Name: "TTYRead", Type: "time.Duration", Key: "tty_read", Desc: "read popup tty"},
				{
					Name: "DoneWrite",
					Type: "time.Duration",
					Key:  "done_write",
					Desc: "signal popup done",
				},
			},
		},
	}
}

// configTypeName is how the value a --format template runs against is named in
// help: the type is [runinpopup.Config], and the tree is rooted at it.
const configTypeName = "Config"

// ConfigSchemaHelp renders ConfigDocs as an indented tree for embedding in
// command help text — one row per field, showing the Go field name a --format
// template uses, its type, its description, and the dotted JSON key in
// parentheses. Columns are padded to a common width; the block ends with a
// trailing newline.
func ConfigSchemaHelp() string {
	rows := configHelpRows(ConfigDocs(), "", 0, nil)

	// Columns are padded by rune count, not byte length: the branch glyphs are
	// multi-byte, so the %-*s verb would align them by the wrong measure.
	var treeWidth, typeWidth, descWidth int
	for _, r := range rows {
		treeWidth = max(treeWidth, len([]rune(r.tree)))
		typeWidth = max(typeWidth, len([]rune(r.typ)))
		descWidth = max(descWidth, len([]rune(r.desc)))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "  %s\n", configTypeName)
	for _, r := range rows {
		fmt.Fprintf(
			&b, "%s %s # %s (%s)\n",
			padRunes(r.tree, treeWidth),
			padRunes(r.typ, typeWidth),
			padRunes(r.desc, descWidth),
			r.key,
		)
	}
	return b.String()
}

// padRunes right-pads s with spaces to width runes.
func padRunes(s string, width int) string {
	return s + strings.Repeat(" ", width-len([]rune(s)))
}

// configHelpRow is one rendered line of ConfigSchemaHelp before its columns are
// padded: the tree cell already carries its indent and branch glyph, and key is
// the dotted path down from the root rather than the leaf key alone.
type configHelpRow struct {
	tree string
	typ  string
	desc string
	key  string
}

// configHelpRows flattens a doc tree depth-first, which is the order the tree
// glyphs assume: a node's own row is immediately followed by its children's.
func configHelpRows(
	docs []ConfigFieldDoc,
	keyPrefix string,
	depth int,
	rows []configHelpRow,
) []configHelpRow {
	for i, d := range docs {
		branch := "├─ "
		if i == len(docs)-1 {
			branch = "└─ "
		}
		key := keyPrefix + d.Key
		rows = append(rows, configHelpRow{
			// Two spaces of block indent, then one level per depth, wide enough
			// for the branch glyph so a child lines up under its parent's name.
			tree: "  " + strings.Repeat("    ", depth) + branch + "." + d.Name,
			typ:  d.Type,
			desc: d.Desc,
			key:  key,
		})
		rows = configHelpRows(d.Fields, key+".", depth+1, rows)
	}
	return rows
}
