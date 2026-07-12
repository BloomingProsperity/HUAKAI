package trustreceipt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sampleReceipt() TrustReceiptV1 {
	return TrustReceiptV1{
		RequestID:       "req_trust_b",
		ReceiptSequence: 7,
		TenantScopeRef:  "tenant:demo",
		OccurredAt:      time.Date(2026, 5, 27, 15, 4, 5, 123456789, time.FixedZone("fixture", 8*60*60)),
		Provider:        "anthropic",
		RequestedModel:  "claude-opus-4",
		RoutedModel:     "claude-opus-4-20260514",
		UpstreamModel:   "claude-opus-4",
		DeliveredModel:  "claude-opus-4",
		CostCents:       12345,
		TokenCounts: TokenCounts{
			Input:  1000,
			Output: 2000,
			Cached: 300,
		},
		PriceSnapshot: PriceSnapshot{
			RateTableSnapshotID: 42,
			SnapshotVersion:     "price-v4",
			CurrencyCode:        "USD",
		},
		ValidationState: "verified",
		RedactedMetadataAllowlist: map[string]any{
			"aaa": int64(1),
			"bbb": true,
			"zzz": "last",
		},
	}
}

// TestCanonicalPayloadFieldOrderStableForDeterministicHash
//
// 守 fixed-order canonical：同一语义 payload 不得受 struct literal 写法影响。
// 变异自检：改顶层 buf 写入顺序或漏写 receipt_id，本测试会变红。
func TestCanonicalPayloadFieldOrderStableForDeterministicHash(t *testing.T) {
	a := sampleReceipt()
	b := TrustReceiptV1{
		RedactedMetadataAllowlist: map[string]any{"zzz": "last", "bbb": true, "aaa": int64(1)},
		ValidationState:           "verified",
		PriceSnapshot:             PriceSnapshot{CurrencyCode: "USD", SnapshotVersion: "price-v4", RateTableSnapshotID: 42},
		TokenCounts:               TokenCounts{Cached: 300, Output: 2000, Input: 1000},
		CostCents:                 12345,
		DeliveredModel:            "claude-opus-4",
		UpstreamModel:             "claude-opus-4",
		RoutedModel:               "claude-opus-4-20260514",
		RequestedModel:            "claude-opus-4",
		Provider:                  "anthropic",
		OccurredAt:                time.Date(2026, 5, 27, 7, 4, 5, 123456789, time.UTC),
		TenantScopeRef:            "tenant:demo",
		ReceiptSequence:           7,
		RequestID:                 "req_trust_b",
	}

	gotA, err := Canonical(a)
	if err != nil {
		t.Fatalf("Canonical a: %v", err)
	}
	gotB, err := Canonical(b)
	if err != nil {
		t.Fatalf("Canonical b: %v", err)
	}
	if !bytes.Equal(gotA, gotB) {
		t.Fatalf("canonical bytes differ:\na=%s\nb=%s", gotA, gotB)
	}
	want := `{"schema_version":"trust.receipt.v1","receipt_id":"req_trust_b:7","request_id":"req_trust_b","receipt_sequence":7,"tenant_scope_ref":"tenant:demo","occurred_at":"2026-05-27T07:04:05.123456789Z","provider":"anthropic","requested_model":"claude-opus-4","routed_model":"claude-opus-4-20260514","upstream_model":"claude-opus-4","delivered_model":"claude-opus-4","cost_cents":12345,"token_counts":{"input":1000,"output":2000,"cached":300},"price_snapshot":{"rate_table_snapshot_id":42,"snapshot_version":"price-v4","currency_code":"USD"},"validation_state":"verified","redacted_metadata_allowlist":{"aaa":1,"bbb":true,"zzz":"last"}}`
	if string(gotA) != want {
		t.Fatalf("canonical payload mismatch:\n got: %s\nwant: %s", gotA, want)
	}
}

// TestCanonicalRejectsFloatInPriceSnapshot
//
// 守金额与快照 ID 不允许 float drift：公开类型必须是 int64。
// 变异自检：把 CostCents 或 RateTableSnapshotID 偷换成 float64 会让本测试变红。
func TestCanonicalRejectsFloatInPriceSnapshot(t *testing.T) {
	receiptType := reflect.TypeOf(TrustReceiptV1{})
	costField, ok := receiptType.FieldByName("CostCents")
	if !ok {
		t.Fatal("TrustReceiptV1 missing CostCents")
	}
	if costField.Type.Kind() != reflect.Int64 {
		t.Fatalf("CostCents kind=%s want int64", costField.Type.Kind())
	}

	priceType := reflect.TypeOf(PriceSnapshot{})
	snapshotIDField, ok := priceType.FieldByName("RateTableSnapshotID")
	if !ok {
		t.Fatal("PriceSnapshot missing RateTableSnapshotID")
	}
	if snapshotIDField.Type.Kind() != reflect.Int64 {
		t.Fatalf("RateTableSnapshotID kind=%s want int64", snapshotIDField.Type.Kind())
	}
}

