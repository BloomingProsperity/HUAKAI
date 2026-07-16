package adminhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

func TestProviderAccountRecentRequestsMapsLatencyAndNullableTTFT(t *testing.T) {
	accounts := newProviderAccountHealthStoreStub()
	accounts.put(providerAccountHealthRow(7, 99))
	upstreamModel := "claude-upstream"
	requestedAt := time.Date(2026, 7, 12, 10, 0, 0, 100_000_000, time.UTC)
	requests := &providerAccountRecentRequestsStoreStub{rows: []dbbilling.ListProviderAccountRecentRequestsRow{
		{
			ID: 3, RequestedAt: recentRequestTS(requestedAt), SettledAt: recentRequestTS(requestedAt.Add(1250 * time.Millisecond)),
			RequestedModel: "claude", UpstreamModel: &upstreamModel, EndClass: "stream_end_graceful", Stream: true,
			TokensInput: 11, TokensOutput: 22, UpstreamRequestAt: recentRequestTS(requestedAt.Add(50 * time.Millisecond)),
			FirstByteAt: recentRequestTS(requestedAt.Add(225 * time.Millisecond)), AttemptSeq: 2,
		},
		{
			ID: 2, RequestedAt: recentRequestTS(requestedAt.Add(-time.Second)), SettledAt: recentRequestTS(requestedAt),
			RequestedModel: "claude", EndClass: "upstream_error_5xx", TokensInput: 7, TokensOutput: 0, AttemptSeq: 1,
			UpstreamRequestAt: recentRequestTS(requestedAt.Add(-900 * time.Millisecond)),
		},
	}}

	rec := invokeProviderAccountRecentRequests(t, ProviderAccountRecentRequestsDeps{
		Auth: providerAccountHealthAuthStub{ident: tenantOperator(7)}, Accounts: accounts, Requests: requests,
	}, "/admin/v1/provider-accounts/99/recent-requests")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body providerAccountRecentRequestsResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if body.Source != "settled_usage_records" || len(body.Items) != 2 {
		t.Fatalf("响应=%+v，期望 source 与两条记录", body)
	}
	first := body.Items[0]
	if first.At != "2026-07-12T10:00:00.1Z" || first.Status != "success" || first.LatencyMS != 1250 || first.TTFTMS == nil || *first.TTFTMS != 175 {
		t.Fatalf("首条时延映射=%+v，期望 latency=1250 ttft=175 success", first)
	}
	if first.Model != "claude" || first.UpstreamModel == nil || *first.UpstreamModel != upstreamModel || first.TokensIn != 11 || first.TokensOut != 22 || !first.Stream || first.AttemptSeq != 2 {
		t.Fatalf("首条字段映射=%+v", first)
	}
	second := body.Items[1]
	if second.Status != "error" || second.TTFTMS != nil {
		t.Fatalf("first_byte_at 为空时应映射 error 且 ttft_ms=null，实际=%+v", second)
	}
}

func TestProviderAccountRecentRequestsDefaultAndClampedLimit(t *testing.T) {
	accounts := newProviderAccountHealthStoreStub()
	accounts.put(providerAccountHealthRow(7, 99))
	requests := &providerAccountRecentRequestsStoreStub{}
	deps := ProviderAccountRecentRequestsDeps{
		Auth: providerAccountHealthAuthStub{ident: tenantOperator(7)}, Accounts: accounts, Requests: requests,
	}

	if rec := invokeProviderAccountRecentRequests(t, deps, "/admin/v1/provider-accounts/99/recent-requests"); rec.Code != http.StatusOK {
		t.Fatalf("默认 limit 请求 status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := requests.args[0].RowLimit; got != providerAccountRecentRequestsDefaultLimit {
		t.Fatalf("默认 limit=%d，期望=%d", got, providerAccountRecentRequestsDefaultLimit)
	}
	if rec := invokeProviderAccountRecentRequests(t, deps, "/admin/v1/provider-accounts/99/recent-requests?limit=1000"); rec.Code != http.StatusOK {
		t.Fatalf("钳制 limit 请求 status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := requests.args[1].RowLimit; got != providerAccountRecentRequestsMaxLimit {
		t.Fatalf("超限 limit=%d，期望钳制到=%d", got, providerAccountRecentRequestsMaxLimit)
	}
}

func TestProviderAccountRecentRequestsTenantOverreachIsForbidden(t *testing.T) {
	accounts := newProviderAccountHealthStoreStub()
	accounts.put(providerAccountHealthRow(8, 99))
	requests := &providerAccountRecentRequestsStoreStub{}
	rec := invokeProviderAccountRecentRequests(t, ProviderAccountRecentRequestsDeps{
		Auth: providerAccountHealthAuthStub{ident: tenantOperator(7)}, Accounts: accounts, Requests: requests,
	}, "/admin/v1/provider-accounts/99/recent-requests?tenant_id=8")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("跨租户账号 status=%d，期望 403；body=%s", rec.Code, rec.Body.String())
	}
	// 破坏点→删除目标租户统一裁决时会触达账号或用量 store，本断言转红。
	if len(accounts.getArgs) != 0 {
		t.Fatalf("scope 拒绝后不应查询账号，实际调用=%d", len(accounts.getArgs))
	}
	if len(requests.args) != 0 {
		t.Fatalf("账号租户校验失败后不应查询用量，实际调用=%d", len(requests.args))
	}
}

func invokeProviderAccountRecentRequests(t *testing.T, deps ProviderAccountRecentRequestsDeps, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/provider-accounts", func(r chi.Router) {
		MountProviderAccountRecentRequestsRoutes(r, deps)
	})
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

type providerAccountRecentRequestsStoreStub struct {
	rows []dbbilling.ListProviderAccountRecentRequestsRow
	err  error
	args []dbbilling.ListProviderAccountRecentRequestsParams
}

func (s *providerAccountRecentRequestsStoreStub) ListProviderAccountRecentRequests(_ context.Context, arg dbbilling.ListProviderAccountRecentRequestsParams) ([]dbbilling.ListProviderAccountRecentRequestsRow, error) {
	s.args = append(s.args, arg)
	return s.rows, s.err
}

func recentRequestTS(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
