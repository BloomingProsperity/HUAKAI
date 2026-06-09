package exporthttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

// stubs

type ordersExportStub struct {
	rows  []payment.Order
	got   payment.OrderExportFilter
	calls int
}

func (s *ordersExportStub) ExportOrders(_ context.Context, f payment.OrderExportFilter) ([]payment.Order, error) {
	s.calls++
	s.got = f
	return s.rows, nil
}

type refundsExportStub struct {
	rows  []payment.RefundRecord
	got   payment.RefundExportFilter
	calls int
}

func (s *refundsExportStub) ExportRefunds(_ context.Context, f payment.RefundExportFilter) ([]payment.RefundRecord, error) {
	s.calls++
	s.got = f
	return s.rows, nil
}

func newOrdersRefundsRouter(orders *ordersExportStub, refunds *refundsExportStub, ident admin.AdminIdentity, maxRows int) http.Handler {
	r := chi.NewRouter()
	MountRoutes(r, Deps{
		Auth:    exportAuthStub{ident: ident},
		Orders:  orders,
		Refunds: refunds,
		MaxRows: maxRows,
	})
	return r
}

// TestOrdersExportCSV seeds 2 orders; verifies header + 2 data rows + CSV-injection guard.
//
// MUTATION: if safeCSVRecord / SafeCSVCell is bypassed for out_trade_no,
// the injection cell starts with "=" instead of "'=" -> RED.
func TestOrdersExportCSV(t *testing.T) {
	created := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	orders := &ordersExportStub{rows: []payment.Order{
		{
			ID: 10, TenantID: 7, UserID: 70,
			Status: payment.StatusCompleted, ProviderKind: payment.ProviderManual,
			OrderKind: payment.OrderKindTopup, AmountCents: 5000, CurrencyCode: "USD",
			OutTradeNo: "trade-normal", CreatedAt: created,
		},
		{
			ID: 11, TenantID: 7, UserID: 71,
			Status: payment.StatusCompleted, ProviderKind: payment.ProviderManual,
			OrderKind: payment.OrderKindTopup, AmountCents: 1000, CurrencyCode: "USD",
			// injection payload starting with "=" must be prefix-escaped by SafeCSVCell
			OutTradeNo: "=cmd|' /C calc'!A0",
			CreatedAt:  created.Add(time.Minute),
		},
	}}
	router := newOrdersRefundsRouter(orders, &refundsExportStub{}, scopedAdmin(7), 100)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/orders/export.csv?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	records := readCSV(t, rec.Body.String())

	// Header row must be present and correct.
	if len(records) < 1 {
		t.Fatal("no records returned")
	}
	assertCSVRow(t, records[0], ordersCSVHeader)

	// Must have header + 2 data rows.
	if len(records) != 3 {
		t.Fatalf("records=%d want header+2 data rows; got %v", len(records), records)
	}

	// CSV-injection guard: cell starting with "=" must be prefixed with single-quote.
	// out_trade_no is column index 7 in ordersCSVHeader.
	injectionCell := records[2][7]
	if strings.HasPrefix(injectionCell, "=") {
		t.Errorf("CSV injection not escaped: cell=%q; MUTATION: removing SafeCSVCell makes this fail", injectionCell)
	}
	wantPrefix := "'="
	if !strings.HasPrefix(injectionCell, wantPrefix) {
		t.Errorf("injection cell=%q must start with %q", injectionCell, wantPrefix)
	}
}

// TestRefundsExportRange verifies that a refund outside the date window is excluded.
//
// MUTATION: if parseExportRange is ignored or From/To not forwarded to the store,
// the out-of-window refund is included and len(records)==3 -> RED.
func TestRefundsExportRange(t *testing.T) {
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	inWindow := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	outWindow := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)

	filteredStub := &filteringRefundsStub{
		all: []payment.RefundRecord{
			{ID: 1, TenantID: 7, OrderID: 100, UserID: 70, AmountCents: 500, CurrencyCode: "USD", CreatedAt: inWindow},
			{ID: 2, TenantID: 7, OrderID: 101, UserID: 71, AmountCents: 200, CurrencyCode: "USD", CreatedAt: outWindow},
		},
	}
	router := newFilteringRouter(filteredStub, scopedAdmin(7), 100)

	fromS := from.Format(time.RFC3339)
	toS := to.Format(time.RFC3339)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/refunds/export.csv?from="+fromS+"&to="+toS, nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	records := readCSV(t, rec.Body.String())

	// Must have header + 1 in-window row only.
	// MUTATION: if range ignored, both rows appear -> len==3 -> RED.
	if len(records) != 2 {
		t.Fatalf("records=%d want header+1 in-window row; MUTATION: ignoring range includes out-of-window row", len(records))
	}
	if records[1][0] != "1" {
		t.Errorf("first data row id=%q want 1", records[1][0])
	}
}

// filteringRefundsStub honours date-range filters to simulate real store behaviour.
type filteringRefundsStub struct {
	all []payment.RefundRecord
}

func (s *filteringRefundsStub) ExportRefunds(_ context.Context, f payment.RefundExportFilter) ([]payment.RefundRecord, error) {
	var out []payment.RefundRecord
	for _, r := range s.all {
		if r.TenantID != f.TenantID {
			continue
		}
		if f.From != nil && r.CreatedAt.Before(*f.From) {
			continue
		}
		if f.To != nil && !r.CreatedAt.Before(*f.To) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func newFilteringRouter(refunds *filteringRefundsStub, ident admin.AdminIdentity, maxRows int) http.Handler {
	r := chi.NewRouter()
	MountRoutes(r, Deps{
		Auth:    exportAuthStub{ident: ident},
		Refunds: refunds,
		MaxRows: maxRows,
	})
	return r
}
