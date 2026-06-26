package hermes

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newBoundedTestRunner(t *testing.T, url string, httpClient *http.Client) *RunnerClient {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	c, err := NewRunnerClient(RunnerConfig{
		RunnerURL: url, JWTPrivateKey: priv, JWTKID: "kid-test", HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("NewRunnerClient: %v", err)
	}
	return c
}

// TestRunnerClient_DefaultEgressIsBounded 证明生产中的回退是
// 有界 client,而非 http.DefaultClient:一个带有连接/TLS/
// 响应头超时,并且关键在于不设总 Client.Timeout(它会
// 截断 SSE 流)的 Transport。变异:把 NewRunnerClient 的回退改回
// http.DefaultClient -> Transport 为 nil / Timeout 断言失败 -> 变红。
func TestRunnerClient_DefaultEgressIsBounded(t *testing.T) {
	c := newBoundedTestRunner(t, "http://runner.local", nil) // 不注入 client -> 走回退

	if c.httpClient == http.DefaultClient {
		t.Fatal("runner egress must not fall back to the unbounded http.DefaultClient")
	}
	if c.httpClient.Timeout != 0 {
		t.Fatalf("runner client must NOT set a total timeout (would truncate SSE); got %v", c.httpClient.Timeout)
	}
	tr, ok := c.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("runner client transport must be a bounded *http.Transport, got %T", c.httpClient.Transport)
	}
	if tr.ResponseHeaderTimeout != runnerResponseHeaderTimeout || tr.ResponseHeaderTimeout <= 0 {
		t.Fatalf("ResponseHeaderTimeout must be bounded (%v), got %v", runnerResponseHeaderTimeout, tr.ResponseHeaderTimeout)
	}
	if tr.TLSHandshakeTimeout <= 0 {
		t.Fatalf("TLSHandshakeTimeout must be bounded, got %v", tr.TLSHandshakeTimeout)
	}
}

// TestRunnerClient_ResponseHeaderTimeoutFiresOnHungRunner 是自证式的
// 行为测试:面对一个接受连接但在
// 发送响应头之前卡住的 runner,有界 client 必须快速失败(在其
// 响应头超时处),而无界的 http.DefaultClient 则会等满
// 整个卡顿时间。证明该超时确实在保护共享资源。
// 变异:去掉 ResponseHeaderTimeout(用 DefaultClient)-> 有界分支
// 也会等满整个卡顿时间 -> “快速失败”断言失败 -> 变红。
func TestRunnerClient_ResponseHeaderTimeoutFiresOnHungRunner(t *testing.T) {
	const stall = 600 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(stall) // 在写出任何响应头之前挂起
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// 一个带极小响应头超时的有界 client(无需等待真实的 50s 生产边界,
	// 即可确定性地证明该机制)。
	bounded := &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 50 * time.Millisecond}}
	c := newBoundedTestRunner(t, srv.URL, bounded)

	start := time.Now()
	resp, err := c.Chat(context.Background(), 1, 1, []byte(`{}`))
	elapsed := time.Since(start)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("bounded client must error when the runner stalls past the response-header timeout")
	}
	if !errors.Is(err, ErrRunnerFailure) {
		t.Fatalf("error must wrap ErrRunnerFailure, got %v", err)
	}
	if elapsed >= stall {
		t.Fatalf("bounded client must fail FAST (<%v), took %v", stall, elapsed)
	}

	// 对照组(自证):无界的 DefaultClient 不会快速失败——
	// 它会等满卡顿时间然后拿到 200。这正是有界 client
	// 所消除的资源拖垮(brown-out)行为。
	ctrl := newBoundedTestRunner(t, srv.URL, http.DefaultClient)
	cstart := time.Now()
	cresp, cerr := ctrl.Chat(context.Background(), 1, 1, []byte(`{}`))
	cElapsed := time.Since(cstart)
	if cresp != nil {
		cresp.Body.Close()
	}
	if cerr != nil {
		t.Fatalf("control DefaultClient should reach the server (no header bound), got %v", cerr)
	}
	if cElapsed < stall {
		t.Fatalf("control must have waited out the stall (>=%v) — the test server isn't stalling; got %v", stall, cElapsed)
	}
}
