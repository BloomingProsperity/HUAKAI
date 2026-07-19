package logsink

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"go.uber.org/zap/zapcore"

	"github.com/BloomingProsperity/HUAKAI/internal/logcontract"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

// 两栈采集挂钩:slog 走 logfacade Tap(记录已脱敏),zap 走 Core 包装
// (zap 侧无门面脱敏,此处对 message 与字段值做 privacy 扫描,纵深防御)。

const redactedMarker = "[REDACTED]"

// SlogTap 返回可挂到 logfacade Options.Tap 的回调。显式分类 Info 与全部 Warn/Error
// 转成统一 Entry；是否最终入队由 Sink 的合同校验决定。
func SlogTap(s *Sink) func(context.Context, slog.Record) {
	return func(_ context.Context, record slog.Record) {
		if record.Level < slog.LevelInfo {
			return
		}
		if record.Level < slog.LevelWarn && !slogRecordHasContract(record) {
			return
		}
		e := Entry{
			Time:      record.Time,
			Level:     "info",
			Component: "slog",
			Message:   scrubText(record.Message),
			Attrs:     make(map[string]any, record.NumAttrs()),
		}
		if record.Level >= slog.LevelError {
			e.Level = "error"
		} else if record.Level >= slog.LevelWarn {
			e.Level = "warn"
		}
		record.Attrs(func(attr slog.Attr) bool {
			captureAttr(&e, attr.Key, attr.Value.Resolve())
			return true
		})
		s.Enqueue(e)
	}
}

func slogRecordHasContract(record slog.Record) bool {
	hasCategory := false
	hasEventType := false
	record.Attrs(func(attr slog.Attr) bool {
		switch attr.Key {
		case logcontract.FieldCategory:
			hasCategory = true
		case logcontract.FieldEventType:
			hasEventType = true
		}
		return !(hasCategory && hasEventType)
	})
	return hasCategory && hasEventType
}

func captureAttr(e *Entry, key string, value slog.Value) {
	if value.Kind() == slog.KindGroup {
		members := value.Group()
		group := make(map[string]any, len(members))
		for _, m := range members {
			group[m.Key] = scrubValue(attrAny(m.Value.Resolve()))
		}
		e.Attrs[key] = group
		return
	}
	captureAny(e, key, attrAny(value))
}

// attrAny 把 slog 值转成 JSON 可编码形态;不可编码值降级为 String() 文本。
func attrAny(value slog.Value) any {
	v := value.Any()
	if _, err := json.Marshal(v); err != nil {
		return value.String()
	}
	return v
}

// NewZapCore 包装 zap Core：主输出照旧，显式分类 Info 与全部 Warn/Error 旁路入队。
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
	if ent.Level >= zapcore.WarnLevel ||
		(ent.Level >= zapcore.InfoLevel && zapFieldsHaveContract(c.fields, fields)) {
		c.capture(ent, fields)
	}
	return c.Core.Write(ent, fields)
}

func zapFieldsHaveContract(base, fields []zapcore.Field) bool {
	hasCategory := false
	hasEventType := false
	for _, collection := range [][]zapcore.Field{base, fields} {
		for _, field := range collection {
			switch field.Key {
			case logcontract.FieldCategory:
				hasCategory = true
			case logcontract.FieldEventType:
				hasEventType = true
			}
			if hasCategory && hasEventType {
				return true
			}
		}
	}
	return false
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
		Level:     "info",
		Component: ent.LoggerName,
		Message:   scrubText(ent.Message),
		Attrs:     make(map[string]any, len(enc.Fields)),
	}
	if e.Component == "" {
		e.Component = "zap"
	}
	if ent.Level >= zapcore.ErrorLevel {
		e.Level = "error"
	} else if ent.Level >= zapcore.WarnLevel {
		e.Level = "warn"
	}
	for k, v := range enc.Fields {
		captureAny(&e, k, v)
	}
	c.sink.Enqueue(e)
}

func captureAny(e *Entry, key string, value any) {
	text := func() string { return scrubText(strings.TrimSpace(fmt.Sprint(value))) }
	switch key {
	case "component":
		e.Component = text()
	case "request_id", "logical_request_id":
		e.RequestID = text()
	case logcontract.FieldCategory:
		e.Category = text()
	case logcontract.FieldEventType:
		e.EventType = text()
	case logcontract.FieldResult:
		e.Result = text()
	case logcontract.FieldErrorClass:
		e.ErrorClass = text()
	case logcontract.FieldErrorCode:
		e.ErrorCode = text()
	case logcontract.FieldRetryable:
		e.Retryable = boolValue(value)
	case logcontract.FieldActorKind:
		e.ActorKind = text()
	case logcontract.FieldActorRef:
		e.ActorRef = text()
	case logcontract.FieldTenantID:
		if tenantID, ok := int64Value(value); ok && tenantID > 0 {
			e.TenantID = &tenantID
		}
	case logcontract.FieldTargetType:
		e.TargetType = text()
	case logcontract.FieldTargetRef:
		e.TargetRef = text()
	case logcontract.FieldTraceID:
		e.TraceID = text()
	case logcontract.FieldUpstreamRequestID:
		e.UpstreamRequestID = text()
	case logcontract.FieldIdempotencyKey:
		e.IdempotencyKey = text()
	case logcontract.FieldRecoveryState:
		e.RecoveryState = text()
	default:
		e.Attrs[key] = scrubValue(value)
	}
}

func boolValue(value any) bool {
	if v, ok := value.(bool); ok {
		return v
	}
	v, _ := strconv.ParseBool(strings.TrimSpace(fmt.Sprint(value)))
	return v
}

func int64Value(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		return int64(v), uint64(v) <= uint64(^uint64(0)>>1)
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		if v > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(v), true
	case json.Number:
		parsed, err := v.Int64()
		return parsed, err == nil
	default:
		parsed, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(value)), 10, 64)
		return parsed, err == nil
	}
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
