package usageanalyticshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type leaderboardSeedEvent struct {
	tenantID          int64
	apiKeyID          int64
	userID            int64
	model             string
	providerAccountID *int64
	settledAt         time.Time
	cost              decimal.Decimal
	tokens            int64
}

type leaderboardQueryStub struct {
	mu          sync.Mutex
	events      []leaderboardSeedEvent
	called      string
	calls       int
	userArg     dbbilling.AggregateUsageLeaderboardByUserParams
	modelArg    dbbilling.AggregateUsageLeaderboardByModelParams
	providerArg dbbilling.AggregateUsageLeaderboardByProviderAccountParams
	apiKeyArg   dbbilling.AggregateUsageLeaderboardByApiKeyParams
	started     chan struct{}
	release     chan struct{}
	once        sync.Once
}

func (s *leaderboardQueryStub) AggregateUsageLeaderboardByUser(_ context.Context, arg dbbilling.AggregateUsageLeaderboardByUserParams) ([]dbbilling.AggregateUsageLeaderboardByUserRow, error) {
	s.mu.Lock()
	s.called = "user"
	s.calls++
	s.userArg = arg
	s.mu.Unlock()
	s.maybeBlock()
	rows := s.aggregate(arg.SettledSince, arg.RowLimit, func(e leaderboardSeedEvent) string {
		return strconv.FormatInt(e.userID, 10)
	})
	out := make([]dbbilling.AggregateUsageLeaderboardByUserRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, dbbilling.AggregateUsageLeaderboardByUserRow(row))
	}
	return out, nil
}

func (s *leaderboardQueryStub) AggregateUsageLeaderboardByModel(_ context.Context, arg dbbilling.AggregateUsageLeaderboardByModelParams) ([]dbbilling.AggregateUsageLeaderboardByModelRow, error) {
	s.mu.Lock()
	s.called = "model"
	s.calls++
	s.modelArg = arg
	s.mu.Unlock()
	s.maybeBlock()
	rows := s.aggregate(arg.SettledSince, arg.RowLimit, func(e leaderboardSeedEvent) string {
		return e.model
	})
	out := make([]dbbilling.AggregateUsageLeaderboardByModelRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, dbbilling.AggregateUsageLeaderboardByModelRow(row))
	}
	return out, nil
}

func (s *leaderboardQueryStub) AggregateUsageLeaderboardByProviderAccount(_ context.Context, arg dbbilling.AggregateUsageLeaderboardByProviderAccountParams) ([]dbbilling.AggregateUsageLeaderboardByProviderAccountRow, error) {
	s.mu.Lock()
	s.called = "provider_account"
	s.calls++
	s.providerArg = arg
	s.mu.Unlock()
	s.maybeBlock()
	rows := s.aggregate(arg.SettledSince, arg.RowLimit, func(e leaderboardSeedEvent) string {
		if e.providerAccountID == nil {
			return "unassigned"
		}
		return strconv.FormatInt(*e.providerAccountID, 10)
	})
	out := make([]dbbilling.AggregateUsageLeaderboardByProviderAccountRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, dbbilling.AggregateUsageLeaderboardByProviderAccountRow(row))
	}
	return out, nil
}

func (s *leaderboardQueryStub) AggregateUsageLeaderboardByApiKey(_ context.Context, arg dbbilling.AggregateUsageLeaderboardByApiKeyParams) ([]dbbilling.AggregateUsageLeaderboardByApiKeyRow, error) {
	s.mu.Lock()
	s.called = "api_key"
	s.calls++
	s.apiKeyArg = arg
	s.mu.Unlock()
	s.maybeBlock()
	rows := s.aggregateScoped(arg.SettledSince, arg.RowLimit, arg.TenantID, func(e leaderboardSeedEvent) string {
		return strconv.FormatInt(e.apiKeyID, 10)
	})
	out := make([]dbbilling.AggregateUsageLeaderboardByApiKeyRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, dbbilling.AggregateUsageLeaderboardByApiKeyRow(row))
	}
	return out, nil
}

func (s *leaderboardQueryStub) maybeBlock() {
	if s.started == nil || s.release == nil {
		return
	}
	s.once.Do(func() {
		close(s.started)
	})
	<-s.release
}

