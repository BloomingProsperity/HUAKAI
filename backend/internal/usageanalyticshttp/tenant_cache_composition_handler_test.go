package usageanalyticshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	dboverview "github.com/BloomingProsperity/HUAKAI/internal/db/usageoverview"
)

func (s *tenantOverviewQueryStub) AggregateTenantUsageCacheComposition(_ context.Context, arg dboverview.AggregateTenantUsageCacheCompositionParams) (dboverview.AggregateTenantUsageCacheCompositionRow, error) {
	s.mu.Lock()
	s.calls = append(s.calls, "composition")
	s.tenants = append(s.tenants, arg.TenantID)
	fail := s.fail
	s.mu.Unlock()
	if fail != nil {
		return dboverview.AggregateTenantUsageCacheCompositionRow{}, fail
	}
	var requests, upstream, hits, created, read int64
	createCost := decimal.Zero
	readCost := decimal.Zero
	hitCost := decimal.Zero
	for _, event := range s.events {
		if event.tenantID != arg.TenantID {
			continue
		}
		if arg.SettledSince.Valid && event.settledAt.Before(arg.SettledSince.Time) {
			continue
		}
		requests++
		source := event.settlementSource
		if source == "" {
			source = billing.SettlementSourceProviderUpstream
		}
		switch source {
		case billing.SettlementSourceResponseCacheL2:
			hits++
			hitCost = hitCost.Add(event.cost)
		case billing.SettlementSourceProviderUpstream:
			upstream++
			created += event.cacheCreated
			read += event.cacheRead
			createCost = createCost.Add(event.cacheCreateCost)
			readCost = readCost.Add(event.cacheReadCost)
		}
	}
	return dboverview.AggregateTenantUsageCacheCompositionRow{
		RequestCount:              requests,
		UpstreamRequests:          upstream,
		ResponseCacheHits:         hits,
		PromptCacheCreationTokens: created,
		PromptCacheReadTokens:     read,
		PromptCacheCreationCost:   createCost.StringFixed(8),
		PromptCacheReadCost:       readCost.StringFixed(8),
		ResponseCacheCost:         hitCost.StringFixed(8),
	}, nil
}

func twoTenantCacheCompositionEvents(now time.Time) []tenantOverviewEvent {
	recent := now.Add(-30 * time.Minute)
	return []tenantOverviewEvent{
		{tenantID: 7, overviewSeedEvent: overviewSeedEvent{
			userID: 101, apiKeyID: 1001, settledAt: recent,
			cost: decimal.RequireFromString("4.50"), tokensInput: 100, tokensOutput: 200,
			cacheCreated: 20, cacheRead: 30,
			cacheCreateCost: decimal.RequireFromString("0.30"), cacheReadCost: decimal.RequireFromString("0.10"),
			endClass: "stream_end_graceful",
		}},
		{tenantID: 7, overviewSeedEvent: overviewSeedEvent{
			userID: 101, apiKeyID: 1001, settledAt: recent.Add(time.Minute),
			cost: decimal.Zero, tokensInput: 8, tokensOutput: 2,
			cacheCreated: 99, cacheRead: 88,
			endClass:         "non_streaming",
			settlementSource: billing.SettlementSourceResponseCacheL2,
		}},
		{tenantID: 9, overviewSeedEvent: overviewSeedEvent{
			userID: 202, apiKeyID: 2002, settledAt: recent,
			cost: decimal.RequireFromString("90.00"), tokensInput: 4000, tokensOutput: 5000,
			cacheCreated: 800, cacheRead: 900,
			endClass: "non_streaming",
		}},
	}
}

func decodeTenantCacheComposition(t *testing.T, rec *http.Response) cacheCompositionResponse {
	t.Helper()
	var body cacheCompositionResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("解析缓存构成: %v", err)
	}
	return body
}

