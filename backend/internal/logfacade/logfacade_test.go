package logfacade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"go.uber.org/zap/zapcore"

	"github.com/BloomingProsperity/HUAKAI/internal/loglevel"
)

// withLevel 设置全局 loglevel 并注册测试结束还原;本包测试串行(不 t.Parallel),
// 避免全局级别互相踩踏。
func withLevel(t *testing.T, l zapcore.Level) {
	t.Helper()
	orig := loglevel.Level.Level()
	loglevel.Level.SetLevel(l)
	t.Cleanup(func() { loglevel.Level.SetLevel(orig) })
}

func newBufLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	logger := New(Options{Writer: buf, Service: "huakai-gateway", Env: "test", Version: "v-test"})
	return logger, buf
}

// 级别桥接判别:loglevel=Warn 时 Info 关、Warn 开;调回 Debug 后 Debug 开。
// 变异靶:zapLevelFor 的 Info↔Warn 映射写反 → 本测试必红。
func TestEnabledBridgesLoglevel(t *testing.T) {
	ctx := context.Background()
	logger, buf := newBufLogger()

	withLevel(t, zapcore.WarnLevel)
	if logger.Enabled(ctx, slog.LevelInfo) {
		t.Fatal("loglevel=warn 时 Enabled(Info) 应为 false")
	}
	if !logger.Enabled(ctx, slog.LevelWarn) {
		t.Fatal("loglevel=warn 时 Enabled(Warn) 应为 true")
	}
	// 行为面双保险:Info 不落盘,Warn 落盘。
	logger.Info("info-suppressed")
	if buf.Len() != 0 {
		t.Fatalf("loglevel=warn 时 Info 记录不应输出,得到: %s", buf.String())
	}
	logger.Warn("warn-visible")
	if !strings.Contains(buf.String(), "warn-visible") {
		t.Fatalf("loglevel=warn 时 Warn 记录应输出,得到: %s", buf.String())
	}

	loglevel.Level.SetLevel(zapcore.DebugLevel)
	if !logger.Enabled(ctx, slog.LevelDebug) {
		t.Fatal("loglevel=debug 时 Enabled(Debug) 应为 true")
	}
}

// /loglevel 覆盖 slog 判别:同一 logger 实例,热调级别后 Debug 记录从不可见变可见,
// 证明桥接是逐条查询而非构造期快照。
func TestLoglevelHotSwitchAffectsExistingLogger(t *testing.T) {
	logger, buf := newBufLogger()

	withLevel(t, zapcore.InfoLevel)
	logger.Debug("debug-before-switch")
	if buf.Len() != 0 {
		t.Fatalf("loglevel=info 时 Debug 记录不应输出,得到: %s", buf.String())
	}

	loglevel.Level.SetLevel(zapcore.DebugLevel) // 模拟 PUT /admin/v1/loglevel {"level":"debug"}
	logger.Debug("debug-after-switch")
	if !strings.Contains(buf.String(), "debug-after-switch") {
		t.Fatalf("热调到 debug 后 Debug 记录应输出,得到: %s", buf.String())
	}
}

// JSON 输出判别:输出行是合法 JSON,且携带 service/env/version 常驻字段。
// 变异靶:删 New 里的 WithAttrs 全局字段注入 → 本测试必红。
func TestJSONOutputCarriesGlobalFields(t *testing.T) {
	withLevel(t, zapcore.InfoLevel)
	logger, buf := newBufLogger()

	logger.Info("hello", slog.String("mode", "claude_cli"))

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("输出不是合法 JSON: %v; 原文: %s", err, buf.String())
	}
	for key, want := range map[string]string{
		"service": "huakai-gateway",
		"env":     "test",
		"version": "v-test",
		"msg":     "hello",
		"mode":    "claude_cli",
	} {
		if got, _ := line[key].(string); got != want {
			t.Fatalf("字段 %q = %q, 期望 %q; 原文: %s", key, got, want, buf.String())
		}
	}
}

