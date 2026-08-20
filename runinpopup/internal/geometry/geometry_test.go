package geometry

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		// field and position are what the caller says about the field the value
		// came from: X and Y take a position specifier, Width and Height do not.
		field    string
		position bool
		value    string
		wantErr  bool
	}{
		{name: "empty is the backend's own", field: "X", position: true},
		{name: "cells", field: "X", position: true, value: "10"},
		{name: "zero cells", field: "Width", value: "0"},
		{name: "many cells", field: "Height", value: "200"},
		{name: "percent", field: "Width", value: "80%"},
		{name: "zero percent", field: "Y", position: true, value: "0%"},
		{name: "centre", field: "X", position: true, value: "C"},
		{name: "right", field: "X", position: true, value: "R"},
		{name: "pane", field: "X", position: true, value: "P"},
		{name: "mouse", field: "Y", position: true, value: "M"},
		{name: "window status line", field: "Y", position: true, value: "W"},
		{name: "status line", field: "Y", position: true, value: "S"},
		{
			// Which axis a specifier suits is tmux's rule, and tmux's to enforce.
			name:  "a specifier tmux takes on the other axis is still a specifier",
			field: "Y", position: true, value: "R",
		},
		{name: "a size takes no specifier", field: "Width", value: "C", wantErr: true},
		{name: "nor does a height", field: "Height", value: "M", wantErr: true},
		{name: "an unknown letter", field: "X", position: true, value: "Z", wantErr: true},
		{name: "specifiers are upper case", field: "X", position: true, value: "c", wantErr: true},
		{name: "a word", field: "X", position: true, value: "centre", wantErr: true},
		{name: "a signed count", field: "Width", value: "+5", wantErr: true},
		{name: "a negative count", field: "X", position: true, value: "-5", wantErr: true},
		{name: "a fraction", field: "Width", value: "12.5", wantErr: true},
		{name: "a fractional percentage", field: "Width", value: "12.5%", wantErr: true},
		{name: "a bare percent sign", field: "Height", value: "%", wantErr: true},
		{name: "percent in front", field: "Height", value: "%80", wantErr: true},
		{name: "two percent signs", field: "Height", value: "80%%", wantErr: true},
		{name: "padded", field: "Width", value: " 80 ", wantErr: true},
		{name: "cells with a unit", field: "Width", value: "80c", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.field, tc.value, tc.position)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Validate(%q, %q, %v) = %v, want it accepted",
						tc.field, tc.value, tc.position, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate(%q, %q, %v) = nil, want it rejected",
					tc.field, tc.value, tc.position)
			}
			// The message has to be actionable on its own: it is what a user who
			// mistyped a flag reads, and only the field and the value tell them which
			// one they mistyped.
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("err = %v, want the field %q named in it", err, tc.field)
			}
			if !strings.Contains(err.Error(), tc.value) {
				t.Errorf("err = %v, want the value %q quoted in it", err, tc.value)
			}
		})
	}
}

func TestIsPosition(t *testing.T) {
	for _, value := range []string{"C", "R", "P", "M", "W", "S"} {
		if !IsPosition(value) {
			t.Errorf("IsPosition(%q) = false, want it recognized", value)
		}
	}
	for _, value := range []string{"", "c", "Z", "10", "80%", "CR"} {
		if IsPosition(value) {
			t.Errorf("IsPosition(%q) = true, want only the six letters", value)
		}
	}
}
