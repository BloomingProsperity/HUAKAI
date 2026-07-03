package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"strings"
	"testing"

	"go.uber.org/zap/zapcore"

	"github.com/BloomingProsperity/HUAKAI/internal/loglevel"
)

// restoreLogState 对称还原 setupSlogFacade 触碰的全部进程级状态:
// slog 默认 logger、标准库 log 包的 writer/flags(slog.SetDefault 会隐式改它们)、
// loglevel 全局级别。漏还原任何一项都会不可逆污染同包后续测试。
func restoreLogState(t *testing.T) {
	t.Helper()
	origLogger := slog.Default()
	origWriter := log.Writer()
	origFlags := log.Flags()
	origLevel := loglevel.Level.Level()
	t.Cleanup(func() {
		slog.SetDefault(origLogger)
		log.SetOutput(origWriter)
		log.SetFlags(origFlags)
		loglevel.Level.SetLevel(origLevel)
	})
}

// 判别:setupSlogFacade 装配后,slog.Default() 的级别闸门必须跟随 loglevel.Level
// (即 /admin/v1/loglevel 同时管住 zap 与 slog 两栈)。注意:本测试自己调用
// setupSlogFacade,只验证函数体行为,防不了"main 里的装配调用被删"——那个回退由
// 下方 TestMainWiresSlogFacade 源级守卫兜住。
func TestSetupSlogFacadeBridgesLoglevel(t *testing.T) {
	restoreLogState(t)

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

// 判别:slog.SetDefault 有隐藏副作用——Go 会同时把标准库 log 包改道到 slog
// handler 并固定按 Info 级过 loglevel 闸门(log.SetOutput(handlerWriter)+
// SetFlags(0),见 Go 源码 log/slog/logger.go 的 SetDefault)。若不显式退回,
// /loglevel=warn 降噪时 http.Server 的 panic 栈、trust-chain fail-open 警告等
// log 通道输出会整条静默消失,且 log.Printf 动态文本进 message(门面不扫消息)
// 成脱敏破口。setupSlogFacade 必须恢复 log 包基底行为:无条件直写 stderr、
// 标准时间前缀——即不过任何级别闸门。
// 变异靶:删 setupSlogFacade 里恢复 log 包的两行 → 本测试必红。
func TestSetupSlogFacadeRestoresLogPackage(t *testing.T) {
	restoreLogState(t)

	setupSlogFacade()

	if log.Writer() != os.Stderr {
		t.Fatal("装配后标准库 log 包必须直写 stderr(SetDefault 的隐式改道必须被退回,否则 log 通道被 loglevel 闸门静默吞掉)")
	}
	if log.Flags() != log.LstdFlags {
		t.Fatalf("装配后 log 包 flags 应为 LstdFlags(基底时间前缀),得到 %d", log.Flags())
	}
}

// 源级守卫:main() 无法直接单测,而上方行为测试自己调用 setupSlogFacade——
// 删掉 main 里的装配调用它们照样绿(对抗审查实证过的盲区)。这里读 main.go
// 源文本(诚实标注:这是文本守卫非行为验证,防的就是"装配调用被删,slog 全线
// 退回文本 handler"这一个具体回退)。跑本测试须 -count=1,否则 Go 测试缓存
// 不感知源文件变化会给假绿。
func TestMainWiresSlogFacade(t *testing.T) {
	src, err := os.ReadFile("main.go") // go test 的工作目录即包目录,读的是与运行时同一份源文件
	if err != nil {
		t.Fatalf("读 main.go 失败: %v", err)
	}
	text := string(src)
	start := strings.Index(text, "func main() {")
	if start < 0 {
		t.Fatal("main.go 中找不到 func main()")
	}
	end := strings.Index(text[start:], "\n}")
	if end < 0 {
		t.Fatal("main() 函数体未闭合")
	}
	body := text[start : start+end]
	if !strings.Contains(body, "setupSlogFacade()") {
		t.Fatal("main() 函数体内缺少 setupSlogFacade() 装配调用:slog 门面未接线,全部 slog 调用点退回文本 handler 且不受 /loglevel 管辖")
	}
}
