package usageanalyticshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	dboverview "github.com/BloomingProsperity/HUAKAI/internal/db/usageoverview"
)

func (s *tenantOverviewQueryStub) AggregateTenantUsageHourlyTrend(_ context.Context, arg dboverview.AggregateTenantUsageHourlyTrendParams) ([]dboverview.AggregateTenantUsageHourlyTrendRow, error) {
	s.mu.Lock()
	s.calls = append(s.calls, "hourly")
	s.tenants = append(s.tenants, arg.TenantID)
	fail := s.fail
	s.mu.Unlock()
	if fail != nil {
		return nil, fail
	}
	type bucket struct {
		hour                time.Time
		tokensInput         int64
		tokensOutput        int64
		cacheCreationTokens int64
		cacheReadTokens     int64
		cost                decimal.Decimal
	}
	byHour := map[string]bucket{}
	for _, event := range s.events {
		if event.tenantID != arg.TenantID {
			continue
		}
		if arg.SettledSince.Valid && event.settledAt.Before(arg.SettledSince.Time) {
			continue
		}
		hour := event.settledAt.UTC().Truncate(time.Hour)
		key := hour.Format(time.RFC3339)
		current := byHour[key]
		current.hour = hour
		current.tokensInput += event.tokensInput
		current.tokensOutput += event.tokensOutput
		current.cacheCreationTokens += event.cacheCreated
		current.cacheReadTokens += event.cacheRead
		current.cost = current.cost.Add(event.cost)
		byHour[key] = current
	}
	keys := make([]string, 0, len(byHour))
	for key := range byHour {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([]dboverview.AggregateTenantUsageHourlyTrendRow, 0, len(keys))
	for _, key := range keys {
		current := byHour[key]
		rows = append(rows, dboverview.AggregateTenantUsageHourlyTrendRow{
			Hour:                pgtype.Timestamptz{Time: current.hour, Valid: true},
			TokensInput:         current.tokensInput,
			TokensOutput:        current.tokensOutput,
			CacheCreationTokens: current.cacheCreationTokens,
			CacheReadTokens:     current.cacheReadTokens,
			TotalCost:           current.cost.StringFixed(8),
		})
	}
	return rows, nil
}

func twoTenantHourlyEvents(now time.Time) []tenantOverviewEvent {
	hourA := now.UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	hourB := hourA.Add(time.Hour)
	return []tenantOverviewEvent{
		{tenantID: 7, overviewSeedEvent: overviewSeedEvent{
			userID: 101, apiKeyID: 1001, settledAt: hourA.Add(10 * time.Minute),
			cost: decimal.RequireFromString("4.50"), tokensInput: 100, tokensOutput: 200,
			cacheCreated: 20, cacheRead: 30,
			inputCost: decimal.RequireFromString("1.10"), outputCost: decimal.RequireFromString("2.20"),
			cacheCreateCost: decimal.RequireFromString("0.30"), cacheReadCost: decimal.RequireFromString("0.10"),
			endClass: "stream_end_graceful",
		}},
		{tenantID: 7, overviewSeedEvent: overviewSeedEvent{
			userID: 101, apiKeyID: 1001, settledAt: hourB.Add(15 * time.Minute),
			cost: decimal.RequireFromString("1.25"), tokensInput: 50, tokensOutput: 25,
			cacheCreated: 5, cacheRead: 8,
			inputCost: decimal.RequireFromString("0.40"), outputCost: decimal.RequireFromString("0.60"),
			cacheCreateCost: decimal.RequireFromString("0.15"), cacheReadCost: decimal.RequireFromString("0.10"),
			endClass: "non_streaming",
		}},
		{tenantID: 9, overviewSeedEvent: overviewSeedEvent{
			userID: 202, apiKeyID: 2002, settledAt: hourA.Add(20 * time.Minute),
			cost: decimal.RequireFromString("90.00"), tokensInput: 4000, tokensOutput: 5000,
			cacheCreated: 800, cacheRead: 900,
			inputCost: decimal.RequireFromString("40.00"), outputCost: decimal.RequireFromString("50.00"),
			endClass: "non_streaming",
		}},
	}
}

func decodeTenantHourly(t *testing.T, rec *http.Response) hourlyTrendResponse {
	t.Helper()
	var body hourlyTrendResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("解析小时趋势: %v", err)
	}
	return body
}

