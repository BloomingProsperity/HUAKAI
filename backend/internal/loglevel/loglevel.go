// Package loglevel 暴露进程级的原子日志级别，使得 admin 的
// /loglevel 端点能在运行时（事故排查时）提高或降低日志详尽程度，
// 而无需重启 gateway。main() 用这个级别构建 zap logger；
// admin handler 通过 zap 的 AtomicLevel HTTP handler 读取/设置它。
package loglevel

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Level 是进程级的日志级别，默认 Info。除非运维人员主动修改，
// 否则其行为与固定的 zap.NewProduction() logger 完全一致。
var Level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
