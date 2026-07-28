package versioninfo

import (
	"runtime/debug"
	"testing"
)

func TestReadVersionInfo_versionPropagates(t *testing.T) {
	info := ReadVersionInfo("v1.2.3-test")
	if info.Version != "v1.2.3-test" {
		t.Fatalf("Version = %q, want %q", info.Version, "v1.2.3-test")
	}
	bi, _ := debug.ReadBuildInfo()

	t.Logf("%#v", bi)

	t.Logf("%#v", info)
}

func TestReadVersionInfo_goVersionPopulated(t *testing.T) {
	info := ReadVersionInfo("v0.0.0")
	if info.GoVersion == "" {
		t.Fatal("GoVersion is empty; debug.ReadBuildInfo should populate it under `go test`")
	}
}
