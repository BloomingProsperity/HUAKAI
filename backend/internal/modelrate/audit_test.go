package modelrate

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
)

func TestAuditChainDetectsTamperAndDelete(t *testing.T) {
	ctx := context.Background()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	at := time.Date(2026, 9, 11, 4, 0, 0, 0, time.UTC)
	input := decimal.RequireFromString("2.5")

	first, err := signAuditEntry(signer, auditEvent{
		OccurredAt: at,
		ActorID:    "admin_token:9",
		ActorRole:  "platform_admin",
		Vendor:     "openai",
		Model:      "gpt-5.6",
		Action:     ActionUpsert,
		NewPayload: bucketsJSON(RateBuckets{Input: &input}),
	}, nil)
	if err != nil {
		t.Fatalf("sign first: %v", err)
	}
	if first.Vendor != "openai" || first.Model != "gpt-5.6" {
		t.Fatalf("identity=%s/%s", first.Vendor, first.Model)
	}
	if err := sign.Verify(signer.PublicKey(), first.EntryHash, first.Signature); err != nil {
		t.Fatalf("first signature: %v", err)
	}

	output := decimal.RequireFromString("8")
	second, err := signAuditEntry(signer, auditEvent{
		OccurredAt: at.Add(time.Second),
		ActorID:    "admin_token:9",
		ActorRole:  "platform_admin",
		Vendor:     "openai",
		Model:      "gpt-5.6",
		Action:     ActionUpsert,
		OldPayload: first.NewPayload,
		NewPayload: bucketsJSON(RateBuckets{Input: &input, Output: &output}),
	}, first.EntryHash)
	if err != nil {
		t.Fatalf("sign second: %v", err)
	}
	if !bytes.Equal(second.PrevHash, first.EntryHash) {
		t.Fatalf("second prev_hash mismatch")
	}

	third, err := signAuditEntry(signer, auditEvent{
		OccurredAt: at.Add(2 * time.Second),
		ActorID:    "admin_token:9",
		ActorRole:  "platform_admin",
		Vendor:     "openai",
		Model:      "gpt-5.6",
		Action:     ActionDelete,
		OldPayload: second.NewPayload,
	}, second.EntryHash)
	if err != nil {
		t.Fatalf("sign delete: %v", err)
	}

	clean := VerifyAuditEntries(ctx, signer.PublicKey(), []AuditEntry{first, second, third})
	if !clean.OK {
		t.Fatalf("clean chain=%+v", clean)
	}

	tampered := second
	tampered.EntryHash = bytes.Repeat([]byte{0}, 32)
	tampered.ID = 2
	broken := VerifyAuditEntries(ctx, signer.PublicKey(), []AuditEntry{first, tampered, third})
	if broken.OK || broken.RowID != 2 || broken.Reason == "" {
		t.Fatalf("tampered chain=%+v want fail on row 2", broken)
	}
}

func TestVerifyAcceptsReencodedJSONPayload(t *testing.T) {
	ctx := context.Background()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	input := decimal.RequireFromString("2.5")
	output := decimal.RequireFromString("8")
	entry, err := signAuditEntry(signer, auditEvent{
		OccurredAt: time.Date(2026, 9, 11, 4, 0, 0, 123456789, time.UTC),
		ActorID:    "admin_token:9",
		ActorRole:  "platform_admin",
		Vendor:     "openai",
		Model:      "gpt-5.6",
		Action:     ActionUpsert,
		NewPayload: bucketsJSON(RateBuckets{Input: &input, Output: &output}),
	}, nil)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	reencoded := json.RawMessage(`{
		"output_micro_usd": "8",
		"input_micro_usd": "2.5"
	}`)
	loaded := entry
	loaded.ID = 1
	loaded.NewPayload = reencoded
	loaded.OccurredAt = loaded.OccurredAt.In(time.FixedZone("UTC+0", 0)).Add(300 * time.Nanosecond)
	got := VerifyAuditEntries(ctx, signer.PublicKey(), []AuditEntry{loaded})
	if !got.OK {
		t.Fatalf("reencoded payload must still verify, got %+v", got)
	}
}

func TestIsRetryableWriteConflictOnlySerialization(t *testing.T) {
	if !isRetryableWriteConflict(&pgconn.PgError{Code: "40001"}) {
		t.Fatal("40001 must retry")
	}
	if !isRetryableWriteConflict(&pgconn.PgError{Code: "40P01"}) {
		t.Fatal("40P01 must retry")
	}
	if isRetryableWriteConflict(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("unique violation must not retry as serialization")
	}
}

func TestValidateUpsertRejectsNegativeAndRequiresBucket(t *testing.T) {
	neg := decimal.RequireFromString("-1")
	err := validateUpsert(UpsertParams{
		Vendor: "openai", Model: "gpt-4o", Actor: "admin_token:1", ActorRole: "platform_admin",
		Rates: RateBuckets{Input: &neg},
	})
	if err == nil {
		t.Fatal("negative rate accepted")
	}
	err = validateUpsert(UpsertParams{
		Vendor: "openai", Model: "gpt-4o", Actor: "admin_token:1", ActorRole: "platform_admin",
	})
	if err == nil {
		t.Fatal("empty buckets accepted")
	}
	huge := decimal.RequireFromString("10001")
	err = validateUpsert(UpsertParams{
		Vendor: "openai", Model: "gpt-4o", Actor: "admin_token:1", ActorRole: "platform_admin",
		Rates: RateBuckets{Input: &huge},
	})
	if err == nil {
		t.Fatal("over-max rate accepted")
	}
}
