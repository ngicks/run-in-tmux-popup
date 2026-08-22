// Package libver pins the module-wide, release-controlled version string.
//
// The file lives at the fixed path internal/libver/libver.go so the release
// tool always knows where to rewrite it; the package declares Version and
// nothing else.
package libver

// Version is the human-readable version string for the whole module. Bump it
// with the release tool:
//
//	go run github.com/ngicks/go-common/tools/bump-libver@latest <release-version>
//
// which rewrites this declaration, commits, and tags, then bumps it to the
// next "-devel" version.
//
// Edit by hand only when the release tool is unavailable (e.g. cherry-pick
// of a release commit).
const Version = "v0.0.4"
