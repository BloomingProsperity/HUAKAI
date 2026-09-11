package usageanalyticshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	dboverview "github.com/BloomingProsperity/HUAKAI/internal/db/usageoverview"
)

type tenantOverviewAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s tenantOverviewAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	return s.ident, nil
}

type tenantOverviewEvent struct {
	tenantID int64
	overviewSeedEvent
}

type tenantOverviewQueryStub struct {
	mu      sync.Mutex
	events  []tenantOverviewEvent
	calls   []string
	tenants []int64
	fail    error
}

func (s *tenantOverviewQueryStub) AggregateTenantUsageOverviewTotals(_ context.Context, arg dboverview.AggregateTenantUsageOverviewTotalsParams) (dboverview.AggregateTenantUsageOverviewTotalsRow, error) {
	s.mu.Lock()
	s.calls = append(s.calls, "totals")
	s.tenants = append(s.tenants, arg.TenantID)
	fail := s.fail
	s.mu.Unlock()
	if fail != nil {
		return dboverview.AggregateTenantUsageOverviewTotalsRow{}, fail
	}
	var requests, tokens, tokensInput, tokensOutput, cacheCreated, cacheRead, imageOutput, successes int64
	cost := decimal.Zero
	inputCost := decimal.Zero
	outputCost := decimal.Zero
	cacheCreateCost := decimal.Zero
	cacheReadCost := decimal.Zero
	imageOutputCost := decimal.Zero
	users := map[int64]struct{}{}
	keys := map[int64]struct{}{}
	for _, event := range s.events {
		if event.tenantID != arg.TenantID {
			continue
		}
		if arg.SettledSince.Valid && event.settledAt.Before(arg.SettledSince.Time) {
			continue
		}
		requests++
		tokens += event.tokensInput + event.tokensOutput
		tokensInput += event.tokensInput
		tokensOutput += event.tokensOutput
		cacheCreated += event.cacheCreated
		cacheRead += event.cacheRead
		imageOutput += event.imageOutput
		cost = cost.Add(event.cost)
		inputCost = inputCost.Add(event.inputCost)
		outputCost = outputCost.Add(event.outputCost)
		cacheCreateCost = cacheCreateCost.Add(event.cacheCreateCost)
		cacheReadCost = cacheReadCost.Add(event.cacheReadCost)
		imageOutputCost = imageOutputCost.Add(event.imageOutputCost)
		users[event.userID] = struct{}{}
		keys[event.apiKeyID] = struct{}{}
		if event.endClass == "stream_end_graceful" || event.endClass == "non_streaming" {
			successes++
		}
	}
	return dboverview.AggregateTenantUsageOverviewTotalsRow{
		RequestCount:             requests,
		TotalCost:                cost.StringFixed(8),
		TotalTokens:              tokens,
		TotalTokensInput:         tokensInput,
		TotalTokensOutput:        tokensOutput,
		TotalCacheCreationTokens: cacheCreated,
		TotalCacheReadTokens:     cacheRead,
		TotalImageOutputTokens:   imageOutput,
		TotalInputCost:           inputCost.StringFixed(8),
		TotalOutputCost:          outputCost.StringFixed(8),
		TotalCacheCreationCost:   cacheCreateCost.StringFixed(8),
		TotalCacheReadCost:       cacheReadCost.StringFixed(8),
		TotalImageOutputCost:     imageOutputCost.StringFixed(8),
		ActiveUsers:              int64(len(users)),
		ActiveApiKeys:            int64(len(keys)),
		SuccessCount:             successes,
	}, nil
}

