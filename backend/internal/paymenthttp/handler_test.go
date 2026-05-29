// HUAKAI · iKun

package paymenthttp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

func sampleOrder() payment.Order {
	ts := time.Date(2026, 5, 29, 1, 2, 3, 0, time.UTC)
	return payment.Order{
		ID: 1, TenantID: 5, UserID: 7, OutTradeNo: "t-1", AmountCents: 1000, CurrencyCode: "USD",
		Status: payment.StatusCompleted, ProviderKind: payment.ProviderManual,
		CreatedByAdminID: 99, ConfirmedByAdminID: 99, ConfirmReason: "manual ok",
		ProviderOrderRef: "ref-x", RequestFingerprint: "fp-secret-abc",
		CreatedAt: ts, UpdatedAt: ts,
	}
}

// 守数据泄露: 用户视图绝不暴露任何内部/管理字段, 且全 snake_case。
// mutation: handler 改回直返 payment.Order → 序列化出 PascalCase + 内部字段 + 指纹值 → 红。
func TestUserOrderViewHidesInternalFields(t *testing.T) {
	raw, err := json.Marshal(toOrderView(sampleOrder()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(raw)
	for _, leaked := range []string{
		"created_by_admin_id", "confirmed_by_admin_id", "confirm_reason",
		"provider_order_ref", "request_fingerprint",
		"RequestFingerprint", "CreatedByAdminID", "ConfirmReason", "OutTradeNo",
	} {
		if strings.Contains(js, leaked) {
			t.Fatalf("user order view leaked field %q: %s", leaked, js)
		}
	}
	// 内部指纹值本身绝不出现。
	if strings.Contains(js, "fp-secret-abc") {
		t.Fatalf("user order view leaked request fingerprint value: %s", js)
	}
	// 公开字段必须在且是 snake_case。
	if !strings.Contains(js, `"out_trade_no"`) || !strings.Contains(js, `"amount_cents"`) {
		t.Fatalf("user order view missing public snake_case fields: %s", js)
	}
}

func TestAdminOrderViewIncludesAdminFieldsSnakeCase(t *testing.T) {
	raw, err := json.Marshal(toAdminOrderView(sampleOrder()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(raw)
	if !strings.Contains(js, `"confirmed_by_admin_id"`) || !strings.Contains(js, `"confirm_reason"`) {
		t.Fatalf("admin order view missing admin fields: %s", js)
	}
	// admin 视图仍不暴露纯内部 request_fingerprint。
	if strings.Contains(js, "fp-secret-abc") || strings.Contains(js, "request_fingerprint") {
		t.Fatalf("admin order view exposed request fingerprint: %s", js)
	}
	// 不得出现 PascalCase 字段名。
	if strings.Contains(js, "OutTradeNo") || strings.Contains(js, "AmountCents") {
		t.Fatalf("admin order view leaked PascalCase field: %s", js)
	}
}
