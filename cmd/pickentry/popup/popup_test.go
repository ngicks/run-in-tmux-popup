package popup

import (
	"testing"
)

func TestParseTmuxOpts(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  TmuxOpts
	}{
		{
			name:  "default keyword",
			input: "default",
			want:  TmuxOpts{},
		},
		{
			name:  "empty string",
			input: "",
			want:  TmuxOpts{},
		},
		{
			name:  "position only",
			input: "center",
			want:  TmuxOpts{Position: "center"},
		},
		{
			name:  "position with width and height",
			input: "top,50%,30%",
			want:  TmuxOpts{Position: "top", Width: "50%", Height: "30%"},
		},
		{
			name:  "width and height without position",
			input: "50%,50%",
			want:  TmuxOpts{Width: "50%", Height: "50%"},
		},
		{
			name:  "position with width only",
			input: "bottom,100%",
			want:  TmuxOpts{Position: "bottom", Width: "100%"},
		},
		{
			name:  "width only without position",
			input: "80%",
			want:  TmuxOpts{Width: "80%"},
		},
		{
			name:  "all positions",
			input: "left",
			want:  TmuxOpts{Position: "left"},
		},
		{
			name:  "right position with dimensions",
			input: "right,60%,40%",
			want:  TmuxOpts{Position: "right", Width: "60%", Height: "40%"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseTmuxOpts(tt.input)
			if got != tt.want {
				t.Errorf("ParseTmuxOpts(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}
