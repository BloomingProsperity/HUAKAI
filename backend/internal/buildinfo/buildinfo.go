package buildinfo

import "runtime"

// Version, Commit, and BuildTime are overridable via -ldflags -X at build time.
// Defaults communicate "dev/local build" clearly.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// Info holds a point-in-time snapshot of build metadata.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

// Snapshot returns the current build metadata including the Go runtime version.
func Snapshot() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		BuildTime: BuildTime,
		GoVersion: runtime.Version(),
	}
}
