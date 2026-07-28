// Package versioninfo combines a project-supplied version string with the
// VCS / build info embedded by `go build` (via runtime/debug.ReadBuildInfo).
//
// The project's version constant lives in internal/libver/libver.go
// (rewritten by the bump-libver release tool,
// github.com/ngicks/go-common/tools/bump-libver). Callers pass that
// constant into ReadVersionInfo to get the full picture.
package versioninfo

import "runtime/debug"

// Info is the combined version + build descriptor returned by ReadVersionInfo.
//
// VCS-derived fields are empty when build info is unavailable (e.g. tests,
// `-buildvcs=false`, or builds outside a VCS checkout).
type Info struct {
	Version    string // the project-supplied version string (e.g. libver.Version)
	Commit     string // vcs.revision
	CommitTime string // vcs.time
	Modified   bool   // vcs.modified == "true"
	GoVersion  string // bi.GoVersion (the toolchain that built the binary)
}

// ReadVersionInfo combines version with the VCS / build info recorded by
// `go build`. Pass the project's Version constant; the rest is derived
// from runtime/debug.ReadBuildInfo.
func ReadVersionInfo(version string) Info {
	info := Info{Version: version}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	info.GoVersion = bi.GoVersion
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Commit = s.Value
		case "vcs.time":
			info.CommitTime = s.Value
		case "vcs.modified":
			info.Modified = s.Value == "true"
		}
	}
	return info
}
