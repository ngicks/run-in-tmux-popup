package tmux

import (
	"cmp"
	"context"
	"strconv"
	"strings"
)

// Version returns the raw output of "tmux -V", the form AffectedByZoomCrash
// reads.
func (c *Client) Version(ctx context.Context) (string, error) {
	return c.run(ctx, "-V")
}

// The floating-pane zoom crash is fixed in tmux 3.7c, unreleased as of
// 2026-07-28.
const (
	zoomCrashFixedMajor  = 3
	zoomCrashFixedMinor  = 7
	zoomCrashFixedSuffix = "c"
)

// AffectedByZoomCrash reports whether the tmux that printed version — the raw
// output of "tmux -V" — crashes when a floating pane is created over a zoomed
// pane.
//
// Only a version positively identifiable as 3.7c or later counts as fixed.
// Everything parseVersion rejects is treated as affected, including the "tmux
// next-3.8" of development builds: that string pins no commit, so it cannot
// show the fix is in. A spurious de-zoom is flicker; a missed one takes the
// server down.
func AffectedByZoomCrash(version string) bool {
	major, minor, suffix, ok := parseVersion(version)
	if !ok {
		return true
	}
	return cmp.Or(
		cmp.Compare(major, zoomCrashFixedMajor),
		cmp.Compare(minor, zoomCrashFixedMinor),
		cmp.Compare(suffix, zoomCrashFixedSuffix),
	) < 0
}

// parseVersion splits the output of "tmux -V" — "tmux 3.7b" — into 3, 7 and
// "b", an absent letter suffix being the empty string that sorts before "a". It
// accepts only that release form; anything else reports ok == false.
func parseVersion(out string) (major, minor int, suffix string, ok bool) {
	v, ok := strings.CutPrefix(strings.TrimSpace(out), "tmux ")
	if !ok {
		return 0, 0, "", false
	}
	majorStr, rest, ok := strings.Cut(v, ".")
	if !ok {
		return 0, 0, "", false
	}
	minorStr := rest
	if i := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		minorStr, suffix = rest[:i], rest[i:]
	}
	for _, r := range suffix {
		if r < 'a' || r > 'z' {
			return 0, 0, "", false
		}
	}
	major, err := strconv.Atoi(majorStr)
	if err != nil {
		return 0, 0, "", false
	}
	minor, err = strconv.Atoi(minorStr)
	if err != nil {
		return 0, 0, "", false
	}
	return major, minor, suffix, true
}
