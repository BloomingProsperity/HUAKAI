package usageanalyticshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type leaderboardSeedEvent struct {
	userID            int64
	model             string
	providerAccountID *int64
	settledAt         time.Time
	cost              decimal.Decimal
	tokens            int64
}

type leaderboardQueryStub struct {
	events      []leaderboardSeedEvent
	called      string
	userArg     dbbilling.AggregateUsageLeaderboardByUserParams
	modelArg    dbbilling.AggregateUsageLeaderboardByModelParams
	providerArg dbbilling.AggregateUsageLeaderboardByProviderAccountParams
}

func (s *leaderboardQueryStub) AggregateUsageLeaderboardByUser(_ context.Context, arg dbbilling.AggregateUsageLeaderboardByUserParams) ([]dbbilling.AggregateUsageLeaderboardByUserRow, error) {
	s.called = "user"
	s.userArg = arg
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
	s.called = "model"
	s.modelArg = arg
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
	s.called = "provider_account"
	s.providerArg = arg
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

type leaderboardAggregateRow struct {
	Key          string
	TotalCost    string
	TotalTokens  int64
	RequestCount int64
}

func (s *leaderboardQueryStub) aggregate(since pgtype.Timestamptz, limit int32, keyFor func(leaderboardSeedEvent) string) []leaderboardAggregateRow {
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

func TestLeaderboard_ByDispatchAndCaps(t *testing.T) {
	for _, tc := range []struct {
		target string
		want   string
	}{
		{"/v1/admin/usage/leaderboard?window=7d", "user"},
		{"/v1/admin/usage/leaderboard?by=model&window=7d", "model"},
		{"/v1/admin/usage/leaderboard?by=provider_account&window=120d&limit=999", "provider_account"},
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
		"/v1/admin/usage/leaderboard?by=api_key&window=24h",
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
