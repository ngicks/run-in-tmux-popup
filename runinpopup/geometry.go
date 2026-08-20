package runinpopup

import (
	"github.com/ngicks/run-in-tmux-popup/runinpopup/internal/geometry"
)

// validateGeometry reports the first of the spec's placement and size fields
// that says nothing a backend could act on, naming the field and the value.
//
// It is checked here, at the top of a launch, because the alternative is not
// checking it at all: the values are otherwise read by the multiplexer, which
// answers a bad one by refusing to open the popup — from inside a launcher whose
// output nobody is looking at — or, for the mechanisms that report nothing, by
// silently placing the popup somewhere else.
func (s PopupSpec) validateGeometry() error {
	for _, f := range []struct {
		name  string
		value string
		// position says whether the field takes tmux's position specifiers, which
		// place a popup and cannot size one.
		position bool
	}{
		{"X", s.X, true},
		{"Y", s.Y, true},
		{"Width", s.Width, false},
		{"Height", s.Height, false},
	} {
		if err := geometry.Validate(f.name, f.value, f.position); err != nil {
			return err
		}
	}
	return nil
}
