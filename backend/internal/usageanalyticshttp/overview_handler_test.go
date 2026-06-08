package usageanalyticshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type overviewSeedEvent struct {
	userID       int64
	apiKeyID     int64
	model        string
	settledAt    time.Time
	cost         decimal.Decimal
	tokensInput  int64
	tokensOutput int64
	endClass     string
}

type overviewQueryStub struct {
	mu            sync.Mutex
	events        []overviewSeedEvent
	calls         []string
	totalsArg     pgtype.Timestamptz
	trendArg      pgtype.Timestamptz
	totalsStarted chan struct{}
	totalsRelease chan struct{}
	totalsOnce    sync.Once
}

func (s *overviewQueryStub) AggregateUsageLeaderboardByUser(context.Context, dbbilling.AggregateUsageLeaderboardByUserParams) ([]dbbilling.AggregateUsageLeaderboardByUserRow, error) {
	return nil, nil
}

func (s *overviewQueryStub) AggregateUsageLeaderboardByModel(context.Context, dbbilling.AggregateUsageLeaderboardByModelParams) ([]dbbilling.AggregateUsageLeaderboardByModelRow, error) {
	return nil, nil
}

func (s *overviewQueryStub) AggregateUsageLeaderboardByProviderAccount(context.Context, dbbilling.AggregateUsageLeaderboardByProviderAccountParams) ([]dbbilling.AggregateUsageLeaderboardByProviderAccountRow, error) {
	return nil, nil
}

func (s *overviewQueryStub) AggregateUsageLeaderboardByApiKey(context.Context, dbbilling.AggregateUsageLeaderboardByApiKeyParams) ([]dbbilling.AggregateUsageLeaderboardByApiKeyRow, error) {
	return nil, nil
}

func (s *overviewQueryStub) AggregateUsagePerformanceByModel(context.Context, dbbilling.AggregateUsagePerformanceByModelParams) ([]dbbilling.AggregateUsagePerformanceByModelRow, error) {
	return nil, nil
}

func (s *overviewQueryStub) AggregateUsagePerformanceByProviderAccount(context.Context, dbbilling.AggregateUsagePerformanceByProviderAccountParams) ([]dbbilling.AggregateUsagePerformanceByProviderAccountRow, error) {
	return nil, nil
}

func (s *overviewQueryStub) AggregateUsagePerformanceSummary(context.Context, dbbilling.AggregateUsagePerformanceSummaryParams) (dbbilling.AggregateUsagePerformanceSummaryRow, error) {
	return dbbilling.AggregateUsagePerformanceSummaryRow{}, nil
}

func (s *overviewQueryStub) AggregateUsageLatencyPercentiles(context.Context, dbbilling.AggregateUsageLatencyPercentilesParams) (dbbilling.AggregateUsageLatencyPercentilesRow, error) {
	return dbbilling.AggregateUsageLatencyPercentilesRow{}, nil
}

func (s *overviewQueryStub) AggregateUsagePerformanceByModelBucketed(context.Context, dbbilling.AggregateUsagePerformanceByModelBucketedParams) ([]dbbilling.AggregateUsagePerformanceByModelBucketedRow, error) {
	return nil, nil
}

