package meexporthttp

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type usageStoreStub struct {
	rows  []dbbilling.ListUsageRecordsRow
	got   dbbilling.ListUsageRecordsParams
	calls int
}

func (s *usageStoreStub) ListUsageRecords(_ context.Context, arg dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error) {
	s.calls++
	s.got = arg
	out := make([]dbbilling.ListUsageRecordsRow, 0, len(s.rows))
	for _, row := range s.rows {
		if arg.TenantID != nil && row.TenantID != *arg.TenantID {
			continue
		}
		if arg.APIKeyID != nil && row.APIKeyID != *arg.APIKeyID {
			continue
		}
		if arg.FromTs.Valid && row.CreatedAt.Time.Before(arg.FromTs.Time) {
			continue
		}
		if arg.ToTs.Valid && row.CreatedAt.Time.After(arg.ToTs.Time) {
			continue
		}
		if arg.HasCursor && row.CreatedAt.Valid && !(row.CreatedAt.Time.Before(arg.CursorCreatedAt.Time) || row.CreatedAt.Time.Equal(arg.CursorCreatedAt.Time) && row.ID < arg.CursorID) {
			continue
		}
		out = append(out, row)
	}
	if arg.PageLimit > 0 && int32(len(out)) > arg.PageLimit {
		out = out[:arg.PageLimit]
	}
	return out, nil
}

func TestMeExportSelfScoped(t *testing.T) {
	userA := sessionauth.SessionIdentity{TenantID: 7, UserID: 40}
	store := &usageStoreStub{rows: []dbbilling.ListUsageRecordsRow{
		meExportRow(3, userA.TenantID, 30, userA.UserID, "req-a", "claude-opus-4", "done"),
		meExportRow(2, userA.TenantID, 31, 41, "req-same-tenant-user-b", "gpt-4o", "done"),
		meExportRow(1, 8, 31, 41, "req-query-tenant", "gpt-4o-mini", "done"),
	}}
	h := NewHandler(Deps{Store: store, MaxRows: 10, FetchPageSize: 10})

	rec := invokeMeExport(h, "/v1/me/usage/export.csv?tenant_id=8&user_id=41&api_key_id=31&from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z", &userA)

	assertStatus(t, rec, http.StatusOK)
	if store.got.TenantID == nil || *store.got.TenantID != userA.TenantID {
		t.Fatalf("tenant scope=%v want authenticated tenant %d, not query tenant", store.got.TenantID, userA.TenantID)
	}
	if store.got.APIKeyID != nil {
		t.Fatalf("session export must not use caller-supplied api_key_id scope, got %d", *store.got.APIKeyID)
	}
	records := readCSV(t, rec.Body.String())
	// 变异:从查询字符串而非 session ident 取 scope -> store 读到 tenant 8 / api key 31,就不再只返回 req-a。
	// 变异:去掉按 ident TenantID+UserID 过滤行 -> req-same-tenant-user-b 泄露进 CSV,本测试变红。
	if len(records) != 2 {
		t.Fatalf("records=%v want header + user A row only", records)
	}
	if got := records[1][0]; got != "req-a" {
		t.Fatalf("exported request_id=%q want req-a; records=%v", got, records)
	}
	for _, leaked := range []string{"req-same-tenant-user-b", "req-query-tenant", "gpt-4o-mini"} {
		if strings.Contains(rec.Body.String(), leaked) {
			t.Fatalf("self export leaked %q in CSV: %s", leaked, rec.Body.String())
		}
	}
}