func (s *tenantOverviewQueryStub) AggregateTenantUsageOverviewTrendByDay(_ context.Context, arg dboverview.AggregateTenantUsageOverviewTrendByDayParams) ([]dboverview.AggregateTenantUsageOverviewTrendByDayRow, error) {
	s.mu.Lock()
	s.calls = append(s.calls, "trend")
	s.tenants = append(s.tenants, arg.TenantID)
	fail := s.fail
	s.mu.Unlock()
	if fail != nil {
		return nil, fail
	}
	type bucket struct {
		day      time.Time
		requests int64
		cost     decimal.Decimal
	}
	byDay := map[string]bucket{}
	for _, event := range s.events {
		if event.tenantID != arg.TenantID {
			continue
		}
		if arg.SettledSince.Valid && event.settledAt.Before(arg.SettledSince.Time) {
			continue
		}
		day := time.Date(event.settledAt.UTC().Year(), event.settledAt.UTC().Month(), event.settledAt.UTC().Day(), 0, 0, 0, 0, time.UTC)
		key := day.Format("2006-01-02")
		current := byDay[key]
		current.day = day
		current.requests++
		current.cost = current.cost.Add(event.cost)
		byDay[key] = current
	}
	rows := make([]dboverview.AggregateTenantUsageOverviewTrendByDayRow, 0, len(byDay))
	for _, current := range byDay {
		rows = append(rows, dboverview.AggregateTenantUsageOverviewTrendByDayRow{
			Day:          pgtype.Timestamptz{Time: current.day, Valid: true},
			RequestCount: current.requests,
			TotalCost:    current.cost.StringFixed(8),
		})
	}
	return rows, nil
}

func (s *tenantOverviewQueryStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *tenantOverviewQueryStub) seenTenants() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int64, len(s.tenants))
	copy(out, s.tenants)
	return out
}

func twoTenantOverviewEvents(now time.Time) []tenantOverviewEvent {
	recent := now.Add(-30 * time.Minute)
	return []tenantOverviewEvent{
		{tenantID: 7, overviewSeedEvent: overviewSeedEvent{
			userID: 101, apiKeyID: 1001, settledAt: recent,
			cost: decimal.RequireFromString("4.50"), tokensInput: 100, tokensOutput: 200,
			cacheCreated: 20, cacheRead: 30,
			inputCost: decimal.RequireFromString("1.10"), outputCost: decimal.RequireFromString("2.20"),
			cacheCreateCost: decimal.RequireFromString("0.30"), cacheReadCost: decimal.RequireFromString("0.10"),
			endClass: "stream_end_graceful",
		}},
		{tenantID: 9, overviewSeedEvent: overviewSeedEvent{
			userID: 202, apiKeyID: 2002, settledAt: recent,
			cost: decimal.RequireFromString("90.00"), tokensInput: 4000, tokensOutput: 5000,
			cacheCreated: 800, cacheRead: 900,
			inputCost: decimal.RequireFromString("40.00"), outputCost: decimal.RequireFromString("50.00"),
			endClass: "non_streaming",
		}},
	}
}

func decodeTenantOverview(t *testing.T, rec *http.Response) tenantOverviewResponse {
	t.Helper()
	var body tenantOverviewResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("解析租户总览: %v", err)
	}
	return body
}

func TestTenantOverviewOperatorSeesOnlyOwnTenant(t *testing.T) {
	now := time.Now().UTC()
	store := &tenantOverviewQueryStub{events: twoTenantOverviewEvents(now)}
	h := NewTenantOverviewHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		store,
	)
	// 变异:忽略 ScopeTenantID 或漏掉 tenant 谓词 -> 会吃到租户 9 的 90 元/9000 Token -> 红。
	rec := invoke(h, "/admin/v1/usage/overview?window=19h")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码=%d 期望 200 体=%s", rec.Code, rec.Body.String())
	}
	body := decodeTenantOverview(t, rec.Result())
	if body.TenantID != 7 {
		t.Fatalf("tenant_id=%d 期望 7", body.TenantID)
	}
	if body.Totals.Requests != 1 || body.Totals.TotalCost != "4.50000000" || body.Totals.TotalTokens != 300 {
		t.Fatalf("totals=%+v 必须只含租户 7", body.Totals)
	}
	if body.Totals.TotalCacheCreationTokens != 20 || body.Totals.TotalCacheReadTokens != 30 {
		t.Fatalf("提示缓存分项=%+v 必须按租户 7 独立列出，不得与租户 9 或 HTTP 快照缓存混成一项", body.Totals)
	}
	if rec.Header().Get(snapshotCacheHeader) != "miss" {
		t.Fatalf("首查 X-Snapshot-Cache=%q 期望 miss", rec.Header().Get(snapshotCacheHeader))
	}
	for _, tenantID := range store.seenTenants() {
		if tenantID != 7 {
			t.Fatalf("查询租户=%v 必须只传 7", store.seenTenants())
		}
	}
}

