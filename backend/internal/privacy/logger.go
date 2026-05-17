package privacy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
)

type Severity string

const (
	SeverityDebug    Severity = "debug"
	SeverityInfo     Severity = "info"
	SeverityWarn     Severity = "warn"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical"
)

type SystemEvent struct {
	Severity   Severity
	Component  string
	RequestID  string
	ErrorClass string
	PanicClass string
	Attrs      map[string]any
}

type UserActionEvent struct {
	TenantID    int64
	RequestID   string
	EventClass  string
	ReasonClass string
	Attrs       map[string]any
}

type SecurityEvent struct {
	TenantID   int64
	RequestID  string
	EventClass string
	ActorIDRef string
	Attrs      map[string]any
}

type SystemLogger interface {
	LogSystem(context.Context, SystemEvent) error
}

type UserActionLogger interface {
	LogUserAction(context.Context, UserActionEvent) error
}

type SecurityLogger interface {
	LogSecurity(context.Context, SecurityEvent) error
}

type ExternalSink interface {
	WritePrivacyEvent(context.Context, []byte) error
}

type Logger struct {
	redactor Redactor
	system   *slog.Logger
	userSink ExternalSink
	secSink  ExternalSink
}

func NewLogger(redactor Redactor, systemOut io.Writer, userSink, secSink ExternalSink) *Logger {
	if redactor == nil {
		redactor = DefaultRedactor()
	}
	if systemOut == nil {
		systemOut = os.Stdout
	}
	return &Logger{
		redactor: redactor,
		system:   slog.New(slog.NewJSONHandler(systemOut, nil)),
		userSink: userSink,
		secSink:  secSink,
	}
}

func NewStdoutSystemLogger(redactor Redactor) *Logger {
	return NewLogger(redactor, os.Stdout, nil, nil)
}

func (l *Logger) LogSystem(ctx context.Context, event SystemEvent) error {
	if l == nil {
		l = NewStdoutSystemLogger(nil)
	}
	payload := map[string]any{
		"schema_version": SchemaVersion,
		"channel":        "system",
		"severity":       normalizeSeverity(event.Severity),
		"component":      event.Component,
		"request_id":     event.RequestID,
		"error_class":    event.ErrorClass,
		"panic_class":    event.PanicClass,
	}
	for k, v := range event.Attrs {
		payload[k] = v
	}
	raw, err := l.redactor.SanitizePayload(ctx, payload)
	if err != nil {
		raw = BlockedPayload(ErrorClassPrivacyGuardHit)
	}
	var attrs map[string]any
	_ = jsonUnmarshal(raw, &attrs)
	l.system.LogAttrs(ctx, slogLevel(event.Severity), "privacy.system",
		slog.Any("event", attrs),
	)
	return err
}

func (l *Logger) LogUserAction(ctx context.Context, event UserActionEvent) error {
	if l == nil {
		l = NewStdoutSystemLogger(nil)
	}
	payload := map[string]any{
		"schema_version": SchemaVersion,
		"channel":        "user_action",
		"tenant_id":      event.TenantID,
		"request_id":     event.RequestID,
		"event_class":    event.EventClass,
		"reason_class":   event.ReasonClass,
	}
	for k, v := range event.Attrs {
		payload[k] = v
	}
	raw, err := l.redactor.SanitizePayload(ctx, payload)
	if err != nil {
		raw = BlockedPayload(ErrorClassPrivacyGuardHit)
	}
	if l.userSink != nil {
		return l.userSink.WritePrivacyEvent(ctx, raw)
	}
	return err
}

func (l *Logger) LogSecurity(ctx context.Context, event SecurityEvent) error {
	if l == nil {
		l = NewStdoutSystemLogger(nil)
	}
	payload := map[string]any{
		"schema_version": SchemaVersion,
		"channel":        "security",
		"tenant_id":      event.TenantID,
		"request_id":     event.RequestID,
		"event_class":    event.EventClass,
		"actor_id_ref":   event.ActorIDRef,
	}
	for k, v := range event.Attrs {
		payload[k] = v
	}
	raw, err := l.redactor.SanitizePayload(ctx, payload)
	if err != nil {
		raw = BlockedPayload(ErrorClassPrivacyGuardHit)
	}
	if l.secSink != nil {
		return l.secSink.WritePrivacyEvent(ctx, raw)
	}
	return err
}

func normalizeSeverity(sev Severity) string {
	switch sev {
	case SeverityDebug, SeverityInfo, SeverityWarn, SeverityError, SeverityCritical:
		return string(sev)
	default:
		return string(SeverityInfo)
	}
}

func slogLevel(sev Severity) slog.Level {
	switch sev {
	case SeverityDebug:
		return slog.LevelDebug
	case SeverityWarn:
		return slog.LevelWarn
	case SeverityError, SeverityCritical:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func jsonUnmarshal(raw []byte, out any) error {
	return json.Unmarshal(raw, out)
}

var _ SystemLogger = (*Logger)(nil)
var _ UserActionLogger = (*Logger)(nil)
var _ SecurityLogger = (*Logger)(nil)
