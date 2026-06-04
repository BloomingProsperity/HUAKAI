package pricingcatalog

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/jackc/pgx/v5"
)

func TestPricingRatioAuditSignedChainDetectsTamperAndDelete(t *testing.T) {
	ctx := context.Background()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	at := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)

	first, err := signPricingRatioAuditEntry(ctx, signer, pricingRatioAuditEvent{
		OccurredAt:  at,
		ActorID:     "admin_token:99",
		ActorRole:   "platform_admin",
		TenantID:    7,
		PoolGroupID: 42,
		Action:      RatioAuditActionUpsert,
		NewRatio:    stringPtrForRatioAuditTest("1.25000000"),
	}, nil)
	if err != nil {
		t.Fatalf("sign first audit entry: %v", err)
	}
	if first.Action != RatioAuditActionUpsert || first.NewRatio == nil || *first.NewRatio != "1.25000000" {
		t.Fatalf("first audit action/new_ratio=%s/%v", first.Action, first.NewRatio)
	}
	if first.ActorID != "admin_token:99" || first.ActorRole != "platform_admin" {
		t.Fatalf("actor=%q role=%q want authenticated admin actor", first.ActorID, first.ActorRole)
	}
	if len(first.PrevHash) != 0 {
		t.Fatalf("first prev_hash len=%d want empty", len(first.PrevHash))
	}
	if len(first.EntryHash) != 32 || len(first.Signature) != 64 || first.KeyID != signer.Fingerprint() {
		t.Fatalf("signed fields hash=%d sig=%d key=%q", len(first.EntryHash), len(first.Signature), first.KeyID)
	}
	if err := sign.Verify(signer.PublicKey(), first.EntryHash, first.Signature); err != nil {
		t.Fatalf("entry_hash signature did not verify: %v", err)
	}

	second, err := signPricingRatioAuditEntry(ctx, signer, pricingRatioAuditEvent{
		OccurredAt:  at.Add(time.Second),
		ActorID:     "admin_token:99",
		ActorRole:   "platform_admin",
		TenantID:    7,
		PoolGroupID: 42,
		Action:      RatioAuditActionUpsert,
		OldRatio:    stringPtrForRatioAuditTest("1.25000000"),
		NewRatio:    stringPtrForRatioAuditTest("1.75000000"),
	}, first.EntryHash)
	if err != nil {
		t.Fatalf("sign second audit entry: %v", err)
	}
	if !bytes.Equal(second.PrevHash, first.EntryHash) {
		t.Fatalf("second prev_hash=%x want first entry_hash=%x", second.PrevHash, first.EntryHash)
	}

	third, err := signPricingRatioAuditEntry(ctx, signer, pricingRatioAuditEvent{
		OccurredAt:  at.Add(2 * time.Second),
		ActorID:     "admin_token:99",
		ActorRole:   "platform_admin",
		TenantID:    7,
		PoolGroupID: 42,
		Action:      RatioAuditActionDelete,
		OldRatio:    stringPtrForRatioAuditTest("1.75000000"),
	}, second.EntryHash)
	if err != nil {
		t.Fatalf("sign delete audit entry: %v", err)
	}
	if third.Action != RatioAuditActionDelete || third.OldRatio == nil || *third.OldRatio != "1.75000000" || third.NewRatio != nil {
		t.Fatalf("delete audit old/new=%v/%v", third.OldRatio, third.NewRatio)
	}

	entries := []PricingRatioAuditEntry{
		withAuditRowIDForTest(first, 1),
		withAuditRowIDForTest(second, 2),
		withAuditRowIDForTest(third, 3),
	}
	if result := VerifyPricingRatioAuditEntries(ctx, signer.PublicKey(), entries); !result.OK {
		t.Fatalf("VerifyPricingRatioAuditEntries result=%+v want OK", result)
	}

	tampered := cloneAuditEntriesForTest(entries)
	*tampered[1].NewRatio = "0.30000000"
	// Mutation check: if verification trusts stored entry_hash/signature without
	// recomputing canonical data, this tamper stays green and the test fails.
	if result := VerifyPricingRatioAuditEntries(ctx, signer.PublicKey(), tampered); result.OK || result.RowID != 2 || !strings.Contains(result.Reason, "entry_hash") {
		t.Fatalf("tamper result=%+v want row 2 entry_hash failure", result)
	}

	deletedMiddle := []PricingRatioAuditEntry{entries[0], entries[2]}
	// Mutation check: if prev_hash chaining is removed, deleting row 2 is not
	// detected and this assertion fails.
	if result := VerifyPricingRatioAuditEntries(ctx, signer.PublicKey(), deletedMiddle); result.OK || result.RowID != 3 || !strings.Contains(result.Reason, "prev_hash") {
		t.Fatalf("deleted-middle result=%+v want row 3 prev_hash failure", result)
	}
}

