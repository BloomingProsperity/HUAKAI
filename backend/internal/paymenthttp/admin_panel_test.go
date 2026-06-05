// HUAKAI · iKun

package paymenthttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

func TestAdminListOrdersRouteParsesFilters(t *testing.T) {
	from := "2026-06-01T00:00:00Z"
	to := "2026-06-03T00:00:00Z"
	svc := &captureService{adminListRes: []payment.Order{
		{ID: 2, TenantID: 5, UserID: 7, Status: payment.StatusPending, AmountCents: 200, CreatedAt: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)},
	}}
	router := newAdminTestRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/orders/?tenant_id=5&status=pending&created_from="+from+"&created_to="+to+"&limit=2&offset=1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if svc.gotAdminList.TenantID != 5 || svc.gotAdminList.Status != payment.StatusPending || svc.gotAdminList.Limit != 2 || svc.gotAdminList.Offset != 1 {
		t.Fatalf("admin list filter not wired: %+v", svc.gotAdminList)
	}
	if svc.gotAdminList.From == nil || svc.gotAdminList.To == nil {
		t.Fatalf("admin list time range missing: %+v", svc.gotAdminList)
	}
	var resp struct {
		Orders []struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		} `json:"orders"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Orders) != 1 || resp.Orders[0].ID != 2 || resp.Orders[0].Status != "pending" {
		t.Fatalf("orders response=%+v want only pending id=2", resp.Orders)
	}
}

func TestAdminAuditRouteReturnsOrderEvents(t *testing.T) {
	occurred := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	svc := &captureService{auditEvents: []payment.AuditEvent{
		{EventType: payment.AuditOrderCreated, ActorKind: payment.ActorKindAdmin, ActorID: 99, OccurredAt: occurred},
		{EventType: payment.AuditCredited, ActorKind: payment.ActorKindAdmin, ActorID: 99, OccurredAt: occurred.Add(time.Minute)},
	}}
	router := newAdminTestRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/orders/7/audit?tenant_id=5", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if svc.gotAuditTenantID != 5 || svc.gotAuditOrderID != 7 {
		t.Fatalf("audit input tenant/order=%d/%d want 5/7", svc.gotAuditTenantID, svc.gotAuditOrderID)
	}
	var resp struct {
		AuditEvents []struct {
			EventType string `json:"event_type"`
			ActorKind string `json:"actor_kind"`
			ActorID   int64  `json:"actor_id"`
		} `json:"audit_events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	if len(resp.AuditEvents) != 2 || resp.AuditEvents[0].EventType != payment.AuditOrderCreated || resp.AuditEvents[1].EventType != payment.AuditCredited {
		t.Fatalf("audit events=%+v want order_created then credited", resp.AuditEvents)
	}
}

func TestAdminDashboardRouteWiresTenantAndRange(t *testing.T) {
	from := "2026-06-01T00:00:00Z"
	to := "2026-06-03T00:00:00Z"
	svc := &captureService{dashboardRes: payment.DashboardStats{
		TotalAmountCents:   300,
		TotalCount:         2,
		TodayCount:         1,
		AverageAmountCents: 150,
		DailySeries: []payment.DailyStats{
			{Date: "2026-06-01", OrderCount: 1, AmountCents: 100},
			{Date: "2026-06-02", OrderCount: 1, AmountCents: 200},
		},
	}}
	router := newAdminTestRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/orders/dashboard?tenant_id=5&created_from="+from+"&created_to="+to, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if svc.gotDashboard.TenantID != 5 || svc.gotDashboard.From.IsZero() || svc.gotDashboard.To.IsZero() {
		t.Fatalf("dashboard filter not wired: %+v", svc.gotDashboard)
	}
	var resp struct {
		TotalAmountCents   int64 `json:"total_amount_cents"`
		TotalCount         int   `json:"total_count"`
		TodayCount         int   `json:"today_count"`
		AverageAmountCents int64 `json:"average_amount_cents"`
		DailySeries        []struct {
			Date        string `json:"date"`
			OrderCount  int    `json:"order_count"`
			AmountCents int64  `json:"amount_cents"`
		} `json:"daily_series"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if resp.TotalAmountCents != 300 || resp.TotalCount != 2 || resp.TodayCount != 1 || resp.AverageAmountCents != 150 || len(resp.DailySeries) != 2 {
		t.Fatalf("dashboard response=%+v want exact stats", resp)
	}
}

func TestAdminRetryRouteWiresServiceAndRendersIdempotentFlag(t *testing.T) {
	svc := &captureService{retryRes: payment.FulfillResult{
		Order:        payment.Order{ID: 7, Status: payment.StatusCompleted, OrderKind: payment.OrderKindTopup},
		Credit:       payment.CreditRecord{ID: 3, AmountCents: 700, CurrencyCode: "USD"},
		BalanceCents: 700,
		Idempotent:   true,
	}}
	router := newAdminTestRouter(svc)
	body, _ := json.Marshal(map[string]any{"tenant_id": 5})
	req := httptest.NewRequest(http.MethodPost, "/orders/7/retry", bytes.NewReader(body))
	req.Header.Set("X-Request-Id", "retry-http")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if svc.gotRetry.TenantID != 5 || svc.gotRetry.OrderID != 7 || svc.gotRetry.ActorAdminID != 99 || svc.gotRetry.RequestID != "retry-http" {
		t.Fatalf("retry input not wired: %+v", svc.gotRetry)
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if string(resp["idempotent"]) != "true" || string(resp["balance_cents"]) != "700" {
		t.Fatalf("retry response=%s want idempotent true and balance 700", rec.Body.String())
	}
}

func TestAdminProviderConfigGetPutWiresRuntimeConfig(t *testing.T) {
	svc := &captureService{
		providerConfigRes: payment.ProviderRuntimeConfig{
			ProviderKind: payment.ProviderTaobao,
			Enabled:      false,
			CheckoutURL:  "",
			Source:       "runtime",
		},
		setProviderConfigRes: payment.ProviderRuntimeConfig{
			ProviderKind: payment.ProviderTaobao,
			Enabled:      true,
			CheckoutURL:  "https://pay.example/taobao",
			Source:       "runtime",
		},
	}
	router := newAdminTestRouter(svc)

	getReq := httptest.NewRequest(http.MethodGet, "/orders/providers/taobao/config", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status=%d want 200; body=%s", getRec.Code, getRec.Body.String())
	}
	if svc.gotProviderConfigKind != payment.ProviderTaobao {
		t.Fatalf("GET provider kind=%q want taobao", svc.gotProviderConfigKind)
	}

	body, _ := json.Marshal(map[string]any{"enabled": true, "checkout_url": "https://pay.example/taobao"})
	putReq := httptest.NewRequest(http.MethodPut, "/orders/providers/taobao/config", bytes.NewReader(body))
	putRec := httptest.NewRecorder()
	router.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT status=%d want 200; body=%s", putRec.Code, putRec.Body.String())
	}
	if svc.gotSetProviderConfig.ProviderKind != payment.ProviderTaobao || !svc.gotSetProviderConfig.Enabled ||
		svc.gotSetProviderConfig.CheckoutURL != "https://pay.example/taobao" || svc.gotSetProviderConfig.UpdatedBy != "99" {
		t.Fatalf("PUT config not wired: %+v", svc.gotSetProviderConfig)
	}
}
