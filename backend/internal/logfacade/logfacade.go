// Package logfacade 把全局 slog 默认 logger 统一成与 zap 同构的生产形态:
// JSON 输出、级别由 loglevel.Level 单一真相源驱动(/loglevel 热调对 zap 与 slog
// 同时生效)、常驻 service/env/version 字段、attr 值经 privacy 禁写扫描 fail-closed。
// main 启动期 slog.SetDefault 装配一次,全部存量 slog 调用点零改动升级。
package logfacade

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"

	"go.uber.org/zap/zapcore"

	"github.com/BloomingProsperity/HUAKAI/internal/loglevel"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

// redactedMarker 替换命中禁写扫描的 attr 值;检测逻辑复用 privacy 包,本包不新造。
const redactedMarker = "[REDACTED]"

// Options 控制门面的输出流与常驻字段。级别永远取 loglevel.Level,不提供注入点:
// 单一级别真相源正是本包存在的目的。
type Options struct {
	// Writer 为 nil 时落 os.Stderr,与 main 的 zap 生产配置同流,便于采集器统一。
	Writer  io.Writer
	Service string
	Env     string
	Version string
}

// New 构造统一门面 logger。
func New(opts Options) *slog.Logger {
	w := opts.Writer
	if w == nil {
		w = os.Stderr
	}
	// 内层 JSONHandler 放开到 Debug:级别闸门唯一归外层 handler 的 Enabled。
	inner := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}).
		WithAttrs([]slog.Attr{
			slog.String("service", opts.Service),
			slog.String("env", opts.Env),
			slog.String("version", opts.Version),
		})
	return slog.New(&handler{inner: inner})
}

type handler struct {
	inner slog.Handler
}

// Enabled 桥接两个级别域:查 loglevel.Level(zap AtomicLevel,内部共享指针),
// 因此 /admin/v1/loglevel 热调对 slog 通道即时生效,无需重建 logger。
func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return loglevel.Level.Enabled(zapLevelFor(level))
}

func (h *handler) Handle(ctx context.Context, record slog.Record) error {
	scrubbed := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		scrubbed.AddAttrs(scrubAttr(attr))
		return true
	})
	return h.inner.Handle(ctx, scrubbed)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		clean = append(clean, scrubAttr(attr))
	}
	return &handler{inner: h.inner.WithAttrs(clean)}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{inner: h.inner.WithGroup(name)}
}

// zapLevelFor 按阈值把 slog 级别折算到 zap 的四档;两域仅这四档语义重合,
// 阈值间自定义级别就近归入其下界档位。
func zapLevelFor(level slog.Level) zapcore.Level {
	switch {
	case level >= slog.LevelError:
		return zapcore.ErrorLevel
	case level >= slog.LevelWarn:
		return zapcore.WarnLevel
	case level >= slog.LevelInfo:
		return zapcore.InfoLevel
	default:
		return zapcore.DebugLevel
	}
}

// scrubAttr 对 attr 值做 privacy 禁写扫描,命中即整值替换(fail-closed)。
// 消息与 key 不扫:按 slog 约定两者是编译期常量,动态数据只进 attr 值;
// 且禁写标记含宽词(如 credential),扫消息会把正常运维消息整条打掉。
func scrubAttr(attr slog.Attr) slog.Attr {
	value := attr.Value.Resolve()
	switch value.Kind() {
	case slog.KindString:
		if privacy.ContainsForbiddenRawData([]byte(value.String())) {
			return slog.String(attr.Key, redactedMarker)
		}
	case slog.KindGroup:
		members := value.Group()
		clean := make([]slog.Attr, 0, len(members))
		for _, member := range members {
			clean = append(clean, scrubAttr(member))
		}
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(clean...)}
	case slog.KindAny:
		// error 特判:JSONHandler 会把多数 error 序列化成 {},丢掉错误文本;
		// 这里先取文本再扫,保住 TextHandler 时代的可观测性。
		if err, ok := value.Any().(error); ok {
			text := err.Error()
			if privacy.ContainsForbiddenRawData([]byte(text)) {
				return slog.String(attr.Key, redactedMarker)
			}
			return slog.String(attr.Key, text)
		}
		raw, jerr := json.Marshal(value.Any())
		if jerr != nil || privacy.ContainsForbiddenRawData(raw) {
			return slog.String(attr.Key, redactedMarker)
		}
	}
	return slog.Attr{Key: attr.Key, Value: value}
}
