// Package templateutil centralizes the [text/template] helpers shared by every
// template-rendering call site (the config subcommand's --format, and any other
// renderer a project adds). One func map means every template sees the same
// functions; one FuncDocs means the help text cannot drift.
package templateutil

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
)

// FuncMap returns the template function map shared across the project's template
// renderers. A fresh map is returned on each call so callers may mutate it
// without affecting one another.
//
//	json VALUE   → VALUE marshaled as indented JSON
//
// Almost every consumer needs json at minimum (dump a sub-value); add
// project-specific helpers (env, quote, which, ...) here and document each in
// FuncDocs.
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"json": JSON,
	}
}

// FuncDoc documents a single helper exposed by FuncMap.
type FuncDoc struct {
	Name  string // bare function name as registered in FuncMap
	Usage string // name plus argument placeholders, e.g. "json VALUE"
	Desc  string // one-line human description
}

// FuncDocs returns documentation for every helper in FuncMap, in a stable
// display order. It is the single source of truth behind FuncHelp and the
// command help text; keep it in sync with FuncMap (guarded by a test).
func FuncDocs() []FuncDoc {
	return []FuncDoc{
		{Name: "json", Usage: "json VALUE", Desc: "VALUE marshaled as indented JSON"},
	}
}

// FuncHelp renders FuncDocs as an aligned, indented block for embedding in
// command help text. Each line is "  <usage>  <desc>" with the usage column
// padded to a common width; the block ends with a trailing newline.
func FuncHelp() string {
	docs := FuncDocs()
	width := 0
	for _, d := range docs {
		width = max(width, len(d.Usage))
	}
	var b strings.Builder
	for _, d := range docs {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, d.Usage, d.Desc)
	}
	return b.String()
}

// JSON marshals v as indented JSON. Handy in a --format template to dump a
// sub-struct, e.g. {{ json .Timeouts }}.
func JSON(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
