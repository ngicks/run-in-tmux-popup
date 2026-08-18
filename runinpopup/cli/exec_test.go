package cli

import (
	"strings"
	"testing"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

func TestRenderExecResult(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result runinpopup.ExecResult
		want   string
	}{
		{
			name: "a command that ran reports its status on one line",
			result: runinpopup.ExecResult{
				Command:  []string{"sh", "-c", "echo hi"},
				ExitCode: 0,
				Stdout:   "hi\n",
			},
			want: `{"command":["sh","-c","echo hi"],"exit_code":0,"stdout":"hi\n","stderr":""}` + "\n",
		},
		{
			name: "a zero result keeps every key but the omitted error",
			want: `{"command":null,"exit_code":0,"stdout":"","stderr":""}` + "\n",
		},
		{
			name: "a command that never started carries -1 and the error",
			result: runinpopup.ExecResult{
				Command:  []string{"nope"},
				ExitCode: -1,
				Error:    `exec: "nope": executable file not found in $PATH`,
			},
			want: `{"command":["nope"],"exit_code":-1,"stdout":"","stderr":"",` +
				`"error":"exec: \"nope\": executable file not found in $PATH"}` + "\n",
		},
		{
			name: "captured output is escaped, HTML-significant bytes included",
			result: runinpopup.ExecResult{
				Command:  []string{"printf", "%s", "1 < 2"},
				ExitCode: 1,
				Stdout:   "1 < 2 && 3 > 2",
				Stderr:   "tab\there",
			},
			want: `{"command":["printf","%s","1 \u003c 2"],"exit_code":1,` +
				`"stdout":"1 \u003c 2 \u0026\u0026 3 \u003e 2","stderr":"tab\there"}` + "\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			if err := RenderExecResult(&buf, tc.result); err != nil {
				t.Fatalf("RenderExecResult: %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("RenderExecResult =\n\t%q\nwant\n\t%q", got, tc.want)
			}
		})
	}
}
