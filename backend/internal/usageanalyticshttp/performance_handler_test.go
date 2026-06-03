package usageanalyticshttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type performanceSeedEvent struct {
	model             string
	providerAccountID *int64
	settledAt         time.Time
	requestedAt       time.Time
	firstByteAt       *time.Time
	lastEventAt       *time.Time
	tokensOutput      int64
	endClass          string
}

type performanceQueryStub struct {
	events      []performanceSeedEvent
	called      string
	modelArg    dbbilling.AggregateUsagePerformanceByModelParams
	providerArg dbbilling.AggregateUsagePerformanceByProviderAccountParams
}

func (s *performanceQueryStub) AggregateUsageLeaderboardByUser(context.Context, dbbilling.AggregateUsageLeaderboardByUserParams) ([]dbbilling.AggregateUsageLeaderboardByUserRow, error) {
	return nil, nil
}

func (s *performanceQueryStub) AggregateUsageLeaderboardByModel(context.Context, dbbilling.AggregateUsageLeaderboardByModelParams) ([]dbbilling.AggregateUsageLeaderboardByModelRow, error) {
	return nil, nil
}

func (s *performanceQueryStub) AggregateUsageLeaderboardByProviderAccount(context.Context, dbbilling.AggregateUsageLeaderboardByProviderAccountParams) ([]dbbilling.AggregateUsageLeaderboardByProviderAccountRow, error) {
	return nil, nil
}

func (s *performanceQueryStub) AggregateUsagePerformanceByModel(_ context.Context, arg dbbilling.AggregateUsagePerformanceByModelParams) ([]dbbilling.AggregateUsagePerformanceByModelRow, error) {
	s.called = "model"
	s.modelArg = arg
	rows := s.aggregate(arg.SettledSince, arg.RowLimit, func(e performanceSeedEvent) string {
		return e.model
	})
	out := make([]dbbilling.AggregateUsagePerformanceByModelRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, dbbilling.AggregateUsagePerformanceByModelRow(row))
	}
	return out, nil
}

func (s *performanceQueryStub) AggregateUsagePerformanceByProviderAccount(_ context.Context, arg dbbilling.AggregateUsagePerformanceByProviderAccountParams) ([]dbbilling.AggregateUsagePerformanceByProviderAccountRow, error) {
	s.called = "provider_account"
	s.providerArg = arg
	rows := s.aggregate(arg.SettledSince, arg.RowLimit, func(e performanceSeedEvent) string {
		if e.providerAccountID == nil {
			return "unassigned"
		}
		return strconv.FormatInt(*e.providerAccountID, 10)
	})
	out := make([]dbbilling.AggregateUsagePerformanceByProviderAccountRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, dbbilling.AggregateUsagePerformanceByProviderAccountRow(row))
	}
	return out, nil
}

func (s *performanceQueryStub) AggregateUsageOverviewTotals(context.Context, pgtype.Timestamptz) (dbbilling.AggregateUsageOverviewTotalsRow, error) {
	return dbbilling.AggregateUsageOverviewTotalsRow{}, nil
}

func (s *performanceQueryStub) AggregateUsageOverviewTrendByDay(context.Context, pgtype.Timestamptz) ([]dbbilling.AggregateUsageOverviewTrendByDayRow, error) {
	return nil, nil
}

type performanceAggregateRow struct {
	Key          string
	AvgTtftMs    string
	AvgTps       string
	RequestCount int64
	ErrorCount   int64
}

