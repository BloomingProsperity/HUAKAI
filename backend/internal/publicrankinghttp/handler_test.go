package publicrankinghttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type rankingStoreStub struct {
	rows  []dbbilling.AggregateUsageLeaderboardByModelRow
	calls int
	arg   dbbilling.AggregateUsageLeaderboardByModelParams
}

func (s *rankingStoreStub) AggregateUsageLeaderboardByModel(_ context.Context, arg dbbilling.AggregateUsageLeaderboardByModelParams) ([]dbbilling.AggregateUsageLeaderboardByModelRow, error) {
	s.calls++
	s.arg = arg
	out := append([]dbbilling.AggregateUsageLeaderboardByModelRow(nil), s.rows...)
	if arg.RowLimit > 0 && len(out) > int(arg.RowLimit) {
		out = out[:arg.RowLimit]
	}
	return out, nil
}

// Mutation: adding an API-key, session, or admin auth gate to the public
// rankings handler would turn this no-header request into 401 and fail.
func TestPublicRankingsNoAuth(t *testing.T) {
	store := &rankingStoreStub{rows: []dbbilling.AggregateUsageLeaderboardByModelRow{
		{Key: "gpt-4.1-mini", TotalCost: "123.45000000", TotalTokens: 9000, RequestCount: 7},
	}}
	handler := NewHandler(Deps{Store: store})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/rankings?limit=1", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	body := decodeRankingsResponse(t, rec)
	if len(body.Rankings) != 1 || body.Rankings[0].Model != "gpt-4.1-mini" || body.Rankings[0].RequestCount != 7 {
		t.Fatalf("rankings=%+v want public model ranking with request_count", body.Rankings)
	}
	if store.calls != 1 {
		t.Fatalf("store calls=%d want 1", store.calls)
	}
}

// Mutation: returning the raw aggregate row or adding total_cost, actual_cost,
// user_id, api_key_id, or provider_account_id would put that forbidden field
// name in the response JSON and fail.
func TestPublicRankingsProjection_NoCostOrIdentity(t *testing.T) {
	store := &rankingStoreStub{rows: []dbbilling.AggregateUsageLeaderboardByModelRow{
		{Key: "model-with-private-cost", TotalCost: "999999.99000000", TotalTokens: 11, RequestCount: 3},
	}}
	handler := NewHandler(Deps{Store: store})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/rankings?limit=2", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	bodyText := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{
		"actual_cost",
		"total_cost",
		"user_id",
		"api_key_id",
		"provider_account_id",
	} {
		if strings.Contains(bodyText, forbidden) {
			t.Fatalf("response body leaks %q: %s", forbidden, rec.Body.String())
		}
	}
	body := decodeRankingsResponse(t, rec)
	if len(body.Rankings) != 1 {
		t.Fatalf("rankings=%+v want one public ranking", body.Rankings)
	}
	if body.Rankings[0].Model != "model-with-private-cost" || body.Rankings[0].RequestCount != 3 {
		t.Fatalf("ranking=%+v want model plus request_count only", body.Rankings[0])
	}
}

// Mutation: applying the client limit as the response size, or passing the
// client limit through as the aggregate fetch limit before usage-volume sort,
// would return more than 100 entries or use row_limit=1000 and fail.
func TestPublicRankingsLimitCap(t *testing.T) {
	rows := make([]dbbilling.AggregateUsageLeaderboardByModelRow, 0, maxPublicRankingsLimit+5)
	for i := 0; i < maxPublicRankingsLimit+5; i++ {
		rows = append(rows, dbbilling.AggregateUsageLeaderboardByModelRow{
			Key:          "model-" + strconv.Itoa(i),
			TotalCost:    "1.00000000",
			TotalTokens:  int64(i + 1),
			RequestCount: int64(maxPublicRankingsLimit + 5 - i),
		})
	}
	store := &rankingStoreStub{rows: rows}
	handler := NewHandler(Deps{Store: store})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/rankings?limit=1000", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if store.arg.RowLimit != publicRankingsFetchLimit {
		t.Fatalf("row limit arg=%d want fixed internal fetch limit, not client limit 1000", store.arg.RowLimit)
	}
	body := decodeRankingsResponse(t, rec)
	if len(body.Rankings) != maxPublicRankingsLimit {
		t.Fatalf("rankings=%d want cap %d", len(body.Rankings), maxPublicRankingsLimit)
	}
}