func TestTenantOverviewOperatorCannotReadOtherTenant(t *testing.T) {
	store := &tenantOverviewQueryStub{events: twoTenantOverviewEvents(time.Now().UTC())}
	h := NewTenantOverviewHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		store,
	)
	rec := invoke(h, "/admin/v1/usage/overview?window=19h&tenant_id=9")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("状态码=%d 期望 403 体=%s", rec.Code, rec.Body.String())
	}
	if store.callCount() != 0 {
		t.Fatalf("跨租户请求不得触达聚合查询，calls=%v", store.seenTenants())
	}
}

func TestTenantOverviewPlatformAdminRequiresTenantID(t *testing.T) {
	store := &tenantOverviewQueryStub{events: twoTenantOverviewEvents(time.Now().UTC())}
	h := NewTenantOverviewHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		store,
	)
	// 变异:省略 tenant_id 回落全平台 -> 200 且 totals 含 90 元 -> 红。
	rec := invoke(h, "/admin/v1/usage/overview?window=19h")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码=%d 期望 400 体=%s", rec.Code, rec.Body.String())
	}
	if store.callCount() != 0 {
		t.Fatalf("缺 tenant_id 不得查询，calls=%d", store.callCount())
	}
	if !containsErrorCode(rec.Body.String(), "tenant_id_required") {
		t.Fatalf("体=%s 必须含 tenant_id_required", rec.Body.String())
	}
}

func TestTenantOverviewPlatformAdminSeesExplicitTenantOnly(t *testing.T) {
	store := &tenantOverviewQueryStub{events: twoTenantOverviewEvents(time.Now().UTC())}
	h := NewTenantOverviewHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		store,
	)
	rec := invoke(h, "/admin/v1/usage/overview?window=23h&tenant_id=7")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码=%d 期望 200 体=%s", rec.Code, rec.Body.String())
	}
	body := decodeTenantOverview(t, rec.Result())
	if body.TenantID != 7 || body.Totals.TotalCost != "4.50000000" || body.Totals.Requests != 1 {
		t.Fatalf("部署者显式租户 7 应得本租户 totals，实得 %+v", body)
	}
	if body.Window != "23h" {
		t.Fatalf("window=%q 期望 23h", body.Window)
	}
}

func TestTenantOverviewCacheKeyIsolatesTenants(t *testing.T) {
	oldTTL := overviewSnapshotTTL
	overviewSnapshotTTL = time.Minute
	defer func() { overviewSnapshotTTL = oldTTL }()

	store := &tenantOverviewQueryStub{events: twoTenantOverviewEvents(time.Now().UTC())}
	adminAuth := tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}
	h := NewTenantOverviewHandler(adminAuth, store)

	first := invoke(h, "/admin/v1/usage/overview?window=29h&tenant_id=7")
	if first.Code != http.StatusOK || first.Header().Get(snapshotCacheHeader) != "miss" {
		t.Fatalf("租户 7 首查 code=%d cache=%q 体=%s", first.Code, first.Header().Get(snapshotCacheHeader), first.Body.String())
	}
	// 变异:缓存 key 不含 tenant_id -> 租户 9 会 hit 到 4.50 -> 红。
	second := invoke(h, "/admin/v1/usage/overview?window=29h&tenant_id=9")
	if second.Code != http.StatusOK {
		t.Fatalf("租户 9 状态码=%d 体=%s", second.Code, second.Body.String())
	}
	if second.Header().Get(snapshotCacheHeader) != "miss" {
		t.Fatalf("租户 9 必须 miss，实得 cache=%q；共享窗口缓存会串租户", second.Header().Get(snapshotCacheHeader))
	}
	body := decodeTenantOverview(t, second.Result())
	if body.TenantID != 9 || body.Totals.TotalCost != "90.00000000" || body.Totals.TotalTokens != 9000 {
		t.Fatalf("租户 9 响应=%+v 被串成租户 7", body)
	}
	third := invoke(h, "/admin/v1/usage/overview?window=29h&tenant_id=7")
	if third.Header().Get(snapshotCacheHeader) != "hit" {
		t.Fatalf("同一租户再查应 hit，实得 %q", third.Header().Get(snapshotCacheHeader))
	}
	if got := store.callCount(); got != 4 {
		t.Fatalf("查询次数=%d 期望 4（7 的 totals+trend 一次，9 的 totals+trend 一次）", got)
	}
}

