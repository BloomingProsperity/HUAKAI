package billingreconhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

type authStub struct {
	ident admin.AdminIdentity
	err   error
}

func (a authStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if a.err != nil {
		return admin.AdminIdentity{}, a.err
	}
	return a.ident, nil
}

type repriceServiceStub struct {
	called int
	req    billing.RepriceRequest
	result billing.RepriceResult
	err    error
}

func (s *repriceServiceStub) RepriceUsageRecords(_ context.Context, req billing.RepriceRequest) (billing.RepriceResult, error) {
	s.called++
	s.req = req
	if s.err != nil {
		return billing.RepriceResult{}, s.err
	}
	return s.result, nil
}

func TestDryRunDefaultsTrueAndReturnsMoneyStrings(t *testing.T) {
	svc := &repriceServiceStub{result: billing.RepriceResult{
		DryRun: true,
		Items: []billing.RepriceItem{{
			UsageRecordID:     11,
			TenantID:          7,
			Status:            billing.RepriceStatusWouldApply,
			OriginalCost:      decimal.RequireFromString("0.01000000"),
			AuthoritativeCost: decimal.RequireFromString("0.01250000"),
			CostDelta:         decimal.RequireFromString("0.00250000"),
			PricingSource:     "billing_policy_version=1.0",
		}},
		Summary: billing.RepriceSummary{Total: 1, WouldApply: 1},
	}}
	h := NewHandler(Deps{Auth: authStub{ident: admintest.Platform(0)}, Service: svc})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/v1/billing/reprice", strings.NewReader(`{"usage_record_id":11}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if svc.called != 1 || !svc.req.DryRun || svc.req.UsageRecordID != 11 {
		t.Fatalf("service called=%d req=%+v, want dry_run default true usage_record_id=11", svc.called, svc.req)
	}
	var body repriceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.DryRun || len(body.Items) != 1 || body.Items[0].CostDelta != "0.00250000" {
		t.Fatalf("body=%+v, want dry_run item with fixed money strings", body)
	}
}

func TestApplyRequiresExplicitDryRunFalse(t *testing.T) {
	svc := &repriceServiceStub{result: billing.RepriceResult{
		DryRun:  false,
		Items:   []billing.RepriceItem{{UsageRecordID: 11, TenantID: 7, Status: billing.RepriceStatusRepriced}},
		Summary: billing.RepriceSummary{Total: 1, Repriced: 1},
	}}
	h := NewHandler(Deps{Auth: authStub{ident: admintest.Platform(0)}, Service: svc})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/v1/billing/reprice", strings.NewReader(`{"usage_record_id":11,"dry_run":false}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if svc.called != 1 || svc.req.DryRun {
		t.Fatalf("req=%+v, want explicit apply dry_run=false", svc.req)
	}
	var body repriceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Summary.Repriced != 1 || body.Summary.AlreadyRepriced != 0 {
		t.Fatalf("summary=%+v want repriced=1/already_repriced=0", body.Summary)
	}
}

func TestBatchScopeParsesTenantWindowAndLimit(t *testing.T) {
	svc := &repriceServiceStub{result: billing.RepriceResult{DryRun: true}}
	h := NewHandler(Deps{Auth: authStub{ident: admintest.Platform(0)}, Service: svc})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/v1/billing/reprice", strings.NewReader(`{
		"tenant_id":7,
		"from":"2026-07-05T00:00:00Z",
		"to":"2026-07-06T00:00:00Z",
		"limit":42
	}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if svc.req.TenantID != 7 || svc.req.Limit != 42 || svc.req.From.IsZero() || svc.req.To.IsZero() || !svc.req.DryRun {
		t.Fatalf("req=%+v, want parsed tenant window dry-run", svc.req)
	}
}

func TestNonPlatformAdminRejectedBeforeService(t *testing.T) {
	svc := &repriceServiceStub{}
	h := NewHandler(Deps{Auth: authStub{ident: admintest.TenantOperator(0, 7)}, Service: svc})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/v1/billing/reprice", strings.NewReader(`{"usage_record_id":11}`)))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s, want 403", rec.Code, rec.Body.String())
	}
	if svc.called != 0 {
		t.Fatalf("非 platform_admin 不应调用 service, called=%d", svc.called)
	}
}

func TestAmbiguousScopeRejected(t *testing.T) {
	svc := &repriceServiceStub{}
	h := NewHandler(Deps{Auth: authStub{ident: admintest.Platform(0)}, Service: svc})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/v1/billing/reprice", strings.NewReader(`{"usage_record_id":11,"tenant_id":7}`)))

	if rec.Code != http.StatusBadRequest || svc.called != 0 {
		t.Fatalf("status=%d called=%d body=%s, want 400 before service", rec.Code, svc.called, rec.Body.String())
	}
}
