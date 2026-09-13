package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
)

type stubScreener struct {
	result ScreenResult
	err    error
	calls  int
}

func (s *stubScreener) Screen(context.Context, ScreenRequest) (ScreenResult, error) {
	s.calls++
	return s.result, s.err
}

func TestApplyHTTP_NilScreenerPasses(t *testing.T) {
	rec := httptest.NewRecorder()
	if !ApplyHTTP(rec, context.Background(), nil, ScreenRequest{}) {
		t.Fatal("nil screener 应放行")
	}
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("nil screener 不应写响应: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestApplyHTTP_BlockWritesStable403(t *testing.T) {
	// 变异：把 403 改成 500 或吞掉阻断，会让策略拒绝看起来像后端故障。
	screener := &stubScreener{result: ScreenResult{Decision: DecisionBlockKeyword, ReasonCode: "keyword_match"}}
	rec := httptest.NewRecorder()
	if ApplyHTTP(rec, context.Background(), screener, ScreenRequest{RequestID: "req-1"}) {
		t.Fatal("命中关键词应阻断")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), clienterr.CodeContentPolicyViolation) {
		t.Fatalf("body=%s want content_policy_violation", rec.Body.String())
	}
	var payload map[string]map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v", err)
	}
	if payload["error"]["message"] != clienterr.MessageFor(clienterr.CodeContentPolicyViolation) {
		t.Fatalf("message=%q", payload["error"]["message"])
	}
}

func TestScreenBody_BackendErrorMapsToPolicyBlock(t *testing.T) {
	screener := &stubScreener{err: errors.New("store down")}
	if err := ScreenBody(context.Background(), screener, ScreenRequest{}); !errors.Is(err, ErrPolicyBlocked) {
		t.Fatalf("err=%v want ErrPolicyBlocked", err)
	}
}