func sumHourlyColumns(hours []hourlyTrendPoint) (input, output, cacheWrite, cacheRead int64, cost decimal.Decimal) {
	cost = decimal.Zero
	for _, point := range hours {
		input += point.TokensInput
		output += point.TokensOutput
		cacheWrite += point.CacheCreationTokens
		cacheRead += point.CacheReadTokens
		value, err := decimal.NewFromString(point.Cost)
		if err != nil {
			return 0, 0, 0, 0, decimal.Zero
		}
		cost = cost.Add(value)
	}
	return input, output, cacheWrite, cacheRead, cost
}

func TestTenantHourlyOperatorSeesOnlyOwnTenantHours(t *testing.T) {
	now := time.Now().UTC()
	store := &tenantOverviewQueryStub{events: twoTenantHourlyEvents(now)}
	h := NewTenantHourlyHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		store,
	)
	// 变异:忽略 ScopeTenantID 或对明细在浏览器求和 -> 会吃到租户 9 的 90 元/9000 Token -> 红。
	rec := invoke(h, "/admin/v1/usage/hourly?window=19h")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码=%d 期望 200 体=%s", rec.Code, rec.Body.String())
	}
	body := decodeTenantHourly(t, rec.Result())
	if body.TenantID != 7 {
		t.Fatalf("tenant_id=%d 期望 7", body.TenantID)
	}
	if len(body.Hours) != 2 {
		t.Fatalf("小时点数=%d 期望 2，禁止把两小时合成一点或补零", len(body.Hours))
	}
	if body.Hours[0].TokensInput != 100 || body.Hours[0].TokensOutput != 200 ||
		body.Hours[0].CacheCreationTokens != 20 || body.Hours[0].CacheReadTokens != 30 ||
		body.Hours[0].Cost != "4.50000000" {
		t.Fatalf("第一小时=%+v 必须并列四列 Token 与费用", body.Hours[0])
	}
	if body.Hours[1].TokensInput != 50 || body.Hours[1].CacheReadTokens != 8 || body.Hours[1].Cost != "1.25000000" {
		t.Fatalf("第二小时=%+v 必须独立成桶", body.Hours[1])
	}
	if body.Totals.Requests != 2 || body.Totals.TotalCost != "5.75000000" || body.Totals.TotalTokens != 375 {
		t.Fatalf("totals=%+v 必须只含租户 7，且 total_tokens=input+output 不含提示缓存", body.Totals)
	}
	if body.Totals.TotalTokens != body.Totals.TotalTokensInput+body.Totals.TotalTokensOutput {
		t.Fatalf("total_tokens=%d 不得把缓存列折进总量", body.Totals.TotalTokens)
	}
	in, out, created, read, cost := sumHourlyColumns(body.Hours)
	if in != body.Totals.TotalTokensInput || out != body.Totals.TotalTokensOutput ||
		created != body.Totals.TotalCacheCreationTokens || read != body.Totals.TotalCacheReadTokens ||
		cost.StringFixed(8) != body.Totals.TotalCost {
		t.Fatalf("小时序列 %+v 必须与同窗 totals %+v 对账", body.Hours, body.Totals)
	}
	for _, tenantID := range store.seenTenants() {
		if tenantID != 7 {
			t.Fatalf("查询租户=%v 必须只传 7", store.seenTenants())
		}
	}
}

func TestTenantHourlyOperatorCannotReadOtherTenant(t *testing.T) {
	store := &tenantOverviewQueryStub{events: twoTenantHourlyEvents(time.Now().UTC())}
	h := NewTenantHourlyHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		store,
	)
	rec := invoke(h, "/admin/v1/usage/hourly?window=19h&tenant_id=9")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("状态码=%d 期望 403 体=%s", rec.Code, rec.Body.String())
	}
	if store.callCount() != 0 {
		t.Fatalf("跨租户请求不得触达聚合查询，calls=%v", store.seenTenants())
	}
}

