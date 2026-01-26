package preprocess

import (
	"slices"
	"testing"
)

func TestParsePinentryUserData(t *testing.T) {
	input := "(NAME_OF_HOST_PROGRAM):(path/to/bin):(session_id):(client_id):yayyayay:wooooo"
	p := ParsePinentryUserData(input)
	if p.Kind != "(NAME_OF_HOST_PROGRAM)" {
		t.Errorf("wrong")
	}
	if p.Path != "(path/to/bin)" {
		t.Errorf("wrong")
	}
	if p.SessionId != "(session_id)" {
		t.Errorf("wrong")
	}
	if p.ClientId != "(client_id)" {
		t.Errorf("wrong")
	}
	if !slices.Equal(p.Rest, []string{"yayyayay", "wooooo"}) {
		t.Errorf("wrong")
	}
	t.Logf("%#v", p)
}