func (s *overviewQueryStub) AggregateUsageOverviewTotals(_ context.Context, settledSince pgtype.Timestamptz) (dbbilling.AggregateUsageOverviewTotalsRow, error) {
	s.mu.Lock()
	s.calls = append(s.calls, "totals")
	s.totalsArg = settledSince
	s.mu.Unlock()
	if s.totalsStarted != nil && s.totalsRelease != nil {
		s.totalsOnce.Do(func() {
			close(s.totalsStarted)
		})
		<-s.totalsRelease
	}
	var requests, tokens, successes int64
	cost := decimal.Zero
	users := map[int64]struct{}{}
	keys := map[int64]struct{}{}
	for _, event := range s.events {
		if settledSince.Valid && event.settledAt.Before(settledSince.Time) {
			continue
		}
		requests++
		tokens += event.tokensInput + event.tokensOutput
		cost = cost.Add(event.cost)
		users[event.userID] = struct{}{}
		keys[event.apiKeyID] = struct{}{}
		if event.endClass == "stream_end_graceful" || event.endClass == "non_streaming" {
			successes++
		}
	}
	return dbbilling.AggregateUsageOverviewTotalsRow{
		RequestCount:  requests,
		TotalCost:     cost.StringFixed(8),
		TotalTokens:   tokens,
		ActiveUsers:   int64(len(users)),
		ActiveApiKeys: int64(len(keys)),
		SuccessCount:  successes,
	}, nil
}