func TestTenantOverviewQueryFailureDoesNotReturnOtherTenantCache(t *testing.T) {
	oldTTL := overviewSnapshotTTL
	overviewSnapshotTTL = time.Minute
	defer func() { overviewSnapshotTTL = oldTTL }()

	store := &tenantOverviewQueryStub{events: twoTenantOverviewEvents(time.Now().UTC())}
	h := NewTenantOverviewHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		store,
	)
	okRec := invoke(h, "/admin/v1/usage/overview?window=31h&tenant_id=7")
	if okRec.Code != http.StatusOK {
		t.Fatalf("预热租户 7 失败 code=%d 体=%s", okRec.Code, okRec.Body.String())
	}
	store.fail = errors.New("overview backend down")
	failRec := invoke(h, "/admin/v1/usage/overview?window=31h&tenant_id=9")
	if failRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("租户 9 聚合失败应为 503，实得 %d 体=%s", failRec.Code, failRec.Body.String())
	}
	if containsFixedCost(failRec.Body.String(), "4.50000000") {
		t.Fatalf("失败响应不得回落其它租户缓存：%s", failRec.Body.String())
	}
}

func TestTenantOverviewAuthFailureRejected(t *testing.T) {
	store := &tenantOverviewQueryStub{}
	h := NewTenantOverviewHandler(tenantOverviewAuthStub{err: admin.ErrAdminUnauthorized}, store)
	rec := invoke(h, "/admin/v1/usage/overview?window=19h&tenant_id=7")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("状态码=%d 期望 401", rec.Code)
	}
	if store.callCount() != 0 {
		t.Fatal("鉴权失败不得查询")
	}
}

func TestTenantOverviewUnconfiguredReturns503(t *testing.T) {
	rec := invoke(NewTenantOverviewHandler(tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}, nil),
		"/admin/v1/usage/overview?window=19h&tenant_id=7")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码=%d 期望 503", rec.Code)
	}
}

func TestTenantOverviewInvalidWindowDoesNotQuery(t *testing.T) {
	store := &tenantOverviewQueryStub{}
	h := NewTenantOverviewHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		store,
	)
	rec := invoke(h, "/admin/v1/usage/overview?window=abc&tenant_id=7")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码=%d 期望 400 体=%s", rec.Code, rec.Body.String())
	}
	if store.callCount() != 0 {
		t.Fatalf("非法 window 不得查询，calls=%d", store.callCount())
	}
}

func TestTenantOverviewSnapshotHeaderIsNotPromptCache(t *testing.T) {
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
	h := NewTenantOverviewHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		store,
	)
	first := invoke(h, "/admin/v1/usage/overview?window=37h")
	second := invoke(h, "/admin/v1/usage/overview?window=37h")
	if second.Header().Get(snapshotCacheHeader) != "hit" {
		t.Fatalf("第二次应为 HTTP 快照 hit，实得 %q", second.Header().Get(snapshotCacheHeader))
	}
	body := decodeTenantOverview(t, second.Result())
	if body.Totals.TotalCacheReadTokens != 0 || body.Totals.TotalCacheCreationTokens != 0 {
		t.Fatalf("仅有响应缓存命中时提示缓存 Token 必须为 0，实得 %+v", body.Totals)
	}
	if first.Code != http.StatusOK {
		t.Fatalf("首查失败 %d", first.Code)
	}
}

func containsErrorCode(body, code string) bool {
	return strings.Contains(body, code)
}

func containsFixedCost(body, cost string) bool {
	return strings.Contains(body, cost)
}
