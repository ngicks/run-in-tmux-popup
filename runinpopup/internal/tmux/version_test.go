package tmux

import "testing"

func TestAffectedByZoomCrash(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    bool
		why     string
	}{
		{"tmux 3.7b", true, "the version that crashes"},
		{"tmux 3.7b\n", true, "tmux -V output ends in a newline"},
		{"tmux 3.7", true, "no suffix sorts before 3.7a"},
		{"tmux 3.7a", true, ""},
		{"tmux 3.7c", false, "the fix"},
		{"tmux 3.7d", false, ""},
		{"tmux 3.6", true, ""},
		{"tmux 2.9a", true, "floating panes did not exist; the popup fails on its own"},
		{"tmux 3.8", false, ""},
		{"tmux 3.10", false, "the minor is compared as a number, not as text"},
		{"tmux 4.0", false, ""},
		{"tmux next-3.8", true, "a dev build's version pins no commit, so it cannot show the fix"},
		{"tmux master", true, "unparseable"},
		{"3.7c", true, `the "tmux " prefix is part of the contract`},
		{"tmux 3.7.1", true, "not a release form tmux prints"},
		{"tmux 3.", true, "unparseable"},
		{"tmux ", true, "unparseable"},
		{"", true, "no output at all"},
		{"garbage", true, "unparseable"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			if got := AffectedByZoomCrash(tc.version); got != tc.want {
				t.Errorf("AffectedByZoomCrash(%q) = %v, want %v (%s)",
					tc.version, got, tc.want, tc.why)
			}
		})
	}
}
