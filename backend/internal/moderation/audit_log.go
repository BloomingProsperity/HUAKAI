package moderation

import (
	"github.com/BloomingProsperity/HUAKAI/internal/textsafe"

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
	// rune 安全:裸 reason[:128] 切半中文 → 审计行 INSERT 失败丢失。
	return textsafe.TruncateBytes(reason, 128)
}