func TestTenantCacheCompositionOperatorSeesOnlyOwnTenant(t *testing.T) {
	now := time.Now().UTC()
	store := &tenantOverviewQueryStub{events: twoTenantCacheCompositionEvents(now)}
	h := NewTenantCacheCompositionHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		store,
	)
	// 变异:把 L2 草稿 Token 加进提示缓存，或吃到租户 9 -> 红。
	rec := invoke(h, "/admin/v1/usage/cache-composition?window=19h")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码=%d 期望 200 体=%s", rec.Code, rec.Body.String())
	}
	body := decodeTenantCacheComposition(t, rec.Result())
	if body.TenantID != 7 {
		t.Fatalf("tenant_id=%d 期望 7", body.TenantID)
	}
	if body.Composition.PromptCacheCreationTokens != 20 || body.Composition.PromptCacheReadTokens != 30 {
		t.Fatalf("提示缓存=%+v 必须只要上游 20/30，不得含 L2 草稿 99/88", body.Composition)
	}
	if body.Composition.ResponseCacheHits != 1 || body.Composition.ResponseCacheCost != "0.00000000" {
		t.Fatalf("响应缓存命中=%+v 必须单独一栏且费用为 0", body.Composition)
	}
	if body.Composition.Requests != 2 || body.Composition.UpstreamRequests != 1 {
		t.Fatalf("请求构成=%+v 必须 2=1 上游+1 命中", body.Composition)
	}
	if body.Composition.ResponseCacheHitRate != "0.5000" {
		t.Fatalf("命中率=%q 期望 0.5000", body.Composition.ResponseCacheHitRate)
	}
	if body.Totals.Requests != 2 {
		t.Fatalf("同窗 totals.requests=%d 必须与构成请求数对账", body.Totals.Requests)
	}
	for _, tenantID := range store.seenTenants() {
		if tenantID != 7 {
			t.Fatalf("查询租户=%v 必须只传 7", store.seenTenants())
		}
	}
}

func TestTenantCacheCompositionOperatorCannotReadOtherTenant(t *testing.T) {
	store := &tenantOverviewQueryStub{events: twoTenantCacheCompositionEvents(time.Now().UTC())}
	h := NewTenantCacheCompositionHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		store,
	)
	rec := invoke(h, "/admin/v1/usage/cache-composition?window=19h&tenant_id=9")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("状态码=%d 期望 403 体=%s", rec.Code, rec.Body.String())
	}
	if store.callCount() != 0 {
		t.Fatalf("跨租户不得查询，calls=%v", store.seenTenants())
	}
}

func TestTenantCacheCompositionPlatformAdminRequiresTenantID(t *testing.T) {
	store := &tenantOverviewQueryStub{events: twoTenantCacheCompositionEvents(time.Now().UTC())}
	h := NewTenantCacheCompositionHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		store,
	)
	for _, target := range []string{
		"/admin/v1/usage/cache-composition?window=19h",
		"/admin/v1/usage/cache-composition?window=19h&tenant_id=",
		"/admin/v1/usage/cache-composition?window=19h&tenant_id=%20",
	} {
		rec := invoke(h, target)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 状态码=%d 期望 400", target, rec.Code)
		}
		if !containsErrorCode(rec.Body.String(), "tenant_id_required") && !containsErrorCode(rec.Body.String(), "invalid_tenant_id") {
			t.Fatalf("%s 体=%s 必须拒绝省略/空串/空白", target, rec.Body.String())
		}
	}
	if store.callCount() != 0 {
		t.Fatalf("缺 tenant_id 不得查询")
	}
}

func TestTenantCacheCompositionPlatformAdminSeesExplicitTenantOnly(t *testing.T) {
	store := &tenantOverviewQueryStub{events: twoTenantCacheCompositionEvents(time.Now().UTC())}
	h := NewTenantCacheCompositionHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		store,
	)
	rec := invoke(h, "/admin/v1/usage/cache-composition?window=23h&tenant_id=7")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码=%d 体=%s", rec.Code, rec.Body.String())
	}
	body := decodeTenantCacheComposition(t, rec.Result())
	if body.TenantID != 7 || body.Composition.ResponseCacheHits != 1 || body.Window != "23h" {
		t.Fatalf("部署者显式租户 7 实得 %+v", body)
	}
}

func TestTenantCacheCompositionRejectsTimezoneRewrite(t *testing.T) {
	store := &tenantOverviewQueryStub{}
	h := NewTenantCacheCompositionHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		store,
	)
	rec := invoke(h, "/admin/v1/usage/cache-composition?window=19h&tenant_id=7&timezone=Asia/Shanghai")
	if rec.Code != http.StatusBadRequest || !containsErrorCode(rec.Body.String(), "timezone_not_supported") {
		t.Fatalf("应拒绝时区改窗 体=%s", rec.Body.String())
	}
	if store.callCount() != 0 {
		t.Fatal("非法时区不得查询")
	}
}