// TestCanonicalMapKeySortIsUTF8ByteLex
//
// 守 allowlist map key 排序：必须按 UTF-8 byte lex 升序，不能沿用 map 迭代顺序。
// 变异自检：删除 sort 或改成逆序，本测试会变红。
func TestCanonicalMapKeySortIsUTF8ByteLex(t *testing.T) {
	r := sampleReceipt()
	r.RedactedMetadataAllowlist = map[string]any{
		"zzz": "v",
		"aaa": "v",
		"bbb": "v",
	}

	got, err := Canonical(r)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	payload := string(got)
	allowlistStart := strings.Index(payload, `"redacted_metadata_allowlist":`)
	if allowlistStart < 0 {
		t.Fatalf("missing redacted_metadata_allowlist: %s", payload)
	}
	allowlist := payload[allowlistStart:]
	a := strings.Index(allowlist, `"aaa"`)
	b := strings.Index(allowlist, `"bbb"`)
	z := strings.Index(allowlist, `"zzz"`)
	if !(a >= 0 && b > a && z > b) {
		t.Fatalf("allowlist keys not sorted a/b/z: %s", allowlist)
	}
}

// TestCanonicalHashChangesOnAnyFieldFlip
//
// 守所有关键字段进入 canonical：provider/cost/token/validation 任一变动 hash 必变。
// 变异自检：漏写任一字段，对应 subtest 会变红。
func TestCanonicalHashChangesOnAnyFieldFlip(t *testing.T) {
	base := sampleReceipt()
	baseHash, err := CanonicalHash(base)
	if err != nil {
		t.Fatalf("CanonicalHash base: %v", err)
	}
	tests := []struct {
		name string
		mut  func(*TrustReceiptV1)
	}{
		{name: "provider", mut: func(r *TrustReceiptV1) { r.Provider = "openai" }},
		{name: "cost_cents", mut: func(r *TrustReceiptV1) { r.CostCents++ }},
		{name: "token_counts_input", mut: func(r *TrustReceiptV1) { r.TokenCounts.Input++ }},
		{name: "validation_state", mut: func(r *TrustReceiptV1) { r.ValidationState = "provisional" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := base
			changed.RedactedMetadataAllowlist = cloneMap(base.RedactedMetadataAllowlist)
			tt.mut(&changed)
			got, err := CanonicalHash(changed)
			if err != nil {
				t.Fatalf("CanonicalHash changed: %v", err)
			}
			if got == baseHash {
				t.Fatalf("hash unchanged after %s flip: %s", tt.name, hex.EncodeToString(got[:]))
			}
		})
	}
}

// TestCanonicalRoundTripJSONUnescape
//
// 守字符串 escape 策略：非 ASCII/control 必以 \u 形式进入 canonical，hash 固定。
// 变异自检：改成 raw UTF-8 或不同 escape 策略,本测试会变红。
func TestCanonicalRoundTripJSONUnescape(t *testing.T) {
	r := sampleReceipt()
	r.Provider = "火山"
	r.DeliveredModel = "模型😀\nnext"
	r.RedactedMetadataAllowlist = map[string]any{
		"note": "中文😀\u0001",
	}

	got, err := Canonical(r)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	payload := string(got)
	for _, forbidden := range []string{"火山", "模型", "中文", "😀"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("canonical payload contains raw non-ASCII %q: %s", forbidden, payload)
		}
	}
	for _, want := range []string{`\u706b\u5c71`, `\u6a21\u578b\ud83d\ude00\nnext`, `\u4e2d\u6587\ud83d\ude00\u0001`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("canonical payload missing escaped fragment %s: %s", want, payload)
		}
	}
	gotHash := sha256.Sum256(got)
	wantHash, err := CanonicalHash(r)
	if err != nil {
		t.Fatalf("CanonicalHash: %v", err)
	}
	if gotHash != wantHash {
		t.Fatalf("CanonicalHash mismatch: got %x want %x", wantHash, gotHash)
	}
	if hex.EncodeToString(gotHash[:]) != "32fefb5211d6364f0ee8efa72d826d5059cba180bb9243b3a900853e00e74219" {
		t.Fatalf("canonical escaped hash=%x want fixed escape-strategy hash", gotHash)
	}
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