// redactor 判别:含禁写标记的 attr 值(裸串/嵌套 group/Any-map 敏感 key)不落明文。
// 变异靶:scrubAttr 改为原样返回 → 本测试必红。
func TestScrubsForbiddenAttrValues(t *testing.T) {
	withLevel(t, zapcore.InfoLevel)
	const fakeToken = "sk-fake1234567890abcdef"

	logger, buf := newBufLogger()
	logger.Warn("upstream call failed",
		slog.String("detail", fakeToken),
		slog.Group("ctx", slog.String("hint", "bearer "+fakeToken)),
		slog.Any("payload", map[string]any{"access_token": "whatever"}),
	)

	out := buf.String()
	if strings.Contains(out, fakeToken) {
		t.Fatalf("输出泄露假 token 明文: %s", out)
	}
	if strings.Contains(out, "whatever") {
		t.Fatalf("敏感 key 下的 Any 值应整值替换: %s", out)
	}
	if !strings.Contains(out, redactedMarker) {
		t.Fatalf("命中禁写扫描的值应替换为 %s: %s", redactedMarker, out)
	}
}

// 防过度脱敏回归:干净的运维字段与 privacy.LogSystem 形态的已脱敏 event map 必须存活。
func TestCleanAttrsSurvive(t *testing.T) {
	withLevel(t, zapcore.InfoLevel)
	logger, buf := newBufLogger()

	logger.Warn("privacy.system",
		slog.Any("event", map[string]any{"error_class": "upstream_timeout", "component": "forwarder"}),
		slog.String("mode", "claude_cli"),
	)

	out := buf.String()
	for _, want := range []string{"upstream_timeout", "forwarder", "claude_cli"} {
		if !strings.Contains(out, want) {
			t.Fatalf("干净字段 %q 被误脱敏: %s", want, out)
		}
	}
}

// error 值渲染判别:slog.Any("err", err) 在 JSONHandler 下默认序列化成 {},
// 门面必须保住错误文本;含密钥的错误文本则整值替换。
func TestErrorAttrRendersMessage(t *testing.T) {
	withLevel(t, zapcore.InfoLevel)
	logger, buf := newBufLogger()

	logger.Warn("op failed", slog.Any("err", errors.New("upstream dial blocked")))
	if !strings.Contains(buf.String(), "upstream dial blocked") {
		t.Fatalf("error 值应渲染为错误文本: %s", buf.String())
	}

	buf.Reset()
	logger.Warn("op failed", slog.Any("err", errors.New("refresh with sk-fakesecret999")))
	if strings.Contains(buf.String(), "sk-fakesecret999") {
		t.Fatalf("含禁写标记的错误文本不应落明文: %s", buf.String())
	}
}

// WithAttrs/WithGroup 路径判别:logger.With 预绑定的敏感值同样被扫;分组结构保留。
func TestWithAttrsScrubbedAndGroupPreserved(t *testing.T) {
	withLevel(t, zapcore.InfoLevel)
	logger, buf := newBufLogger()

	logger.With(slog.String("bound", "bearer sk-boundsecret")).
		WithGroup("req").Warn("x", slog.String("route_id", "r1"))

	out := buf.String()
	if strings.Contains(out, "sk-boundsecret") {
		t.Fatalf("With 预绑定的敏感值不应落明文: %s", out)
	}
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("输出不是合法 JSON: %v", err)
	}
	group, ok := line["req"].(map[string]any)
	if !ok || group["route_id"] != "r1" {
		t.Fatalf("WithGroup 分组结构应保留: %s", out)
	}
}

// 默认 writer 判别:Writer 为 nil 时不 panic 且落 stderr(与 zap 同流);
// 这里只验证构造与发射不炸,流向由 New 的实现直读 os.Stderr 保证。
func TestNilWriterDefaultsSafely(t *testing.T) {
	withLevel(t, zapcore.ErrorLevel) // 压到 Error,避免测试输出污染 stderr
	logger := New(Options{Service: "s", Env: "e", Version: "v"})
	logger.Info("suppressed")
}

// mustNotPanic 把 panic 转成 Fatal:日志发射绝不允许向调用点抛 panic
// (billing/quota worker loop 无 recover,一条日志能打崩整进程)。
func mustNotPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("日志发射不得 panic,得到: %v", r)
		}
	}()
	fn()
}

