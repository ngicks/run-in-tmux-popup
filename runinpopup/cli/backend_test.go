package cli

import (
	"strconv"
	"strings"
	"testing"

	"github.com/ngicks/run-in-tmux-popup/runinpopup/backend"
)

func TestBackendNameList_namesEveryBackend(t *testing.T) {
	list := BackendNameList()
	for _, name := range backend.Names() {
		if !strings.Contains(list, strconv.Quote(name)) {
			t.Errorf("BackendNameList = %q, missing the backend %q", list, name)
		}
	}
	if got, want := strings.Count(list, `"`), 2*len(backend.Names()); got != want {
		t.Errorf("BackendNameList = %q holds %d quotes, want %d: it names something extra",
			list, got, want)
	}
}

func TestBackendNameList_readsAsProse(t *testing.T) {
	for _, tc := range []struct {
		name  string
		names []string
		want  string
	}{
		{name: "none", names: nil, want: ""},
		{name: "one stands alone", names: []string{"a"}, want: `"a"`},
		{name: "two are joined by or", names: []string{"a", "b"}, want: `"a" or "b"`},
		{
			name:  "more are comma-separated up to the last",
			names: []string{"a", "b", "c"},
			want:  `"a", "b" or "c"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := quoteNameList(tc.names); got != tc.want {
				t.Errorf("quoteNameList(%q) = %q, want %q", tc.names, got, tc.want)
			}
		})
	}
}
