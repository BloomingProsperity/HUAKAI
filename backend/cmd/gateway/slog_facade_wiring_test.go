package main

import (
	"context"
	"log/slog"
	"testing"

	"go.uber.org/zap/zapcore"

	"github.com/BloomingProsperity/HUAKAI/internal/loglevel"
)

// 判别:setupSlogFacade 装配后,slog.Default() 的级别闸门必须跟随 loglevel.Level
// (即 /admin/v1/loglevel 同时管住 zap 与 slog 两栈)。若 main 漏调 SetDefault 或
// 门面未桥接 loglevel,本测试必红(内建默认 handler 固定 Info+,不随 loglevel 变)。
func TestSetupSlogFacadeBridgesLoglevel(t *testing.T) {
	origLogger := slog.Default()
	origLevel := loglevel.Level.Level()
	t.Cleanup(func() {
		slog.SetDefault(origLogger)
		loglevel.Level.SetLevel(origLevel)
	})

	setupSlogFacade()
	ctx := context.Background()

	loglevel.Level.SetLevel(zapcore.WarnLevel)
	if slog.Default().Enabled(ctx, slog.LevelInfo) {
		t.Fatal("loglevel=warn 时默认 slog 的 Info 应被抑制(内建 handler 做不到这点)")
	}
	loglevel.Level.SetLevel(zapcore.DebugLevel)
	if !slog.Default().Enabled(ctx, slog.LevelDebug) {
		t.Fatal("loglevel=debug 时默认 slog 的 Debug 应可见")
	}
}
