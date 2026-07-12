package logsink

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"go.uber.org/zap/zapcore"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

// 两栈采集挂钩:slog 走 logfacade Tap(记录已脱敏),zap 走 Core 包装
// (zap 侧无门面脱敏,此处对 message 与字段值做 privacy 扫描,纵深防御)。

const redactedMarker = "[REDACTED]"

// SlogTap 返回可挂到 logfacade Options.Tap 的回调:warn+ 记录转 Entry 入队。
func SlogTap(s *Sink) func(context.Context, slog.Record) {
	return func(_ context.Context, record slog.Record) {
		if record.Level < slog.LevelWarn {
			return
		}
		e := Entry{
			Time:      record.Time,
			Level:     "warn",
			Component: "slog",
			Message:   record.Message,
			Attrs:     make(map[string]any, record.NumAttrs()),
		}
		if record.Level >= slog.LevelError {
			e.Level = "error"
		}
		record.Attrs(func(attr slog.Attr) bool {
			captureAttr(&e, attr.Key, attr.Value.Resolve())
			return true
		})
		s.Enqueue(e)
	}
}

func captureAttr(e *Entry, key string, value slog.Value) {
	switch key {
	case "component":
		e.Component = value.String()
		return
	case "request_id", "logical_request_id":
		e.RequestID = value.String()
		return
	}
	if value.Kind() == slog.KindGroup {
		members := value.Group()
		group := make(map[string]any, len(members))
		for _, m := range members {
			group[m.Key] = attrAny(m.Value.Resolve())
		}
		e.Attrs[key] = group
		return
	}
	e.Attrs[key] = attrAny(value)
}

// attrAny 把 slog 值转成 JSON 可编码形态;不可编码值降级为 String() 文本。
func attrAny(value slog.Value) any {
	v := value.Any()
	if _, err := json.Marshal(v); err != nil {
		return value.String()
	}
	return v
}

// NewZapCore 包装 zap Core:主输出照旧,warn+ 记录旁路转 Entry 入队。
// 用法:logger.WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
// return logsink.NewZapCore(c, sink) }))。
func NewZapCore(inner zapcore.Core, s *Sink) zapcore.Core {
	return &sinkCore{Core: inner, sink: s}
}

type sinkCore struct {
	zapcore.Core
	sink   *Sink
	fields []zapcore.Field
}

func (c *sinkCore) With(fields []zapcore.Field) zapcore.Core {
	merged := make([]zapcore.Field, 0, len(c.fields)+len(fields))
	merged = append(merged, c.fields...)
	merged = append(merged, fields...)
	return &sinkCore{Core: c.Core.With(fields), sink: c.sink, fields: merged}
}

func (c *sinkCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

func (c *sinkCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	if ent.Level >= zapcore.WarnLevel {
		c.capture(ent, fields)
	}
	return c.Core.Write(ent, fields)
}

// capture 把 zap 记录转 Entry。panic 隔离:旁路任何异常不影响主输出。
func (c *sinkCore) capture(ent zapcore.Entry, fields []zapcore.Field) {
	defer func() { _ = recover() }()
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range c.fields {
		f.AddTo(enc)
	}
	for _, f := range fields {
		f.AddTo(enc)
	}
	e := Entry{
		Time:      ent.Time,
		Level:     "warn",
		Component: ent.LoggerName,
		Message:   scrubText(ent.Message),
		Attrs:     make(map[string]any, len(enc.Fields)),
	}
	if e.Component == "" {
		e.Component = "zap"
	}
	if ent.Level >= zapcore.ErrorLevel {
		e.Level = "error"
	}
	for k, v := range enc.Fields {
		switch k {
		case "request_id", "logical_request_id":
			e.RequestID = fmt.Sprint(v)
		default:
			e.Attrs[k] = scrubValue(v)
		}
	}
	c.sink.Enqueue(e)
}

// scrubValue 对字段值做 privacy 禁写扫描(zap 侧没有 logfacade 那层脱敏)。
func scrubValue(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return scrubText(fmt.Sprint(v))
	}
	if privacy.ContainsForbiddenRawData(raw) {
		return redactedMarker
	}
	return v
}

func scrubText(s string) string {
	if privacy.ContainsForbiddenRawData([]byte(s)) {
		return redactedMarker
	}
	return s
}
