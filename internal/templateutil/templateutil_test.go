package templateutil

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestFuncDocs_MatchesFuncMap(t *testing.T) {
	funcNames := slices.Sorted(maps.Keys(FuncMap()))

	docNames := make([]string, 0, len(FuncDocs()))
	for _, d := range FuncDocs() {
		docNames = append(docNames, d.Name)
	}
	slices.Sort(docNames)

	if !slices.Equal(funcNames, docNames) {
		t.Fatalf(
			"FuncDocs and FuncMap out of sync: FuncMap has %v, FuncDocs has %v",
			funcNames,
			docNames,
		)
	}
}

func TestFuncHelp_ListsEveryDoc(t *testing.T) {
	help := FuncHelp()
	for _, d := range FuncDocs() {
		if !strings.Contains(help, d.Usage) || !strings.Contains(help, d.Desc) {
			t.Errorf("FuncHelp is missing %q / %q:\n%s", d.Usage, d.Desc, help)
		}
	}
}