func TestTenantCacheCompositionCacheKeyIsolatesTenants(t *testing.T) {
	oldTTL := overviewSnapshotTTL
	overviewSnapshotTTL = time.Minute
	defer func() { overviewSnapshotTTL = oldTTL }()

	store := &tenantOverviewQueryStub{events: twoTenantCacheCompositionEvents(time.Now().UTC())}
	h := NewTenantCacheCompositionHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		store,
	)
	first := invoke(h, "/admin/v1/usage/cache-composition?window=29h&tenant_id=7")
	if first.Code != http.StatusOK || first.Header().Get(snapshotCacheHeader) != "miss" {
		t.Fatalf("租户 7 首查失败 code=%d cache=%q", first.Code, first.Header().Get(snapshotCacheHeader))
	}
	second := invoke(h, "/admin/v1/usage/cache-composition?window=29h&tenant_id=9")
	if second.Header().Get(snapshotCacheHeader) != "miss" {
		t.Fatalf("租户 9 必须 miss，实得 %q", second.Header().Get(snapshotCacheHeader))
	}
	body := decodeTenantCacheComposition(t, second.Result())
	if body.TenantID != 9 || body.Composition.PromptCacheReadTokens != 900 || body.Composition.ResponseCacheHits != 0 {
		t.Fatalf("租户 9 被串栏：%+v", body)
	}
	third := invoke(h, "/admin/v1/usage/cache-composition?window=29h&tenant_id=7")
	if third.Header().Get(snapshotCacheHeader) != "hit" {
		t.Fatalf("同租户再查应 hit，实得 %q", third.Header().Get(snapshotCacheHeader))
	}
}

func TestTenantCacheCompositionQueryFailureDoesNotReturnOtherTenantCache(t *testing.T) {
	oldTTL := overviewSnapshotTTL
	overviewSnapshotTTL = time.Minute
	defer func() { overviewSnapshotTTL = oldTTL }()

	store := &tenantOverviewQueryStub{events: twoTenantCacheCompositionEvents(time.Now().UTC())}
	h := NewTenantCacheCompositionHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		store,
	)
	okRec := invoke(h, "/admin/v1/usage/cache-composition?window=31h&tenant_id=7")
	if okRec.Code != http.StatusOK {
		t.Fatalf("预热失败 %s", okRec.Body.String())
	}
	store.fail = errors.New("composition backend down")
	failRec := invoke(h, "/admin/v1/usage/cache-composition?window=31h&tenant_id=9")
	if failRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("失败应为 503，实得 %d", failRec.Code)
	}
	if containsFixedCost(failRec.Body.String(), "4.50000000") {
		t.Fatalf("失败不得回落其它租户：%s", failRec.Body.String())
	}
}

func TestTenantCacheCompositionAuthFailureRejected(t *testing.T) {
	store := &tenantOverviewQueryStub{}
	h := NewTenantCacheCompositionHandler(tenantOverviewAuthStub{err: admin.ErrAdminUnauthorized}, store)
	rec := invoke(h, "/admin/v1/usage/cache-composition?window=19h&tenant_id=7")
	if rec.Code != http.StatusUnauthorized || store.callCount() != 0 {
		t.Fatalf("鉴权失败应 401 且不查询，code=%d", rec.Code)
	}
}

func TestTenantCacheCompositionUnconfiguredReturns503(t *testing.T) {
	rec := invoke(NewTenantCacheCompositionHandler(tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}, nil),
		"/admin/v1/usage/cache-composition?window=19h&tenant_id=7")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码=%d 期望 503", rec.Code)
	}
}

func TestTenantCacheCompositionSnapshotHeaderIsNotPromptCache(t *testing.T) {
	oldTTL := overviewSnapshotTTL
	overviewSnapshotTTL = time.Minute
	defer func() { overviewSnapshotTTL = oldTTL }()

	now := time.Now().UTC()
	store := &tenantOverviewQueryStub{events: []tenantOverviewEvent{{
		tenantID: 7,
		overviewSeedEvent: overviewSeedEvent{
			userID: 1, apiKeyID: 1, settledAt: now.Add(-10 * time.Minute),
			cost: decimal.RequireFromString("1.00"), tokensInput: 8, tokensOutput: 2,
			endClass: "non_streaming",
		},
	}}}
	h := NewTenantCacheCompositionHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		store,
	)
	first := invoke(h, "/admin/v1/usage/cache-composition?window=37h")
	second := invoke(h, "/admin/v1/usage/cache-composition?window=37h")
	if second.Header().Get(snapshotCacheHeader) != "hit" {
		t.Fatalf("第二次应为快照 hit，实得 %q", second.Header().Get(snapshotCacheHeader))
	}
	body := decodeTenantCacheComposition(t, second.Result())
	if body.Composition.PromptCacheReadTokens != 0 || body.Composition.ResponseCacheHits != 0 {
		t.Fatalf("仅有分析快照命中时业务构成必须为 0：%+v", body.Composition)
	}
	if first.Code != http.StatusOK {
		t.Fatalf("首查失败 %d", first.Code)
	}
}