// nilErrProbe 指针接收者读字段:typed-nil 时调用 Error() 即 nil deref panic。
type nilErrProbe struct{ msg string }

func (p *nilErrProbe) Error() string { return p.msg }

// poisonMarshaler 模拟有毒 MarshalJSON:panic 会穿透 encoding/json 的内部 recover。
type poisonMarshaler struct{}

func (poisonMarshaler) MarshalJSON() ([]byte, error) { panic("poison marshaler") }

// panic 安全判别:typed-nil error(err != nil 但内部指针为 nil)过门面不得 panic,
// 且该值 fail-closed 替换。变异靶:去掉 safeErrorText 的 recover → 本测试必红(崩溃即 Fatal)。
func TestTypedNilErrorDoesNotPanic(t *testing.T) {
	withLevel(t, zapcore.InfoLevel)
	logger, buf := newBufLogger()

	var probe *nilErrProbe
	var err error = probe // typed-nil:接口非 nil,内部指针 nil
	mustNotPanic(t, func() {
		logger.Warn("op failed", slog.Any("err", err))
	})
	if !strings.Contains(buf.String(), "op failed") {
		t.Fatalf("记录本体应照常输出(降级而非丢弃): %s", buf.String())
	}
	if !strings.Contains(buf.String(), redactedMarker) {
		t.Fatalf("有毒 error 值应 fail-closed 替换: %s", buf.String())
	}
}

// panic 安全判别:有毒 MarshalJSON 过门面不得 panic,值 fail-closed 替换。
// 变异靶:去掉 safeMarshal 的 recover → 本测试必红。
func TestPoisonMarshalerDoesNotPanic(t *testing.T) {
	withLevel(t, zapcore.InfoLevel)
	logger, buf := newBufLogger()

	mustNotPanic(t, func() {
		logger.Warn("op failed", slog.Any("payload", poisonMarshaler{}))
	})
	if !strings.Contains(buf.String(), "op failed") {
		t.Fatalf("记录本体应照常输出: %s", buf.String())
	}
	if !strings.Contains(buf.String(), redactedMarker) {
		t.Fatalf("有毒 Marshaler 值应 fail-closed 替换: %s", buf.String())
	}
}

// 一等凭证形态判别:本仓自己签发的 hk_* key(admin/keygen.go)、裸 JWT、GitHub PAT
// 过门面必须被抹掉。变异靶:从 privacy 标记表删 hk_live_ 等新条目 → 本测试必红。
func TestScrubsFirstPartyKeyForms(t *testing.T) {
	withLevel(t, zapcore.InfoLevel)
	logger, buf := newBufLogger()

	logger.Warn("key echo",
		slog.String("k1", "hk_live_abcdefgh23456789abcdefgh"),
		slog.String("k2", "eyJhbGciOiJIUzI1NiJ9.fake.fake"),
		slog.String("k3", "ghp_FAKEFAKEFAKEFAKE"),
	)
	out := buf.String()
	for _, leak := range []string{"hk_live_", "eyJhbGci", "ghp_FAKE"} {
		if strings.Contains(out, leak) {
			t.Fatalf("一等凭证形态 %q 落了明文: %s", leak, out)
		}
	}
}

// map key 扫描判别:秘密出现在 JSON map key 位(Any-map 的 key / 字符串值恰为
// 合法 JSON 时树扫描的 key)同样必须命中。变异靶:去掉 privacy
// containsForbiddenValue 的 key 子串扫 → 本测试必红。
func TestScrubsSecretsInMapKeys(t *testing.T) {
	withLevel(t, zapcore.InfoLevel)
	logger, buf := newBufLogger()

	logger.Warn("x",
		slog.Any("m", map[string]int{"sk-live-FAKE": 3}),
		slog.String("s", `{"sk-live-FAKE":7}`),
	)
	if strings.Contains(buf.String(), "sk-live-FAKE") {
		t.Fatalf("map key 承载的秘密落了明文: %s", buf.String())
	}
}