func (s *performanceQueryStub) aggregate(since pgtype.Timestamptz, limit int32, keyFor func(performanceSeedEvent) string) []performanceAggregateRow {
	type agg struct {
		requests int64
		errors   int64
		ttftSum  float64
		ttftN    int64
		tpsSum   float64
		tpsN     int64
	}
	byKey := map[string]agg{}
	for _, event := range s.events {
		if since.Valid && event.settledAt.Before(since.Time) {
			continue
		}
		key := keyFor(event)
		current := byKey[key]
		current.requests++
		if event.endClass != "stream_end_graceful" && event.endClass != "non_streaming" {
			current.errors++
		}
		if event.firstByteAt != nil {
			current.ttftSum += event.firstByteAt.Sub(event.requestedAt).Seconds() * 1000
			current.ttftN++
		}
		if event.firstByteAt != nil && event.lastEventAt != nil && event.tokensOutput > 0 {
			seconds := event.lastEventAt.Sub(*event.firstByteAt).Seconds()
			if seconds != 0 {
				current.tpsSum += float64(event.tokensOutput) / seconds
				current.tpsN++
			}
		}
		byKey[key] = current
	}
	rows := make([]performanceAggregateRow, 0, len(byKey))
	for key, current := range byKey {
		rows = append(rows, performanceAggregateRow{
			Key:          key,
			AvgTtftMs:    fixedAverage(current.ttftSum, current.ttftN),
			AvgTps:       fixedAverage(current.tpsSum, current.tpsN),
			RequestCount: current.requests,
			ErrorCount:   current.errors,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].RequestCount == rows[j].RequestCount {
			return rows[i].Key < rows[j].Key
		}
		return rows[i].RequestCount > rows[j].RequestCount
	})
	if limit > 0 && len(rows) > int(limit) {
		rows = rows[:limit]
	}
	return rows
}