func (s *overviewQueryStub) AggregateUsageOverviewTrendByDay(_ context.Context, settledSince pgtype.Timestamptz) ([]dbbilling.AggregateUsageOverviewTrendByDayRow, error) {
	s.mu.Lock()
	s.calls = append(s.calls, "trend")
	s.trendArg = settledSince
	s.mu.Unlock()
	type bucket struct {
		day      time.Time
		requests int64
		cost     decimal.Decimal
	}
	byDay := map[string]bucket{}
	for _, event := range s.events {
		if settledSince.Valid && event.settledAt.Before(settledSince.Time) {
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
	rows := make([]dbbilling.AggregateUsageOverviewTrendByDayRow, 0, len(byDay))
	for _, current := range byDay {
		rows = append(rows, dbbilling.AggregateUsageOverviewTrendByDayRow{
			Day:          pgtype.Timestamptz{Time: current.day, Valid: true},
			RequestCount: current.requests,
			TotalCost:    current.cost.StringFixed(8),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Day.Time.Before(rows[j].Day.Time)
	})
	return rows, nil
}

func (s *overviewQueryStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *overviewQueryStub) joinedCalls() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.calls, ",")
}

func TestOverviewTotalsTrendWindowAndRatesAreDiscriminating(t *testing.T) {
	now := time.Now().UTC()
	recentDay := now.Truncate(24 * time.Hour).Add(-12 * time.Hour)
	store := &overviewQueryStub{events: []overviewSeedEvent{
		{userID: 101, apiKeyID: 1001, model: "gpt-fast", settledAt: recentDay, cost: decimal.RequireFromString("4.50"), tokensInput: 100, tokensOutput: 200, endClass: "stream_end_graceful"},
		{userID: 101, apiKeyID: 1002, model: "gpt-fast", settledAt: recentDay.Add(time.Hour), cost: decimal.RequireFromString("1.25"), tokensInput: 50, tokensOutput: 50, endClass: "upstream_5xx"},
		{userID: 202, apiKeyID: 1002, model: "gpt-stable", settledAt: recentDay.Add(-24 * time.Hour), cost: decimal.RequireFromString("3.25"), tokensInput: 250, tokensOutput: 150, endClass: "non_streaming"},
		{userID: 202, apiKeyID: 1003, model: "gpt-stable", settledAt: recentDay.Add(-25 * time.Hour), cost: decimal.RequireFromString("4.50"), tokensInput: 100, tokensOutput: 100, endClass: "stream_end_graceful"},
		{userID: 303, apiKeyID: 1004, model: "old-expensive", settledAt: recentDay.Add(-8 * 24 * time.Hour), cost: decimal.RequireFromString("90.00"), tokensInput: 4000, tokensOutput: 5000, endClass: "upstream_5xx"},
	}}
	rec := invoke(NewOverviewHandler(store), "/v1/admin/usage/overview?window=7d")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Window string `json:"window"`
		Totals struct {
			Requests      int64  `json:"requests"`
			TotalCost     string `json:"total_cost"`
			TotalTokens   int64  `json:"total_tokens"`
			ActiveUsers   int64  `json:"active_users"`
			ActiveAPIKeys int64  `json:"active_api_keys"`
			SuccessRate   string `json:"success_rate"`
		} `json:"totals"`
		Trend []struct {
			Day      string `json:"day"`
			Requests int64  `json:"requests"`
			Cost     string `json:"cost"`
		} `json:"trend"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body=%s err=%v", rec.Body.String(), err)
	}
	if body.Window != "7d" {
		t.Fatalf("window=%q want 7d", body.Window)
	}
	if body.Totals.Requests != 4 || body.Totals.TotalCost != "13.50000000" || body.Totals.TotalTokens != 1000 {
		t.Fatalf("totals=%+v want requests=4 cost=13.50000000 tokens=1000; mutation without window admits old-expensive", body.Totals)
	}
	if body.Totals.ActiveUsers != 2 || body.Totals.ActiveAPIKeys != 3 {
		t.Fatalf("active users/keys=%d/%d want 2/3; mutation from COUNT(DISTINCT) to COUNT returns 4/4", body.Totals.ActiveUsers, body.Totals.ActiveAPIKeys)
	}
	if body.Totals.SuccessRate != "0.7500" {
		t.Fatalf("success_rate=%s want 0.7500; mutation dropping success end_class filter returns 1.0000", body.Totals.SuccessRate)
	}
	if len(body.Trend) != 2 {
		t.Fatalf("trend=%+v want two day buckets; mutation without day bucket or window changes bucket count", body.Trend)
	}
	wantTrend := map[string]struct {
		requests int64
		cost     string
	}{
		dayLabel(recentDay.Add(-24 * time.Hour)): {2, "7.75000000"},
		dayLabel(recentDay):                      {2, "5.75000000"},
	}
	for _, point := range body.Trend {
		want, ok := wantTrend[point.Day]
		if !ok {
			t.Fatalf("unexpected trend point=%+v want days %v", point, wantTrend)
		}
		if point.Requests != want.requests || point.Cost != want.cost {
			t.Fatalf("trend[%s]=%+v want requests=%d cost=%s", point.Day, point, want.requests, want.cost)
		}
	}
	if store.joinedCalls() != "totals,trend" {
		t.Fatalf("query calls=%v want totals then trend", store.joinedCalls())
	}
	if !store.totalsArg.Valid || !store.trendArg.Valid {
		t.Fatalf("overview queries must receive valid settled_since filters")
	}
	if now.Sub(store.totalsArg.Time) < 6*24*time.Hour {
		t.Fatalf("settled_since=%v must be about 7d; mutation using 24h would drop previous-day bucket", store.totalsArg)
	}
}

func TestOverviewWindowCapsAndInvalidParams(t *testing.T) {
	rec := invoke(NewOverviewHandler(&overviewQueryStub{}), "/v1/admin/usage/overview?window=120d")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var capped struct {
		Window string `json:"window"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &capped); err != nil {
		t.Fatalf("decode body=%s err=%v", rec.Body.String(), err)
	}
	if capped.Window != maxLeaderboardWindowText {
		t.Fatalf("window=%q want %s for capped 120d", capped.Window, maxLeaderboardWindowText)
	}

	for _, target := range []string{
		"/v1/admin/usage/overview",
		"/v1/admin/usage/overview?window=-1h",
		"/v1/admin/usage/overview?window=abc",
	} {
		t.Run(strings.ReplaceAll(target, "/", "_"), func(t *testing.T) {
			store := &overviewQueryStub{}
			rec := invoke(NewOverviewHandler(store), target)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code=%d want 400 body=%s", rec.Code, rec.Body.String())
			}
			if len(store.calls) != 0 {
				t.Fatalf("invalid request called queries %v", store.calls)
			}
		})
	}
}

