package exporthttp

import (
	"context"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

type exportAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s exportAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	return s.ident, nil
}

type paymentExportStub struct {
	rows  []payment.Order
	got   payment.OrderExportFilter
	calls int
}

func (s *paymentExportStub) ExportOrders(_ context.Context, filter payment.OrderExportFilter) ([]payment.Order, error) {
	s.calls++
	s.got = filter
	return s.rows, nil
}

type usageExportStub struct {
	rows  []dbbilling.ListUsageRecordsRow
	got   dbbilling.ListUsageRecordsParams
	calls int
}

func (s *usageExportStub) ListUsageRecords(_ context.Context, arg dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error) {
	s.calls++
	s.got = arg
	return s.rows, nil
}

func TestPaymentsExportCSVShape(t *testing.T) {
	created := time.Date(2026, 6, 1, 12, 30, 0, 0, time.UTC)
	payments := &paymentExportStub{rows: []payment.Order{
		{ID: 101, TenantID: 7, UserID: 70, ProviderKind: payment.ProviderManual, Status: payment.StatusCompleted, AmountCents: 1234, CurrencyCode: "USD", CreatedAt: created, OutTradeNo: "trade-101", OrderKind: payment.OrderKindTopup},
		{ID: 102, TenantID: 7, UserID: 71, ProviderKind: payment.ProviderTaobao, Status: payment.StatusPending, AmountCents: 5000, CurrencyCode: "USD", CreatedAt: created.Add(time.Hour), OutTradeNo: "trade-102", OrderKind: payment.OrderKindSubscription},
	}}
	router := newExportTestRouter(payments, &usageExportStub{}, scopedAdmin(7), 10)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/payments/export.csv?from=2026-06-01T00:00:00Z&to=2026-06-03T00:00:00Z&status=completed", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Fatalf("Content-Type=%q want text/csv; charset=utf-8", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="payments-export.csv"` {
		t.Fatalf("Content-Disposition=%q want attachment filename", got)
	}
	if payments.got.TenantID != 7 || payments.got.Status != payment.StatusCompleted || payments.got.Limit != 11 {
		t.Fatalf("payment filter=%+v want tenant 7 status completed limit max+1", payments.got)
	}
	records := readCSV(t, rec.Body.String())
	// MUTATION: omit the header row write in the payments handler; this assertion fails because records[0] becomes the first data row.
	wantHeader := []string{"order_id", "user_id", "provider", "status", "amount", "currency", "created_at", "out_trade_no", "order_kind"}
	assertCSVRow(t, records[0], wantHeader)
	if len(records) != 3 {
		t.Fatalf("records=%v want header + 2 data rows", records)
	}
	assertCSVRow(t, records[1], []string{"101", "70", "manual", "completed", "12.34", "USD", "2026-06-01T12:30:00Z", "trade-101", "topup"})
}

func TestExportTenantScoped(t *testing.T) {
	payments := &paymentExportStub{rows: []payment.Order{
		{ID: 201, TenantID: 7, UserID: 70, ProviderKind: payment.ProviderManual, Status: payment.StatusCompleted, AmountCents: 100, CurrencyCode: "USD", CreatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), OutTradeNo: "tenant-a"},
		{ID: 202, TenantID: 8, UserID: 80, ProviderKind: payment.ProviderManual, Status: payment.StatusCompleted, AmountCents: 200, CurrencyCode: "USD", CreatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), OutTradeNo: "tenant-b-leak"},
	}}
	router := newExportTestRouter(payments, &usageExportStub{}, scopedAdmin(7), 10)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/payments/export.csv?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if payments.got.TenantID != 7 {
		t.Fatalf("tenant filter=%d want 7", payments.got.TenantID)
	}
	records := readCSV(t, rec.Body.String())
	// MUTATION: drop the tenant filter / tenant-row guard; tenant-b-leak appears in the CSV and this test turns red.
	if len(records) != 2 {
		t.Fatalf("records=%v want header + tenant A row only", records)
	}
	if strings.Contains(rec.Body.String(), "tenant-b-leak") {
		t.Fatalf("cross-tenant order leaked in CSV: %s", rec.Body.String())
	}
}

func TestExportCSVInjectionGuard(t *testing.T) {
	payments := &paymentExportStub{rows: []payment.Order{
		{ID: 301, TenantID: 7, UserID: 70, ProviderKind: payment.ProviderManual, Status: payment.StatusCompleted, AmountCents: 100, CurrencyCode: "USD", CreatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), OutTradeNo: "=cmd|' /C calc'!A0"},
	}}
	router := newExportTestRouter(payments, &usageExportStub{}, scopedAdmin(7), 10)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/payments/export.csv?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	records := readCSV(t, rec.Body.String())
	// MUTATION: remove SafeCSVCell from string cells; the out_trade_no cell starts with '=' and this assertion fails.
	if got := records[1][7]; got != "'=cmd|' /C calc'!A0" {
		t.Fatalf("out_trade_no cell=%q want quote-prefixed formula guard", got)
	}
}

