package runinpopup

import (
	"reflect"
	"slices"
	"testing"
)

func TestParsePinentryUserData(t *testing.T) {
	input := "(NAME_OF_HOST_PROGRAM):(path/to/bin):(session_id):(client_id)" +
		":(session,meta):yeee:wooooo"
	p := ParsePinentryUserData(input)
	if p.Kind != "(NAME_OF_HOST_PROGRAM)" {
		t.Errorf("Kind = %q", p.Kind)
	}
	if p.Path != "(path/to/bin)" {
		t.Errorf("Path = %q", p.Path)
	}
	if p.SessionId != "(session_id)" {
		t.Errorf("SessionId = %q", p.SessionId)
	}
	if p.ClientId != "(client_id)" {
		t.Errorf("ClientId = %q", p.ClientId)
	}
	if p.SessionMeta != "(session,meta)" {
		t.Errorf("SessionMeta = %q", p.SessionMeta)
	}
	if !slices.Equal(p.Rest, []string{"yeee", "wooooo"}) {
		t.Errorf("Rest = %#v", p.Rest)
	}
	t.Logf("%#v", p)
}

func TestParsePinentryUserData_shortAndEmpty(t *testing.T) {
	p := ParsePinentryUserData("  TMUX_POPUP:/usr/bin/tmux\n")
	if p.Kind != "TMUX_POPUP" {
		t.Errorf("Kind = %q", p.Kind)
	}
	if p.Path != "/usr/bin/tmux" {
		t.Errorf("Path = %q", p.Path)
	}
	if p.SessionId != "" || p.ClientId != "" || p.SessionMeta != "" || p.Rest != nil {
		t.Errorf("trailing fields must stay empty: %#v", p)
	}

	if got := ParsePinentryUserData(""); !reflect.DeepEqual(got, PinentryUserData{}) {
		t.Errorf("empty input = %#v, want zero value", got)
	}
}

func TestPinentryUserData_Debug(t *testing.T) {
	for _, tc := range []struct {
		kind string
		want bool
	}{
		{"TMUX_POPUP_DEBUG", true},
		{"ZELLIJ_POPUP_DEBUG", true},
		{" tmux_popup_debug ", true},
		{"TMUX_POPUP", false},
		{"TMUX_POPUP_DEBUGGER", false},
		{"", false},
	} {
		if got := (PinentryUserData{Kind: tc.kind}).Debug(); got != tc.want {
			t.Errorf("Kind %q: Debug() = %t, want %t", tc.kind, got, tc.want)
		}
	}
}