func (s *leaderboardQueryStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *leaderboardQueryStub) AggregateUsagePerformanceByModel(context.Context, dbbilling.AggregateUsagePerformanceByModelParams) ([]dbbilling.AggregateUsagePerformanceByModelRow, error) {
	return nil, nil
}

func (s *leaderboardQueryStub) AggregateUsagePerformanceByProviderAccount(context.Context, dbbilling.AggregateUsagePerformanceByProviderAccountParams) ([]dbbilling.AggregateUsagePerformanceByProviderAccountRow, error) {
	return nil, nil
}

func (s *leaderboardQueryStub) AggregateUsageOverviewTotals(context.Context, pgtype.Timestamptz) (dbbilling.AggregateUsageOverviewTotalsRow, error) {
	return dbbilling.AggregateUsageOverviewTotalsRow{}, nil
}

func (s *leaderboardQueryStub) AggregateUsageOverviewTrendByDay(context.Context, pgtype.Timestamptz) ([]dbbilling.AggregateUsageOverviewTrendByDayRow, error) {
	return nil, nil
}

type leaderboardAggregateRow struct {
	Key          string
	TotalCost    string
	TotalTokens  int64
	RequestCount int64
}

func (s *leaderboardQueryStub) aggregate(since pgtype.Timestamptz, limit int32, keyFor func(leaderboardSeedEvent) string) []leaderboardAggregateRow {
	return s.aggregateScoped(since, limit, 0, keyFor)
}