func TestExportDateRangeValidation(t *testing.T) {
	payments := &paymentExportStub{}
	router := newExportTestRouter(payments, &usageExportStub{}, scopedAdmin(7), 10)

	tests := []string{
		"/v1/admin/payments/export.csv?from=2026-06-03T00:00:00Z&to=2026-06-01T00:00:00Z",
		"/v1/admin/payments/export.csv?from=2026-01-01T00:00:00Z&to=2027-01-03T00:00:00Z",
	}
	for _, target := range tests {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s want 400", target, rec.Code, rec.Body.String())
		}
	}
	if payments.calls != 0 {
		t.Fatalf("payment exporter called %d times for invalid ranges; want zero", payments.calls)
	}
}

func TestUsageExportCSVShape(t *testing.T) {
	created := time.Date(2026, 6, 1, 12, 30, 0, 0, time.UTC)
	usage := &usageExportStub{rows: []dbbilling.ListUsageRecordsRow{{
		TenantID: 7, UserID: 70, RequestID: "req-usage-1", RequestedModel: "gpt-4o",
		TokensInput: 12, TokensOutput: 34, ActualCost: decimal.RequireFromString("0.00012345"),
		CreatedAt: pgtype.Timestamptz{Time: created, Valid: true},
	}}}
	router := newExportTestRouter(&paymentExportStub{}, usage, scopedAdmin(7), 10)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/usage/export.csv?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if usage.got.TenantID == nil || *usage.got.TenantID != 7 || usage.got.PageLimit != 11 {
		t.Fatalf("usage filter=%+v want tenant 7 and max+1 limit", usage.got)
	}
	records := readCSV(t, rec.Body.String())
	// MUTATION: omit the usage header row; this assertion fails because records[0] becomes req-usage-1.
	assertCSVRow(t, records[0], []string{"request_id", "user_id", "model", "tokens_input", "tokens_output", "cost_usd", "created_at"})
	if len(records) != 2 {
		t.Fatalf("records=%v want header + 1 data row", records)
	}
	assertCSVRow(t, records[1], []string{"req-usage-1", "70", "gpt-4o", "12", "34", "0.00012345", "2026-06-01T12:30:00Z"})
}

func TestExportAuthRequiredAdmin(t *testing.T) {
	router := newExportTestRouter(&paymentExportStub{}, &usageExportStub{}, admin.AdminIdentity{TokenID: 9, Role: "viewer", ScopeTenantID: 7}, 10)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/payments/export.csv?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want 403 for resolved non-admin role", rec.Code, rec.Body.String())
	}
}

func TestExportTruncationIsExplicit(t *testing.T) {
	payments := &paymentExportStub{rows: []payment.Order{
		{ID: 401, TenantID: 7, UserID: 70, ProviderKind: payment.ProviderManual, Status: payment.StatusCompleted, AmountCents: 100, CurrencyCode: "USD", CreatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), OutTradeNo: "kept"},
		{ID: 402, TenantID: 7, UserID: 70, ProviderKind: payment.ProviderManual, Status: payment.StatusCompleted, AmountCents: 200, CurrencyCode: "USD", CreatedAt: time.Date(2026, 6, 1, 0, 1, 0, 0, time.UTC), OutTradeNo: "truncated"},
	}}
	router := newExportTestRouter(payments, &usageExportStub{}, scopedAdmin(7), 1)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/payments/export.csv?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Truncated"); got != "true" {
		t.Fatalf("X-Truncated=%q want true", got)
	}
	records := readCSV(t, rec.Body.String())
	if len(records) != 3 || records[2][0] != "# truncated" {
		t.Fatalf("records=%v want explicit trailing truncation notice", records)
	}
}

func newExportTestRouter(payments *paymentExportStub, usage *usageExportStub, ident admin.AdminIdentity, maxRows int) http.Handler {
	r := chi.NewRouter()
	MountRoutes(r, Deps{
		Auth:     exportAuthStub{ident: ident},
		Payments: payments,
		Usage:    usage,
		MaxRows:  maxRows,
	})
	return r
}

func scopedAdmin(tenantID int64) admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: 1, Role: admin.RoleTenantOperator, ScopeTenantID: tenantID}
}

func readCSV(t *testing.T, body string) [][]string {
	t.Helper()
	records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("read csv body=%q err=%v", body, err)
	}
	return records
}

func assertCSVRow(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("row=%v len=%d want %v len=%d", got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row[%d]=%q row=%v want %v", i, got[i], got, want)
		}
	}
}