func TestOverviewSnapshotCacheHitsWithinTTLAndSetsHeader(t *testing.T) {
	oldTTL := overviewSnapshotTTL
	overviewSnapshotTTL = time.Minute
	defer func() { overviewSnapshotTTL = oldTTL }()

	now := time.Now().UTC()
	store := &overviewQueryStub{events: []overviewSeedEvent{
		{userID: 11, apiKeyID: 101, settledAt: now.Add(-30 * time.Minute), cost: decimal.RequireFromString("1.00"), tokensInput: 10, tokensOutput: 5, endClass: "non_streaming"},
	}}
	h := NewOverviewHandler(store)
	first := invoke(h, "/v1/admin/usage/overview?window=6d")
	if first.Code != http.StatusOK {
		t.Fatalf("first code=%d want 200 body=%s", first.Code, first.Body.String())
	}
	if first.Header().Get("X-Snapshot-Cache") != "miss" {
		t.Fatalf("first X-Snapshot-Cache=%q want miss", first.Header().Get("X-Snapshot-Cache"))
	}
	second := invoke(h, "/v1/admin/usage/overview?window=6d")
	if second.Code != http.StatusOK {
		t.Fatalf("second code=%d want 200 body=%s", second.Code, second.Body.String())
	}
	if second.Header().Get("X-Snapshot-Cache") != "hit" {
		t.Fatalf("second X-Snapshot-Cache=%q want hit; mutation without cache header reports miss", second.Header().Get("X-Snapshot-Cache"))
	}
	if got := store.callCount(); got != 2 {
		t.Fatalf("query calls=%d want 2 totals+trend once; mutation without TTL cache calls 4 times", got)
	}
}

func TestOverviewSnapshotCacheExpiresAfterTTL(t *testing.T) {
	oldTTL := overviewSnapshotTTL
	overviewSnapshotTTL = 15 * time.Millisecond
	defer func() { overviewSnapshotTTL = oldTTL }()

	store := &overviewQueryStub{}
	h := NewOverviewHandler(store)
	first := invoke(h, "/v1/admin/usage/overview?window=13h")
	if first.Code != http.StatusOK {
		t.Fatalf("first code=%d want 200 body=%s", first.Code, first.Body.String())
	}
	time.Sleep(25 * time.Millisecond)
	second := invoke(h, "/v1/admin/usage/overview?window=13h")
	if second.Code != http.StatusOK {
		t.Fatalf("second code=%d want 200 body=%s", second.Code, second.Body.String())
	}
	if second.Header().Get("X-Snapshot-Cache") != "miss" {
		t.Fatalf("second X-Snapshot-Cache=%q want miss after TTL expiry; mutation ignoring expiry returns hit", second.Header().Get("X-Snapshot-Cache"))
	}
	if got := store.callCount(); got != 4 {
		t.Fatalf("query calls=%d want 4 totals+trend twice after expiry", got)
	}
}

func TestOverviewSnapshotCacheCoalescesConcurrentSameKeyMisses(t *testing.T) {
	oldTTL := overviewSnapshotTTL
	overviewSnapshotTTL = time.Minute
	defer func() { overviewSnapshotTTL = oldTTL }()

	store := &overviewQueryStub{
		totalsStarted: make(chan struct{}),
		totalsRelease: make(chan struct{}),
	}
	h := NewOverviewHandler(store)

	const workers = 50
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- invoke(h, "/v1/admin/usage/overview?window=17h")
		}()
	}
	<-store.totalsStarted
	time.Sleep(20 * time.Millisecond)
	close(store.totalsRelease)
	wg.Wait()
	close(results)

	for rec := range results {
		if rec.Code != http.StatusOK {
			t.Fatalf("code=%d want 200 body=%s", rec.Code, rec.Body.String())
		}
	}
	if got := store.callCount(); got != 2 {
		t.Fatalf("query calls=%d want 2 totals+trend once; mutation removing inflight coalescing lets concurrent loaders stampede", got)
	}
}

func dayLabel(t time.Time) string {
	return time.Date(t.UTC().Year(), t.UTC().Month(), t.UTC().Day(), 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}
