package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// stubAuthStore 嵌入 userauth.Store 接口(nil),满足接口但任何方法被调用即 panic。
// registration-disabled 路径在 Register 内于触库前返回 ErrRegistrationDisabled,
// 因此本测试不会触发任何 store 方法。
type stubAuthStore struct{ userauth.Store }

// TestZapAuthEventSinkRecords 直接验证生产 sink 把一条 auth 事件落成结构化日志。
func TestZapAuthEventSinkRecords(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	sink := newAuthEventSink(zap.New(core))
	sink.RecordAuthEvent(context.Background(), gatewayhttp.AuthEvent{
		EventType:   "user_login_failed",
		TenantID:    7,
		IP:          "203.0.113.77",
		UserAgent:   "HUAKAI-AuthAudit-Test/1.0",
		Outcome:     "failure",
		ReasonClass: "invalid_credentials",
	})
	entries := logs.FilterField(zap.String("event_type", "user_login_failed")).All()
	if len(entries) != 1 {
		t.Fatalf("expected 1 recorded auth event, got %d", len(entries))
	}
	if entries[0].Level != zapcore.WarnLevel {
		t.Fatalf("expected failure event at Warn level, got %v", entries[0].Level)
	}
	fields := entries[0].ContextMap()
	if fields["ip"] != "203.0.113.77" {
		t.Fatalf("认证事件日志 IP=%v，期望 203.0.113.77", fields["ip"])
	}
	if fields["user_agent"] != "HUAKAI-AuthAudit-Test/1.0" {
		t.Fatalf("认证事件日志 User-Agent=%v，期望 HUAKAI-AuthAudit-Test/1.0", fields["user_agent"])
	}
}

// TestAuthHandlerDepsWiresEventSink 是判别性测试: 通过实际装配路径
// (authHandlerDeps -> MountAuthRoutes) 驱动一个真实 register-disabled 流,
// 断言生产 sink 把 user_register_failed 安全事件落了一条结构化日志。
// mutation: 若 authHandlerDeps 不再注入 EventSink(回到 nil/noop),
// recordAuthEvent 静默丢弃 → observer 0 条 → 本测试变红。
func TestAuthHandlerDepsWiresEventSink(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	authSvc := userauth.NewService(stubAuthStore{})
	authSvc.RegistrationMode = userauth.RegistrationModeDisabled
	d := &deps{userAuth: authSvc}

	r := chi.NewRouter()
	gatewayhttp.MountAuthRoutes(r, authHandlerDeps(d, logger))

	body, _ := json.Marshal(map[string]any{
		"tenant_id": 7,
		"email":     "abuse@example.com",
		"password":  "Sup3rSecret!23",
	})
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	entries := logs.FilterField(zap.String("event_type", "user_register_failed")).All()
	if len(entries) != 1 {
		t.Fatalf("expected production sink to record 1 user_register_failed event, got %d (HTTP %d)", len(entries), rec.Code)
	}
	if entries[0].ContextMap()["reason_class"] != "registration_disabled" {
		t.Fatalf("expected reason_class=registration_disabled, got %v", entries[0].ContextMap()["reason_class"])
	}
}

// TestSessionHandlerDepsWiresEventSink 断言 session 装配也接了非 nil 生产 sink。
// mutation: sessionHandlerDeps 去掉 EventSink 注入 → EventSink==nil → 本测试红。
func TestSessionHandlerDepsWiresEventSink(t *testing.T) {
	deps := sessionHandlerDeps(&deps{}, zap.NewNop())
	if deps.EventSink == nil {
		t.Fatal("sessionHandlerDeps must wire a non-nil EventSink")
	}
}
