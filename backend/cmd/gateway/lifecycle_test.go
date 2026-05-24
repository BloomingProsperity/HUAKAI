package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
)

// TestNewGatewayServerHasReadAndIdleTimeouts 守 P1-B 修复:
// *http.Server 必须设 ReadTimeout 防 slowloris-style 慢 body 攻击,
// 必须设 IdleTimeout 防 keep-alive 闲连接耗尽。
// Mutation:删 ReadTimeout 或 IdleTimeout 时本用例必红。
// 同时验证 WriteTimeout 必为 0(SSE 流可以长达数分钟,不能用 WriteTimeout 砍断)。
func TestNewGatewayServerHasReadAndIdleTimeouts(t *testing.T) {
	srv := newGatewayServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	if srv.ReadHeaderTimeout <= 0 {
		t.Fatalf("ReadHeaderTimeout must be > 0, got %v", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout <= 0 {
		t.Fatalf("ReadTimeout must be > 0 to defend against slow-body attacks, got %v", srv.ReadTimeout)
	}
	if srv.ReadTimeout < srv.ReadHeaderTimeout {
		t.Fatalf("ReadTimeout (%v) must be >= ReadHeaderTimeout (%v)", srv.ReadTimeout, srv.ReadHeaderTimeout)
	}
	if srv.IdleTimeout <= 0 {
		t.Fatalf("IdleTimeout must be > 0 to defend against keep-alive exhaustion, got %v", srv.IdleTimeout)
	}
	if srv.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout must be 0; SSE streams need unbounded write window, got %v", srv.WriteTimeout)
	}
}

func TestServeGatewayReturnsListenAndServeError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	srv := newGatewayServer("127.0.0.1:not-a-port", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	err := serveGateway(ctx, srv, &gatewayRuntime{}, cancel, zaptest.NewLogger(t))
	if err == nil {
		t.Fatal("serveGateway returned nil; want ListenAndServe error")
	}
	if !strings.Contains(err.Error(), "not-a-port") && !strings.Contains(err.Error(), "unknown port") {
		t.Fatalf("serveGateway err=%v; want ListenAndServe address error", err)
	}
}
