package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 守 streaming-P1:AI relay 数据面路径不被套连接级总超时，否则长流/长推理会被砍断；
// 控制面路径保留总超时。
// Mutation: 去掉 isAIRelayPath 豁免 → relay 路径也带 deadline → 本断言红。
func TestAIAwareTimeout_ExemptsRelayPathsFromTotalTimeout(t *testing.T) {
	mw := aiAwareTimeout(50 * time.Millisecond)
	hasDeadline := func(method, path string) bool {
		var dl bool
		mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			_, dl = r.Context().Deadline()
		})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))
		return dl
	}
	if hasDeadline("POST", "/v1/chat/completions") {
		t.Fatal("/v1/chat/completions must NOT carry a total-timeout deadline (would cut long streams/reasoning)")
	}
	if hasDeadline("POST", "/v1/messages") {
		t.Fatal("/v1/messages must NOT carry a total-timeout deadline")
	}
	if hasDeadline("POST", "/v1/audio/speech") {
		t.Fatal("/v1/audio/speech must NOT carry a total-timeout deadline")
	}
	if hasDeadline("POST", "/v1/rerank") {
		t.Fatal("/v1/rerank must NOT carry a total-timeout deadline")
	}
	if !hasDeadline("GET", "/v1/admin/pools") {
		t.Fatal("control-plane path must keep the total-timeout deadline")
	}
}