func TestTenantHourlyPlatformAdminRequiresTenantID(t *testing.T) {
	store := &tenantOverviewQueryStub{events: twoTenantHourlyEvents(time.Now().UTC())}
	h := NewTenantHourlyHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		store,
	)
	for _, target := range []string{
		"/admin/v1/usage/hourly?window=19h",
		"/admin/v1/usage/hourly?window=19h&tenant_id=",
		"/admin/v1/usage/hourly?window=19h&tenant_id=%20",
	} {
		rec := invoke(h, target)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 状态码=%d 期望 400 体=%s", target, rec.Code, rec.Body.String())
		}
		if !containsErrorCode(rec.Body.String(), "tenant_id_required") && !containsErrorCode(rec.Body.String(), "invalid_tenant_id") {
			t.Fatalf("%s 体=%s 必须拒绝省略/空串/空白，禁止回落全平台", target, rec.Body.String())
		}
	}
	if store.callCount() != 0 {
		t.Fatalf("缺 tenant_id 不得查询，calls=%d", store.callCount())
	}
}

func TestTenantHourlyPlatformAdminSeesExplicitTenantOnly(t *testing.T) {
	store := &tenantOverviewQueryStub{events: twoTenantHourlyEvents(time.Now().UTC())}
	h := NewTenantHourlyHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		store,
	)
	rec := invoke(h, "/admin/v1/usage/hourly?window=23h&tenant_id=7")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码=%d 期望 200 体=%s", rec.Code, rec.Body.String())
	}
	body := decodeTenantHourly(t, rec.Result())
	if body.TenantID != 7 || body.Totals.TotalCost != "5.75000000" || len(body.Hours) != 2 {
		t.Fatalf("部署者显式租户 7 应得本租户小时序列，实得 %+v", body)
	}
	if body.Window != "23h" {
		t.Fatalf("window=%q 期望 23h", body.Window)
	}
}

func TestTenantHourlyRejectsTimezoneRewrite(t *testing.T) {
	store := &tenantOverviewQueryStub{events: twoTenantHourlyEvents(time.Now().UTC())}
	h := NewTenantHourlyHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		store,
	)
	rec := invoke(h, "/admin/v1/usage/hourly?window=19h&tenant_id=7&timezone=Asia/Shanghai")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码=%d 期望 400 体=%s", rec.Code, rec.Body.String())
	}
	if !containsErrorCode(rec.Body.String(), "timezone_not_supported") {
		t.Fatalf("体=%s 必须拒绝时区改窗", rec.Body.String())
	}
	if store.callCount() != 0 {
		t.Fatalf("非法时区不得查询，calls=%d", store.callCount())
	}
}

func TestTenantHourlyCacheKeyIsolatesTenants(t *testing.T) {
	oldTTL := overviewSnapshotTTL
	overviewSnapshotTTL = time.Minute
	defer func() { overviewSnapshotTTL = oldTTL }()

	store := &tenantOverviewQueryStub{events: twoTenantHourlyEvents(time.Now().UTC())}
	h := NewTenantHourlyHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		store,
	)
	first := invoke(h, "/admin/v1/usage/hourly?window=29h&tenant_id=7")
	if first.Code != http.StatusOK || first.Header().Get(snapshotCacheHeader) != "miss" {
		t.Fatalf("租户 7 首查 code=%d cache=%q 体=%s", first.Code, first.Header().Get(snapshotCacheHeader), first.Body.String())
	}
	// 变异:缓存 key 不含 tenant_id -> 租户 9 会 hit 到 5.75 -> 红。
	second := invoke(h, "/admin/v1/usage/hourly?window=29h&tenant_id=9")
	if second.Code != http.StatusOK {
		t.Fatalf("租户 9 状态码=%d 体=%s", second.Code, second.Body.String())
	}
	if second.Header().Get(snapshotCacheHeader) != "miss" {
		t.Fatalf("租户 9 必须 miss，实得 cache=%q；共享窗口缓存会串租户", second.Header().Get(snapshotCacheHeader))
	}
	body := decodeTenantHourly(t, second.Result())
	if body.TenantID != 9 || body.Totals.TotalCost != "90.00000000" || body.Totals.TotalTokens != 9000 {
		t.Fatalf("租户 9 响应=%+v 被串成租户 7", body)
	}
	third := invoke(h, "/admin/v1/usage/hourly?window=29h&tenant_id=7")
	if third.Header().Get(snapshotCacheHeader) != "hit" {
		t.Fatalf("同一租户再查应 hit，实得 %q", third.Header().Get(snapshotCacheHeader))
	}
	if got := store.callCount(); got != 4 {
		t.Fatalf("查询次数=%d 期望 4（7 的 totals+hourly 一次，9 的 totals+hourly 一次）", got)
	}
}