func TestLoadPricingRatioAuditEntriesUsesAppendIDOrderForChainVerification(t *testing.T) {
	ctx := context.Background()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	firstAt := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	secondAt := firstAt.Add(-time.Minute)

	first, err := signPricingRatioAuditEntry(ctx, signer, pricingRatioAuditEvent{
		OccurredAt:  firstAt,
		ActorID:     "admin_token:99",
		ActorRole:   "platform_admin",
		TenantID:    7,
		PoolGroupID: 42,
		Action:      RatioAuditActionUpsert,
		NewRatio:    stringPtrForRatioAuditTest("1.25000000"),
	}, nil)
	if err != nil {
		t.Fatalf("sign first audit entry: %v", err)
	}
	second, err := signPricingRatioAuditEntry(ctx, signer, pricingRatioAuditEvent{
		OccurredAt:  secondAt,
		ActorID:     "admin_token:99",
		ActorRole:   "platform_admin",
		TenantID:    7,
		PoolGroupID: 42,
		Action:      RatioAuditActionUpsert,
		OldRatio:    stringPtrForRatioAuditTest("1.25000000"),
		NewRatio:    stringPtrForRatioAuditTest("1.50000000"),
	}, first.EntryHash)
	if err != nil {
		t.Fatalf("sign second audit entry: %v", err)
	}

	entries, err := loadPricingRatioAuditEntries(ctx, &ratioAuditOrderQueryerForTest{rows: []PricingRatioAuditEntry{
		withAuditRowIDForTest(first, 1),
		withAuditRowIDForTest(second, 2),
	}})
	if err != nil {
		t.Fatalf("loadPricingRatioAuditEntries: %v", err)
	}

	// 判别性夹具: row 2 的 occurred_at 早于 row 1, 旧的时间排序会把合法 id 链倒置成 prev_hash mismatch。
	if result := VerifyPricingRatioAuditEntries(ctx, signer.PublicKey(), entries); !result.OK {
		t.Fatalf("VerifyPricingRatioAuditEntries result=%+v want OK for append-id ordered chain", result)
	}
}

func withAuditRowIDForTest(entry PricingRatioAuditEntry, id int64) PricingRatioAuditEntry {
	entry.ID = id
	return entry
}

func cloneAuditEntriesForTest(entries []PricingRatioAuditEntry) []PricingRatioAuditEntry {
	out := make([]PricingRatioAuditEntry, len(entries))
	for i := range entries {
		out[i] = entries[i]
		out[i].PrevHash = append([]byte(nil), entries[i].PrevHash...)
		out[i].EntryHash = append([]byte(nil), entries[i].EntryHash...)
		out[i].Signature = append([]byte(nil), entries[i].Signature...)
		if entries[i].OldRatio != nil {
			out[i].OldRatio = stringPtrForRatioAuditTest(*entries[i].OldRatio)
		}
		if entries[i].NewRatio != nil {
			out[i].NewRatio = stringPtrForRatioAuditTest(*entries[i].NewRatio)
		}
	}
	return out
}

func stringPtrForRatioAuditTest(v string) *string {
	return &v
}

type ratioAuditOrderQueryerForTest struct {
	rows []PricingRatioAuditEntry
}

func (q *ratioAuditOrderQueryerForTest) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	rows := cloneAuditEntriesForTest(q.rows)
	switch {
	case strings.Contains(sql, "ORDER BY id ASC"):
		sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	case strings.Contains(sql, "ORDER BY occurred_at ASC, id ASC"):
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].OccurredAt.Equal(rows[j].OccurredAt) {
				return rows[i].ID < rows[j].ID
			}
			return rows[i].OccurredAt.Before(rows[j].OccurredAt)
		})
	default:
		return nil, errors.New("pricing ratio audit query missing deterministic order")
	}
	return &ratioAuditRows{rows: rows}, nil
}
