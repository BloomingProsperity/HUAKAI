package buildinfo

import (
	"runtime"
	"testing"
)

func TestSnapshotDefaultsAndGoVersion(t *testing.T) {
	snap := Snapshot()

	if snap.Version != "dev" {
		t.Errorf("Version default = %q, want %q", snap.Version, "dev")
	}
	if snap.Commit != "unknown" {
		t.Errorf("Commit default = %q, want %q", snap.Commit, "unknown")
	}
	if snap.BuildTime != "unknown" {
		t.Errorf("BuildTime default = %q, want %q", snap.BuildTime, "unknown")
	}

	want := runtime.Version()
	if snap.GoVersion != want {
		t.Errorf("GoVersion = %q, want %q", snap.GoVersion, want)
	}
	if snap.GoVersion == "" {
		t.Errorf("GoVersion must not be empty")
	}
}

func TestSnapshotLdflagsOverride(t *testing.T) {
	// Simulate what -ldflags -X would do at link time.
	origVersion := Version
	origCommit := Commit
	origBuildTime := BuildTime
	defer func() {
		Version = origVersion
		Commit = origCommit
		BuildTime = origBuildTime
	}()

	Version = "v1.2.3"
	Commit = "abc1234"
	BuildTime = "2026-06-09T12:00:00Z"

	snap := Snapshot()
	if snap.Version != "v1.2.3" {
		t.Errorf("Version = %q, want %q", snap.Version, "v1.2.3")
	}
	if snap.Commit != "abc1234" {
		t.Errorf("Commit = %q, want %q", snap.Commit, "abc1234")
	}
	if snap.BuildTime != "2026-06-09T12:00:00Z" {
		t.Errorf("BuildTime = %q, want %q", snap.BuildTime, "2026-06-09T12:00:00Z")
	}
	if snap.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", snap.GoVersion, runtime.Version())
	}
}