func TestTenantHourlyQueryFailureDoesNotReturnOtherTenantCache(t *testing.T) {
	oldTTL := overviewSnapshotTTL
	overviewSnapshotTTL = time.Minute
	defer func() { overviewSnapshotTTL = oldTTL }()

	store := &tenantOverviewQueryStub{events: twoTenantHourlyEvents(time.Now().UTC())}
	h := NewTenantHourlyHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		store,
	)
	okRec := invoke(h, "/admin/v1/usage/hourly?window=31h&tenant_id=7")
	if okRec.Code != http.StatusOK {
		t.Fatalf("预热租户 7 失败 code=%d 体=%s", okRec.Code, okRec.Body.String())
	}
	store.fail = errors.New("hourly backend down")
	failRec := invoke(h, "/admin/v1/usage/hourly?window=31h&tenant_id=9")
	if failRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("租户 9 聚合失败应为 503，实得 %d 体=%s", failRec.Code, failRec.Body.String())
	}
	if containsFixedCost(failRec.Body.String(), "5.75000000") {
		t.Fatalf("失败响应不得回落其它租户缓存：%s", failRec.Body.String())
	}
}

func TestTenantHourlyAuthFailureRejected(t *testing.T) {
	store := &tenantOverviewQueryStub{}
	h := NewTenantHourlyHandler(tenantOverviewAuthStub{err: admin.ErrAdminUnauthorized}, store)
	rec := invoke(h, "/admin/v1/usage/hourly?window=19h&tenant_id=7")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("状态码=%d 期望 401", rec.Code)
	}
	if store.callCount() != 0 {
		t.Fatal("鉴权失败不得查询")
	}
}

func TestTenantHourlyUnconfiguredReturns503(t *testing.T) {
	rec := invoke(NewTenantHourlyHandler(tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}, nil),
		"/admin/v1/usage/hourly?window=19h&tenant_id=7")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码=%d 期望 503", rec.Code)
	}
}

func TestTenantHourlyInvalidWindowDoesNotQuery(t *testing.T) {
	store := &tenantOverviewQueryStub{}
	h := NewTenantHourlyHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		store,
	)
	rec := invoke(h, "/admin/v1/usage/hourly?window=abc&tenant_id=7")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码=%d 期望 400 体=%s", rec.Code, rec.Body.String())
	}
	if store.callCount() != 0 {
		t.Fatalf("非法 window 不得查询，calls=%d", store.callCount())
	}
}

func TestTenantHourlySnapshotHeaderIsNotPromptCache(t *testing.T) {
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
	h := NewTenantHourlyHandler(
		tenantOverviewAuthStub{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		store,
	)
	first := invoke(h, "/admin/v1/usage/hourly?window=37h")
	second := invoke(h, "/admin/v1/usage/hourly?window=37h")
	if second.Header().Get(snapshotCacheHeader) != "hit" {
		t.Fatalf("第二次应为 HTTP 快照 hit，实得 %q", second.Header().Get(snapshotCacheHeader))
	}
	body := decodeTenantHourly(t, second.Result())
	if body.Totals.TotalCacheReadTokens != 0 || body.Totals.TotalCacheCreationTokens != 0 {
		t.Fatalf("仅有响应缓存命中时提示缓存 Token 必须为 0，实得 %+v", body.Totals)
	}
	if len(body.Hours) != 1 || body.Hours[0].CacheCreationTokens != 0 || body.Hours[0].CacheReadTokens != 0 {
		t.Fatalf("小时点提示缓存必须与 HTTP 快照分列，实得 %+v", body.Hours)
	}
	if first.Code != http.StatusOK {
		t.Fatalf("首查失败 %d", first.Code)
	}
}