func (s *leaderboardQueryStub) aggregateScoped(since pgtype.Timestamptz, limit int32, tenantID int64, keyFor func(leaderboardSeedEvent) string) []leaderboardAggregateRow {
	type agg struct {
		cost     decimal.Decimal
		tokens   int64
		requests int64
	}
	byKey := map[string]agg{}
	for _, event := range s.events {
		if since.Valid && event.settledAt.Before(since.Time) {
			continue
		}
		if tenantID > 0 && event.tenantID != tenantID {
			continue
		}
		key := keyFor(event)
		current := byKey[key]
		current.cost = current.cost.Add(event.cost)
		current.tokens += event.tokens
		current.requests++
		byKey[key] = current
	}
	rows := make([]leaderboardAggregateRow, 0, len(byKey))
	for key, current := range byKey {
		rows = append(rows, leaderboardAggregateRow{
			Key:          key,
			TotalCost:    current.cost.StringFixed(8),
			TotalTokens:  current.tokens,
			RequestCount: current.requests,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		cmp := decimal.RequireFromString(rows[i].TotalCost).Cmp(decimal.RequireFromString(rows[j].TotalCost))
		if cmp == 0 {
			return rows[i].Key < rows[j].Key
		}
		return cmp > 0
	})
	if limit > 0 && len(rows) > int(limit) {
		rows = rows[:limit]
	}
	return rows
}

func TestLeaderboard_UserWindowOrderAndLimitAreDiscriminating(t *testing.T) {
	now := time.Now().UTC()
	store := &leaderboardQueryStub{events: []leaderboardSeedEvent{
		{userID: 101, settledAt: now.Add(-1 * time.Hour), cost: decimal.RequireFromString("10"), tokens: 1000},
		{userID: 202, settledAt: now.Add(-2 * time.Hour), cost: decimal.RequireFromString("30"), tokens: 3000},
		{userID: 303, settledAt: now.Add(-3 * time.Hour), cost: decimal.RequireFromString("5"), tokens: 500},
		{userID: 404, settledAt: now.Add(-25 * time.Hour), cost: decimal.RequireFromString("100"), tokens: 9000},
	}}
	rec := invoke(NewLeaderboardHandler(store), "/v1/admin/usage/leaderboard?by=user&window=24h&limit=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Window  string `json:"window"`
		By      string `json:"by"`
		Entries []struct {
			Rank         int    `json:"rank"`
			Key          string `json:"key"`
			TotalCost    string `json:"total_cost"`
			TotalTokens  int64  `json:"total_tokens"`
			RequestCount int64  `json:"request_count"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body=%s err=%v", rec.Body.String(), err)
	}
	if body.Window != "24h" || body.By != "user" {
		t.Fatalf("window/by=%q/%q want 24h/user", body.Window, body.By)
	}
	if len(body.Entries) != 2 {
		t.Fatalf("entries=%v want exactly B then A; mutation without LIMIT would include C", body.Entries)
	}
	want := []struct {
		rank int
		key  string
		cost string
	}{
		{1, "202", "30.00000000"},
		{2, "101", "10.00000000"},
	}
	for i, entry := range body.Entries {
		if entry.Rank != want[i].rank || entry.Key != want[i].key || entry.TotalCost != want[i].cost {
			t.Fatalf("entry[%d]=%+v want rank/key/cost=%d/%s/%s; mutation without window admits D, without DESC reorders, without LIMIT admits C", i, entry, want[i].rank, want[i].key, want[i].cost)
		}
	}
	if store.called != "user" {
		t.Fatalf("called=%q want user", store.called)
	}
	if store.userArg.RowLimit != 2 {
		t.Fatalf("limit arg=%d want 2", store.userArg.RowLimit)
	}
	if !store.userArg.SettledSince.Valid || now.Sub(store.userArg.SettledSince.Time) > 25*time.Hour {
		t.Fatalf("settled_since=%v must be a valid 24h window cutoff excluding the 25h D row", store.userArg.SettledSince)
	}
}

func TestLeaderboardByApiKey_Aggregates(t *testing.T) {
	now := time.Now().UTC()
	store := &leaderboardQueryStub{events: []leaderboardSeedEvent{
		{tenantID: 1, apiKeyID: 9001, userID: 42, settledAt: now.Add(-1 * time.Hour), cost: decimal.RequireFromString("10"), tokens: 100},
		{tenantID: 1, apiKeyID: 9001, userID: 42, settledAt: now.Add(-2 * time.Hour), cost: decimal.RequireFromString("2"), tokens: 20},
		{tenantID: 1, apiKeyID: 9002, userID: 42, settledAt: now.Add(-3 * time.Hour), cost: decimal.RequireFromString("7"), tokens: 70},
		{tenantID: 1, apiKeyID: 9003, userID: 42, settledAt: now.Add(-25 * time.Hour), cost: decimal.RequireFromString("999"), tokens: 999},
	}}
	rec := invoke(NewLeaderboardHandler(store), "/v1/admin/usage/leaderboard?by=api_key&window=24h&limit=10")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		By      string `json:"by"`
		Entries []struct {
			Rank         int    `json:"rank"`
			Key          string `json:"key"`
			TotalCost    string `json:"total_cost"`
			TotalTokens  int64  `json:"total_tokens"`
			RequestCount int64  `json:"request_count"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body=%s err=%v", rec.Body.String(), err)
	}
	if body.By != "api_key" {
		t.Fatalf("by=%q want api_key", body.By)
	}
	if len(body.Entries) != 2 {
		t.Fatalf("entries=%v want two api-key buckets; mutation GROUP BY user_id merges the same-user keys, mutation dropping window admits old key 9003", body.Entries)
	}
	want := []struct {
		rank     int
		key      string
		cost     string
		tokens   int64
		requests int64
	}{
		{1, "9001", "12.00000000", 120, 2},
		{2, "9002", "7.00000000", 70, 1},
	}
	for i, entry := range body.Entries {
		if entry.Rank != want[i].rank || entry.Key != want[i].key || entry.TotalCost != want[i].cost ||
			entry.TotalTokens != want[i].tokens || entry.RequestCount != want[i].requests {
			t.Fatalf("entry[%d]=%+v want rank/key/cost/tokens/requests=%d/%s/%s/%d/%d; mutation GROUP BY user_id or summing only one row is caught here",
				i, entry, want[i].rank, want[i].key, want[i].cost, want[i].tokens, want[i].requests)
		}
	}
	if store.called != "api_key" {
		t.Fatalf("called=%q want api_key", store.called)
	}
}

func TestLeaderboardByApiKey_TenantScoped(t *testing.T) {
	now := time.Now().UTC()
	store := &leaderboardQueryStub{events: []leaderboardSeedEvent{
		{tenantID: 7001, apiKeyID: 9101, userID: 51, settledAt: now.Add(-1 * time.Hour), cost: decimal.RequireFromString("6"), tokens: 60},
		{tenantID: 7001, apiKeyID: 9102, userID: 52, settledAt: now.Add(-2 * time.Hour), cost: decimal.RequireFromString("4"), tokens: 40},
		{tenantID: 7002, apiKeyID: 9201, userID: 53, settledAt: now.Add(-1 * time.Hour), cost: decimal.RequireFromString("99"), tokens: 990},
	}}
	rec := invoke(NewLeaderboardHandler(store), "/v1/admin/usage/leaderboard?by=api_key&window=24h&limit=10&tenant_id=7001")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Entries []struct {
			Key       string `json:"key"`
			TotalCost string `json:"total_cost"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body=%s err=%v", rec.Body.String(), err)
	}
	if store.apiKeyArg.TenantID != 7001 {
		t.Fatalf("tenant arg=%d want 7001; mutation dropping tenant_id parse passes zero to SQL", store.apiKeyArg.TenantID)
	}
	if len(body.Entries) != 2 {
		t.Fatalf("entries=%v want only tenant 7001 keys; mutation dropping tenant filter admits tenant 7002 key 9201", body.Entries)
	}
	for _, entry := range body.Entries {
		if entry.Key == "9201" || entry.TotalCost == "99.00000000" {
			t.Fatalf("entry=%+v leaked tenant 7002; mutation dropping SQL tenant filter is caught", entry)
		}
	}
}

func TestLeaderboard_ByDispatchAndCaps(t *testing.T) {
	for _, tc := range []struct {
		target string
		want   string
	}{
		{"/v1/admin/usage/leaderboard?window=7d", "user"},
		{"/v1/admin/usage/leaderboard?by=model&window=7d", "model"},
		{"/v1/admin/usage/leaderboard?by=provider_account&window=120d&limit=999", "provider_account"},
		{"/v1/admin/usage/leaderboard?by=api_key&window=7d", "api_key"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			store := &leaderboardQueryStub{}
			rec := invoke(NewLeaderboardHandler(store), tc.target)
			if rec.Code != http.StatusOK {
				t.Fatalf("code=%d want 200 body=%s", rec.Code, rec.Body.String())
			}
			if store.called != tc.want {
				t.Fatalf("called=%q want %q", store.called, tc.want)
			}
			if tc.want == "provider_account" && store.providerArg.RowLimit != 100 {
				t.Fatalf("capped limit=%d want 100", store.providerArg.RowLimit)
			}
			if tc.want == "provider_account" {
				age := time.Since(store.providerArg.SettledSince.Time)
				if age < maxLeaderboardWindow || age > maxLeaderboardWindow+5*time.Second {
					t.Fatalf("capped window age=%s want about %s", age, maxLeaderboardWindow)
				}
			}
		})
	}
}

func TestLeaderboard_InvalidParamsDoNotQuery(t *testing.T) {
	for _, target := range []string{
		"/v1/admin/usage/leaderboard?by=credential&window=24h",
		"/v1/admin/usage/leaderboard?by=api_key&window=24h&tenant_id=abc",
		"/v1/admin/usage/leaderboard?by=user",
		"/v1/admin/usage/leaderboard?by=user&window=-1h",
		"/v1/admin/usage/leaderboard?by=user&window=24h&limit=abc",
	} {
		t.Run(strings.ReplaceAll(target, "/", "_"), func(t *testing.T) {
			store := &leaderboardQueryStub{}
			rec := invoke(NewLeaderboardHandler(store), target)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code=%d want 400 body=%s", rec.Code, rec.Body.String())
			}
			if store.called != "" {
				t.Fatalf("invalid request called %q query", store.called)
			}
		})
	}
}

func TestLeaderboardSnapshotCacheHitsWithinTTLAndSetsHeader(t *testing.T) {
	oldTTL := leaderboardSnapshotTTL
	leaderboardSnapshotTTL = time.Minute
	defer func() { leaderboardSnapshotTTL = oldTTL }()

	store := &leaderboardQueryStub{events: []leaderboardSeedEvent{
		{userID: 501, settledAt: time.Now().UTC().Add(-30 * time.Minute), cost: decimal.RequireFromString("2.00"), tokens: 20},
	}}
	h := NewLeaderboardHandler(store)
	target := "/v1/admin/usage/leaderboard?by=user&window=41h&limit=3"
	first := invoke(h, target)
	if first.Code != http.StatusOK {
		t.Fatalf("first code=%d want 200 body=%s", first.Code, first.Body.String())
	}
	if first.Header().Get(snapshotCacheHeader) != "miss" {
		t.Fatalf("first %s=%q want miss", snapshotCacheHeader, first.Header().Get(snapshotCacheHeader))
	}
	second := invoke(h, target)
	if second.Code != http.StatusOK {
		t.Fatalf("second code=%d want 200 body=%s", second.Code, second.Body.String())
	}
	if got := store.callCount(); got != 1 {
		t.Fatalf("query calls=%d want 1; mutation removing GetOrLoad calls backend twice", got)
	}
	if second.Header().Get(snapshotCacheHeader) != "hit" {
		t.Fatalf("second %s=%q want hit; mutation without cache wrapper reports miss/empty and queries again", snapshotCacheHeader, second.Header().Get(snapshotCacheHeader))
	}
}

func TestLeaderboardSnapshotCacheExpiresAfterTTL(t *testing.T) {
	oldTTL := leaderboardSnapshotTTL
	leaderboardSnapshotTTL = 15 * time.Millisecond
	defer func() { leaderboardSnapshotTTL = oldTTL }()

	store := &leaderboardQueryStub{}
	h := NewLeaderboardHandler(store)
	target := "/v1/admin/usage/leaderboard?by=model&window=42h&limit=4"
	first := invoke(h, target)
	if first.Code != http.StatusOK {
		t.Fatalf("first code=%d want 200 body=%s", first.Code, first.Body.String())
	}
	time.Sleep(25 * time.Millisecond)
	second := invoke(h, target)
	if second.Code != http.StatusOK {
		t.Fatalf("second code=%d want 200 body=%s", second.Code, second.Body.String())
	}
	if second.Header().Get(snapshotCacheHeader) != "miss" {
		t.Fatalf("second %s=%q want miss after TTL expiry; mutation ignoring expiry returns stale hit", snapshotCacheHeader, second.Header().Get(snapshotCacheHeader))
	}
	if got := store.callCount(); got != 2 {
		t.Fatalf("query calls=%d want 2 after TTL expiry", got)
	}
}

func TestLeaderboardSnapshotCacheCoalescesConcurrentSameKeyMisses(t *testing.T) {
	oldTTL := leaderboardSnapshotTTL
	leaderboardSnapshotTTL = time.Minute
	defer func() { leaderboardSnapshotTTL = oldTTL }()

	store := &leaderboardQueryStub{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	h := NewLeaderboardHandler(store)
	target := "/v1/admin/usage/leaderboard?by=provider_account&window=43h&limit=5"

	const workers = 40
	var wg sync.WaitGroup
	results := make(chan int, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- invoke(h, target).Code
		}()
	}
	<-store.started
	time.Sleep(20 * time.Millisecond)
	close(store.release)
	wg.Wait()
	close(results)
	for code := range results {
		if code != http.StatusOK {
			t.Fatalf("code=%d want 200", code)
		}
	}
	if got := store.callCount(); got != 1 {
		t.Fatalf("query calls=%d want 1; mutation removing inflight coalescing stampedes backend", got)
	}
}

func TestLeaderboardSnapshotCacheKeysSeparateByWindowLimitAndDimension(t *testing.T) {
	oldTTL := leaderboardSnapshotTTL
	leaderboardSnapshotTTL = time.Minute
	defer func() { leaderboardSnapshotTTL = oldTTL }()

	store := &leaderboardQueryStub{}
	h := NewLeaderboardHandler(store)
	targets := []string{
		"/v1/admin/usage/leaderboard?by=user&window=44h&limit=1",
		"/v1/admin/usage/leaderboard?by=user&window=44h&limit=2",
		"/v1/admin/usage/leaderboard?by=user&window=45h&limit=1",
		"/v1/admin/usage/leaderboard?by=model&window=44h&limit=1",
	}
	for i, target := range targets {
		rec := invoke(h, target)
		if rec.Code != http.StatusOK {
			t.Fatalf("target[%d] code=%d want 200 body=%s", i, rec.Code, rec.Body.String())
		}
		if rec.Header().Get(snapshotCacheHeader) != "miss" {
			t.Fatalf("target[%d] %s=%q want miss; cache key must include by/window/limit", i, snapshotCacheHeader, rec.Header().Get(snapshotCacheHeader))
		}
	}
	if got := store.callCount(); got != len(targets) {
		t.Fatalf("query calls=%d want %d independent keys; mutation dropping by/window/limit aliases requests", got, len(targets))
	}
	if rec := invoke(h, targets[0]); rec.Header().Get(snapshotCacheHeader) != "hit" {
		t.Fatalf("repeat base %s=%q want hit", snapshotCacheHeader, rec.Header().Get(snapshotCacheHeader))
	}
}
