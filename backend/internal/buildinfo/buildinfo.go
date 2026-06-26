package buildinfo

import "runtime"

// Version、Commit 和 BuildTime 可在构建时通过 -ldflags -X 覆盖。
// 默认值清晰地表明这是「dev/本地构建」。
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// Info 持有构建元数据在某一时刻的快照。
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

// Snapshot 返回当前的构建元数据,包含 Go 运行时版本。
func Snapshot() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		BuildTime: BuildTime,
		GoVersion: runtime.Version(),
	}
}
