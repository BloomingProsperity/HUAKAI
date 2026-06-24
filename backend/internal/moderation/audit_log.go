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
	// 合规取证不打折:违规/拦截/计费等非 clean 事件无条件落库,只有 clean(pass)
	// 审计才走 sample_rate_pct 采样。否则运营调低采样率会按比例丢失违规取证记录,
	// 与 screener.go 的承诺"命中(block)的审计仍无条件写——取证不打折"矛盾。
	if isSampleableDecision(event.Decision) && !ShouldSample(sampleKey(event), cfg.SampleRatePct) {
		return nil
	}
	event.ReasonCode = safeReasonCode(event.ReasonCode, event.Decision)
	_, err := l.sink.InsertModerationLog(ctx, event)
	return err
}

// isSampleableDecision 报告某条审计事件是否允许被采样丢弃。只有 clean 放行
// (pass / 空 decision)是纯噪音、可采样;一切非 clean 决定(block_keyword /
// block_hash / block_external / block_backend / fee_charged 等)都是取证或
// money-coupled 记录,必须无条件落库,绝不因采样率被丢。
func isSampleableDecision(decision Decision) bool {
	switch decision {
	case "", DecisionPass:
		return true
	default:
		return false
	}
}

func safeReasonCode(reason string, decision Decision) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return string(decision)
	}
	// rune 安全:裸 reason[:128] 切半中文 → 审计行 INSERT 失败丢失。
	return textsafe.TruncateBytes(reason, 128)
}