func fixedAverage(sum float64, n int64) string {
	if n == 0 {
		return "0"
	}
	return fmt.Sprintf("%.6f", sum/float64(n))
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func TestPerformance_ModelAggregatesLatencyThroughputErrorRateOrderAndLimit(t *testing.T) {
	now := time.Now().UTC()
	req := now.Add(-1 * time.Hour)
	store := &performanceQueryStub{events: []performanceSeedEvent{
		{
			model: "gpt-fast", settledAt: now.Add(-10 * time.Minute), requestedAt: req,
			firstByteAt: ptrTime(req.Add(100 * time.Millisecond)), lastEventAt: ptrTime(req.Add(1100 * time.Millisecond)),
			tokensOutput: 50, endClass: "stream_end_graceful",
		},
		{
			model: "gpt-fast", settledAt: now.Add(-9 * time.Minute), requestedAt: req,
			firstByteAt: ptrTime(req.Add(300 * time.Millisecond)), tokensOutput: 25, endClass: "upstream_5xx",
		},
		{
			model: "gpt-fast", settledAt: now.Add(-8 * time.Minute), requestedAt: req,
			tokensOutput: 10, endClass: "non_streaming",
		},
		{
			model: "gpt-stable", settledAt: now.Add(-7 * time.Minute), requestedAt: req,
			firstByteAt: ptrTime(req.Add(200 * time.Millisecond)), lastEventAt: ptrTime(req.Add(2200 * time.Millisecond)),
			tokensOutput: 100, endClass: "stream_end_graceful",
		},
		{
			model: "gpt-stable", settledAt: now.Add(-6 * time.Minute), requestedAt: req,
			firstByteAt: ptrTime(req.Add(400 * time.Millisecond)), lastEventAt: ptrTime(req.Add(400 * time.Millisecond)),
			tokensOutput: 100, endClass: "non_streaming",
		},
		{model: "gpt-rare", settledAt: now.Add(-5 * time.Minute), requestedAt: req, endClass: "stream_end_graceful"},
		{model: "old-model", settledAt: now.Add(-25 * time.Hour), requestedAt: req, endClass: "upstream_5xx"},
		{model: "old-model", settledAt: now.Add(-25 * time.Hour), requestedAt: req, endClass: "upstream_5xx"},
		{model: "old-model", settledAt: now.Add(-25 * time.Hour), requestedAt: req, endClass: "upstream_5xx"},
		{model: "old-model", settledAt: now.Add(-25 * time.Hour), requestedAt: req, endClass: "upstream_5xx"},
	}}
	rec := invoke(NewPerformanceHandler(store), "/v1/admin/usage/performance?by=model&window=24h&limit=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Window  string `json:"window"`
		By      string `json:"by"`
		Entries []struct {
			Rank         int    `json:"rank"`
			Key          string `json:"key"`
			AvgTTFTMS    string `json:"avg_ttft_ms"`
			AvgTPS       string `json:"avg_tps"`
			RequestCount int64  `json:"request_count"`
			ErrorRate    string `json:"error_rate"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body=%s err=%v", rec.Body.String(), err)
	}
	if body.Window != "24h" || body.By != "model" {
		t.Fatalf("window/by=%q/%q want 24h/model", body.Window, body.By)
	}
	if len(body.Entries) != 2 {
		t.Fatalf("entries=%v want exactly fast and stable; mutation without LIMIT admits rare, without window admits old-model", body.Entries)
	}
	fast := body.Entries[0]
	if fast.Rank != 1 || fast.Key != "gpt-fast" || fast.RequestCount != 3 {
		t.Fatalf("first entry=%+v want rank=1 key=gpt-fast count=3; mutation without request_count DESC reorders", fast)
	}
	if fast.AvgTTFTMS != "200.0000" {
		t.Fatalf("avg_ttft_ms=%s want 200.0000; mutation counting nil first_byte as zero gives 133.3333", fast.AvgTTFTMS)
	}
	if fast.AvgTPS != "50.0000" {
		t.Fatalf("avg_tps=%s want 50.0000; mutation using rows without last_event or zero seconds changes/invalidates TPS", fast.AvgTPS)
	}
	if fast.ErrorRate != "0.3333" {
		t.Fatalf("error_rate=%s want 0.3333; mutation counting success end_class as errors gives 1.0000", fast.ErrorRate)
	}
	stable := body.Entries[1]
	if stable.Rank != 2 || stable.Key != "gpt-stable" || stable.RequestCount != 2 || stable.AvgTPS != "50.0000" || stable.ErrorRate != "0.0000" {
		t.Fatalf("second entry=%+v want stable count=2 avg_tps=50.0000 error_rate=0.0000", stable)
	}
	if store.called != "model" {
		t.Fatalf("called=%q want model", store.called)
	}
	if store.modelArg.RowLimit != 2 {
		t.Fatalf("limit arg=%d want 2", store.modelArg.RowLimit)
	}
	if !store.modelArg.SettledSince.Valid || now.Sub(store.modelArg.SettledSince.Time) > 25*time.Hour {
		t.Fatalf("settled_since=%v must be a valid 24h cutoff excluding old-model", store.modelArg.SettledSince)
	}
}

func TestPerformance_ByDispatchDefaultsAndCaps(t *testing.T) {
	now := time.Now().UTC()
	accountID := int64(42)
	for _, tc := range []struct {
		name   string
		target string
		want   string
	}{
		{"default_model", "/v1/admin/usage/performance?window=7d", "model"},
		{"provider_caps", "/v1/admin/usage/performance?by=provider_account&window=120d&limit=999", "provider_account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &performanceQueryStub{events: []performanceSeedEvent{
				{model: "m", providerAccountID: &accountID, settledAt: now, requestedAt: now, endClass: "stream_end_graceful"},
			}}
			rec := invoke(NewPerformanceHandler(store), tc.target)
			if rec.Code != http.StatusOK {
				t.Fatalf("code=%d want 200 body=%s", rec.Code, rec.Body.String())
			}
			if store.called != tc.want {
				t.Fatalf("called=%q want %q", store.called, tc.want)
			}
			if tc.want == "provider_account" {
				if store.providerArg.RowLimit != 100 {
					t.Fatalf("capped limit=%d want 100", store.providerArg.RowLimit)
				}
				age := time.Since(store.providerArg.SettledSince.Time)
				if age < maxLeaderboardWindow || age > maxLeaderboardWindow+5*time.Second {
					t.Fatalf("capped window age=%s want about %s", age, maxLeaderboardWindow)
				}
			}
		})
	}
}

func TestPerformance_InvalidParamsDoNotQuery(t *testing.T) {
	for _, target := range []string{
		"/v1/admin/usage/performance?by=user&window=24h",
		"/v1/admin/usage/performance",
		"/v1/admin/usage/performance?window=-1h",
		"/v1/admin/usage/performance?window=24h&limit=abc",
	} {
		t.Run(strings.ReplaceAll(target, "/", "_"), func(t *testing.T) {
			store := &performanceQueryStub{}
			rec := invoke(NewPerformanceHandler(store), target)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code=%d want 400 body=%s", rec.Code, rec.Body.String())
			}
			if store.called != "" {
				t.Fatalf("invalid request called %q query", store.called)
			}
		})
	}
}
