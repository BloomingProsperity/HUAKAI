package moderation

import (
	"context"
	"strings"
)

type AuditSink interface {
	InsertModerationLog(context.Context, ModerationEvent) (int64, error)
}

type DBModerationLogger struct {
	sink AuditSink
}

func NewAuditLogger(sink AuditSink) *DBModerationLogger {
	return &DBModerationLogger{sink: sink}
}

func (l *DBModerationLogger) Log(ctx context.Context, event ModerationEvent, cfg ModerationConfig) error {
	if l == nil || l.sink == nil {
		return nil
	}
	if !ShouldSample(sampleKey(event), cfg.SampleRatePct) {
		return nil
	}
	event.ReasonCode = safeReasonCode(event.ReasonCode, event.Decision)
	_, err := l.sink.InsertModerationLog(ctx, event)
	return err
}

func safeReasonCode(reason string, decision Decision) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return string(decision)
	}
	if len(reason) > 128 {
		return reason[:128]
	}
	return reason
}
