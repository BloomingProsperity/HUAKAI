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

// 桩对象

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

// TestOrdersExportCSV 种入 2 条订单;校验表头 + 2 行数据 + CSV 注入防护。
//
// 变异:若 out_trade_no 绕过了 safeCSVRecord / SafeCSVCell,
// 注入单元格会以 "=" 而非 "'=" 开头 -> 变红。
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
			// 以 "=" 开头的注入载荷必须被 SafeCSVCell 前缀转义
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

	// 表头行必须存在且正确。
	if len(records) < 1 {
		t.Fatal("no records returned")
	}
	assertCSVRow(t, records[0], ordersCSVHeader)

	// 必须有表头 + 2 行数据。
	if len(records) != 3 {
		t.Fatalf("records=%d want header+2 data rows; got %v", len(records), records)
	}

	// CSV 注入防护:以 "=" 开头的单元格必须加上单引号前缀。
	// out_trade_no 在 ordersCSVHeader 中是第 7 列(索引)。
	injectionCell := records[2][7]
	if strings.HasPrefix(injectionCell, "=") {
		t.Errorf("CSV injection not escaped: cell=%q; MUTATION: removing SafeCSVCell makes this fail", injectionCell)
	}
	wantPrefix := "'="
	if !strings.HasPrefix(injectionCell, wantPrefix) {
		t.Errorf("injection cell=%q must start with %q", injectionCell, wantPrefix)
	}
}

// TestRefundsExportRange 校验落在日期窗口外的退款会被排除。
//
// 变异:若 parseExportRange 被忽略,或 From/To 未传给 store,
// 窗口外的退款会被纳入,len(records)==3 -> 变红。
func TestRefundsExportRange(t *testing.T) {
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	inWindow := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	outWindow := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)

	filteredStub := &filteringRefundsStub{
		all: []payment.RefundRecord{
			{ID: 1, TenantID: 7, OrderID: 100, UserID: 70, AmountCents: 500, RequestedAmountCents: 1000, RequireExact: true, CurrencyCode: "USD", BillingEventID: 9001, CreatedAt: inWindow},
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

	// 必须只有表头 + 1 行窗口内数据。
	// 变异:若范围被忽略,两行都会出现 -> len==3 -> 变红。
	if len(records) != 2 {
		t.Fatalf("records=%d want header+1 in-window row; MUTATION: ignoring range includes out-of-window row", len(records))
	}
	if records[1][0] != "1" {
		t.Errorf("first data row id=%q want 1", records[1][0])
	}
	if records[1][8] != "10.00" || records[1][9] != "true" || records[1][10] != "9001" {
		t.Fatalf("refund operation evidence columns=%v want requested amount/exact mode/billing event", records[1])
	}
}

// filteringRefundsStub 遵守日期范围过滤,以模拟真实 store 的行为。
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
