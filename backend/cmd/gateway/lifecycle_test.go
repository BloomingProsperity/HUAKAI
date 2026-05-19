package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
)

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