// Mutation: preserving the admin aggregate's cost ordering instead of sorting
// by public usage volume would rank expensive-rare first and fail.
func TestPublicRankingsOrdering(t *testing.T) {
	store := &rankingStoreStub{rows: []dbbilling.AggregateUsageLeaderboardByModelRow{
		{Key: "expensive-rare", TotalCost: "1000.00000000", TotalTokens: 10, RequestCount: 1},
		{Key: "cheap-popular", TotalCost: "0.01000000", TotalTokens: 1000, RequestCount: 200},
		{Key: "middle-volume", TotalCost: "2.00000000", TotalTokens: 500, RequestCount: 50},
	}}
	handler := NewHandler(Deps{Store: store})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/rankings?limit=3", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	body := decodeRankingsResponse(t, rec)
	got := make([]string, 0, len(body.Rankings))
	for _, ranking := range body.Rankings {
		got = append(got, ranking.Model)
	}
	want := []string{"cheap-popular", "middle-volume", "expensive-rare"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order=%v want %v by request_count desc", got, want)
	}
}

// Mutation: removing the shared snapshot cache wrapper would call the store
// twice and mark the second response as a cache miss.
func TestPublicRankingsUsesSnapshotCache(t *testing.T) {
	store := &rankingStoreStub{rows: []dbbilling.AggregateUsageLeaderboardByModelRow{
		{Key: "cached-model", TotalCost: "1.00000000", TotalTokens: 100, RequestCount: 10},
	}}
	handler := NewHandler(Deps{Store: store})

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/v1/public/rankings?limit=4", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s want 200", first.Code, first.Body.String())
	}
	if first.Header().Get(snapshotCacheHeader) != "miss" {
		t.Fatalf("first %s=%q want miss", snapshotCacheHeader, first.Header().Get(snapshotCacheHeader))
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/v1/public/rankings?limit=4", nil))
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s want 200", second.Code, second.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("store calls=%d want 1; mutation removing GetOrLoad calls backend twice", store.calls)
	}
	if second.Header().Get(snapshotCacheHeader) != "hit" {
		t.Fatalf("second %s=%q want hit", snapshotCacheHeader, second.Header().Get(snapshotCacheHeader))
	}
}

type decodedRankingsResponse struct {
	Scope    string `json:"scope"`
	Metric   string `json:"metric"`
	Rankings []struct {
		Rank         int    `json:"rank"`
		Model        string `json:"model"`
		RequestCount int64  `json:"request_count"`
		TokenTotal   int64  `json:"token_total"`
		RequestShare string `json:"request_share"`
	} `json:"rankings"`
}

func decodeRankingsResponse(t *testing.T, rec *httptest.ResponseRecorder) decodedRankingsResponse {
	t.Helper()
	var body decodedRankingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body=%s err=%v", rec.Body.String(), err)
	}
	if body.Scope != "platform" {
		t.Fatalf("scope=%q want platform", body.Scope)
	}
	if body.Metric != "request_count" {
		t.Fatalf("metric=%q want request_count", body.Metric)
	}
	for i, ranking := range body.Rankings {
		if ranking.Rank != i+1 {
			t.Fatalf("ranking[%d].rank=%d want %d", i, ranking.Rank, i+1)
		}
		if ranking.Model == "" {
			t.Fatalf("ranking[%d] missing model", i)
		}
		if ranking.RequestCount <= 0 {
			t.Fatalf("ranking[%d].request_count=%d want positive", i, ranking.RequestCount)
		}
		if ranking.TokenTotal < 0 {
			t.Fatalf("ranking[%d].token_total=%d want non-negative", i, ranking.TokenTotal)
		}
		if ranking.RequestShare == "" {
			t.Fatalf("ranking[%d] missing request_share", i)
		}
	}
	return body
}

var _ Store = (*rankingStoreStub)(nil)
