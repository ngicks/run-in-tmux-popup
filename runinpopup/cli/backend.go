package cli

import (
	"strconv"
	"strings"

	"github.com/ngicks/run-in-tmux-popup/runinpopup/backend"
)

// BackendNameList renders every backend name as a quoted prose list —
// `"tmux-popup", "tmux-floating-pane" or "zellij"` — for embedding in help text
// and flag descriptions. Reading it off backend.Names is what keeps help from
// naming a backend that does not exist, or omitting one that does.
func BackendNameList() string {
	return quoteNameList(backend.Names())
}

// quoteNameList quotes each name and joins them the way prose does: commas up
// to the last, "or" before it.
func quoteNameList(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = strconv.Quote(name)
	}
	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
	}
}