func TestMeExportCSVShape(t *testing.T) {
	ident := sessionauth.SessionIdentity{TenantID: 7, UserID: 40}
	store := &usageStoreStub{rows: []dbbilling.ListUsageRecordsRow{
		meExportRow(1, ident.TenantID, 30, ident.UserID, "req-shape", "claude-opus-4", "non_streaming"),
	}}
	h := NewHandler(Deps{Store: store, MaxRows: 10, FetchPageSize: 10})

	rec := invokeMeExport(h, "/v1/me/usage/export.csv?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z", &ident)

	assertStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Fatalf("Content-Type=%q want text/csv; charset=utf-8", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="me-usage-export.csv"` {
		t.Fatalf("Content-Disposition=%q want me usage attachment", got)
	}
	records := readCSV(t, rec.Body.String())
	// 变异:省略表头行的写入;records[0] 变成 req-shape,本断言失败。
	assertCSVRow(t, records[0], []string{"request_id", "model", "tokens_input", "tokens_output", "cost_usd", "created_at", "status"})
	if len(records) != 2 {
		t.Fatalf("records=%v want header + one data row", records)
	}
	assertCSVRow(t, records[1], []string{"req-shape", "claude-opus-4", "12", "34", "0.00012345", "2026-06-01T12:30:01Z", "non_streaming"})
}

func TestMeExportInjectionGuard(t *testing.T) {
	ident := sessionauth.SessionIdentity{TenantID: 7, UserID: 40}
	store := &usageStoreStub{rows: []dbbilling.ListUsageRecordsRow{
		meExportRow(1, ident.TenantID, 30, ident.UserID, "req-injection", "=cmd|' /C calc'!A0", "done"),
	}}
	h := NewHandler(Deps{Store: store, MaxRows: 10, FetchPageSize: 10})

	rec := invokeMeExport(h, "/v1/me/usage/export.csv?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z", &ident)

	assertStatus(t, rec, http.StatusOK)
	records := readCSV(t, rec.Body.String())
	// 变异:写导出单元格时绕过 SafeCSVCell;model 单元格以 '=' 开头,本断言失败。
	if got := records[1][1]; got != "'=cmd|' /C calc'!A0" {
		t.Fatalf("model cell=%q want formula guard prefix", got)
	}
}

func TestMeExportAuthRequired(t *testing.T) {
	store := &usageStoreStub{}
	h := NewHandler(Deps{Store: store, MaxRows: 10, FetchPageSize: 10})

	rec := invokeMeExport(h, "/v1/me/usage/export.csv?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z", nil)

	assertStatus(t, rec, http.StatusUnauthorized)
	if store.calls != 0 {
		t.Fatalf("store called %d times without session; want zero", store.calls)
	}
}

func TestMeExportDateRange(t *testing.T) {
	ident := sessionauth.SessionIdentity{TenantID: 7, UserID: 40}
	store := &usageStoreStub{}
	h := NewHandler(Deps{Store: store, MaxRows: 10, FetchPageSize: 10})

	tests := []string{
		"/v1/me/usage/export.csv?from=2026-06-03T00:00:00Z&to=2026-06-01T00:00:00Z",
		"/v1/me/usage/export.csv?from=2026-01-01T00:00:00Z&to=2027-01-03T00:00:00Z",
	}
	for _, target := range tests {
		rec := invokeMeExport(h, target, &ident)
		assertStatus(t, rec, http.StatusBadRequest)
	}
	if store.calls != 0 {
		t.Fatalf("store called %d times for invalid ranges; want zero", store.calls)
	}
}

func TestMeExportJSONFormatUsesSameScope(t *testing.T) {
	ident := sessionauth.SessionIdentity{TenantID: 7, UserID: 40}
	store := &usageStoreStub{rows: []dbbilling.ListUsageRecordsRow{
		meExportRow(1, ident.TenantID, 30, ident.UserID, "req-json", "claude-opus-4", "done"),
		meExportRow(2, ident.TenantID, 31, 41, "req-json-leak", "gpt-4o", "done"),
	}}
	h := NewHandler(Deps{Store: store, MaxRows: 10, FetchPageSize: 10})

	rec := invokeMeExport(h, "/v1/me/usage/export.csv?format=json&from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z", &ident)

	assertStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q want application/json", got)
	}
	var body struct {
		Items []struct {
			RequestID string `json:"request_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode json export: %v body=%s", err, rec.Body.String())
	}
	if len(body.Items) != 1 || body.Items[0].RequestID != "req-json" {
		t.Fatalf("json items=%+v want only req-json", body.Items)
	}
	if strings.Contains(rec.Body.String(), "req-json-leak") {
		t.Fatalf("json export leaked another user row: %s", rec.Body.String())
	}
}

func invokeMeExport(h http.HandlerFunc, target string, ident *sessionauth.SessionIdentity) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, strings.NewReader(""))
	if ident != nil {
		req = req.WithContext(sessionauth.ContextWithSession(req.Context(), *ident))
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func meExportRow(id, tenantID, apiKeyID, userID int64, requestID, model, status string) dbbilling.ListUsageRecordsRow {
	created := time.Date(2026, 6, 1, 12, 30, 0, 0, time.UTC).Add(time.Duration(id) * time.Second)
	return dbbilling.ListUsageRecordsRow{
		ID:             id,
		TenantID:       tenantID,
		APIKeyID:       apiKeyID,
		UserID:         userID,
		RequestID:      requestID,
		RequestedModel: model,
		TokensInput:    12,
		TokensOutput:   34,
		ActualCost:     decimal.RequireFromString("0.00012345"),
		CreatedAt:      pgtype.Timestamptz{Time: created, Valid: true},
		EndClass:       status,
	}
}

func readCSV(t *testing.T, body string) [][]string {
	t.Helper()
	records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v body=%s", err, body)
	}
	return records
}

func assertCSVRow(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("csv len=%d row=%v want len=%d row=%v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("csv col %d=%q want %q; row=%v", i, got[i], want[i], got)
		}
	}
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, want, rec.Body.String())
	}
}
