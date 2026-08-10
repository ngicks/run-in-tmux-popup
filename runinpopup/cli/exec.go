package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

// RenderExecResult writes result to w as one compact JSON object followed by a
// newline.
//
// Compact rather than indented, unlike RenderConfig: this output is read by the
// process that called exec, not by a person browsing its configuration, and one
// object per line is what a caller can pipe into jq or read line by line.
func RenderExecResult(w io.Writer, result runinpopup.ExecResult) error {
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(b))
	return nil
}
