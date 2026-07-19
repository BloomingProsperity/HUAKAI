package servermonitorhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/servermonitor"
)

type fakeStore struct {
	nodes        []servermonitor.Node
	node         servermonitor.Node
	history      []servermonitor.HistoryPoint
	listErr      error
	getErr       error
	historyErr   error
	historyLimit int
}

func (f *fakeStore) ListNodes(context.Context, time.Time, time.Duration, int, int) ([]servermonitor.Node, error) {
	return f.nodes, f.listErr
}

func (f *fakeStore) GetNode(context.Context, string, time.Time, time.Duration) (servermonitor.Node, error) {
	return f.node, f.getErr
}

func (f *fakeStore) ListHistory(_ context.Context, _ string, _, _ time.Time, limit int) ([]servermonitor.HistoryPoint, error) {
	f.historyLimit = limit
	return f.history, f.historyErr
}

func TestHistoryRollupKeepsLastPointAndGapsMissing(t *testing.T) {
	now := time.Date(2026, 7, 19, 6, 0, 0, 0, time.UTC)
	store := &fakeStore{
		node: servermonitor.Node{Identity: servermonitor.Identity{NodeID: "node-test-01"}},
		history: []servermonitor.HistoryPoint{
			{BucketAt: now.Add(-29 * time.Minute), CollectedAt: now.Add(-29 * time.Minute), Sequence: 1},
			{BucketAt: now.Add(-20 * time.Minute), CollectedAt: now.Add(-20 * time.Minute), Sequence: 2},
			{BucketAt: now.Add(-2 * time.Minute), CollectedAt: now.Add(-2 * time.Minute), Sequence: 3},
		},
	}
	h := New(store, 90*time.Second)
	h.now = func() time.Time { return now }
	router := chi.NewRouter()
	router.Get("/nodes/{node_id}/history", h.History)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/nodes/node-test-01/history?bucket=15m&from=2026-07-19T05:00:00Z&to=2026-07-19T06:00:00Z", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response HistoryResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.GapSemantics != "missing" || response.BucketSeconds != 900 {
		t.Fatalf("history metadata=%+v", response)
	}
	if len(response.Points) != 2 {
		t.Fatalf("rollup points=%+v want 2；中间空桶不能补零", response.Points)
	}
	if response.Points[0].Sequence != 2 || response.Points[1].Sequence != 3 {
		t.Fatalf("rollup 未保留桶内最后样本: %+v", response.Points)
	}
	if store.historyLimit != 50000 {
		t.Fatalf("history limit=%d want 50000", store.historyLimit)
	}
}

func TestHistoryRejectsOversizedMinuteRange(t *testing.T) {
	now := time.Date(2026, 7, 19, 6, 0, 0, 0, time.UTC)
	h := New(&fakeStore{node: servermonitor.Node{}}, 90*time.Second)
	h.now = func() time.Time { return now }
	router := chi.NewRouter()
	router.Get("/nodes/{node_id}/history", h.History)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/nodes/node-test-01/history?from=2026-07-01T00:00:00Z&to=2026-07-19T06:00:00Z", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "history_range_too_large") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDetailNotFoundAndBackendErrorAreStable(t *testing.T) {
	h := New(&fakeStore{getErr: servermonitor.ErrNodeNotFound}, time.Minute)
	router := chi.NewRouter()
	router.Get("/nodes/{node_id}", h.Detail)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nodes/node-test-01", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "server_monitor_node_not_found") {
		t.Fatalf("not found status=%d body=%s", rec.Code, rec.Body.String())
	}

	h = New(&fakeStore{getErr: errors.New("secret host path /var/lib/postgresql")}, time.Minute)
	router = chi.NewRouter()
	router.Get("/nodes/{node_id}", h.Detail)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nodes/node-test-01", nil))
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "/var/lib") {
		t.Fatalf("backend error leaked: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestListReturnsEmptyArray(t *testing.T) {
	h := New(&fakeStore{}, time.Minute)
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/nodes", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"nodes":[]`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
