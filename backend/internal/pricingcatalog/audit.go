package pricingcatalog

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	RatioAuditActionUpsert = "upsert"
	RatioAuditActionDelete = "delete"
)

type PricingRatioAuditEntry struct {
	ID          int64
	OccurredAt  time.Time
	ActorID     string
	ActorRole   string
	TenantID    int64
	PoolGroupID int64
	Action      string
	OldRatio    *string
	NewRatio    *string
	PrevHash    []byte
	EntryHash   []byte
	Signature   []byte
	KeyID       string
}

type VerifyChainResult struct {
	OK     bool
	RowID  int64
	Reason string
}

type pricingRatioAuditEvent struct {
	OccurredAt  time.Time
	ActorID     string
	ActorRole   string
	TenantID    int64
	PoolGroupID int64
	Action      string
	OldRatio    *string
	NewRatio    *string
}

type pricingRatioAuditDBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type pricingRatioAuditQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func appendPricingRatioAuditInTx(ctx context.Context, tx pricingRatioAuditDBTX, signer *sign.Signer, event pricingRatioAuditEvent) (PricingRatioAuditEntry, error) {
	if tx == nil {
		return PricingRatioAuditEntry{}, ErrAuditTxMissing
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", pricingRatioAuditAdvisoryLockKey()); err != nil {
		return PricingRatioAuditEntry{}, fmt.Errorf("%w: pricing ratio audit lock: %w", ErrBackend, err)
	}
	prev, err := latestPricingRatioAuditHash(ctx, tx)
	if err != nil {
		return PricingRatioAuditEntry{}, err
	}
	entry, err := signPricingRatioAuditEntry(ctx, signer, event, prev)
	if err != nil {
		return PricingRatioAuditEntry{}, err
	}
	prevArg := any(nil)
	if len(entry.PrevHash) > 0 {
		prevArg = entry.PrevHash
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO pricing_ratio_audit_log (
    occurred_at, actor_id, actor_role, tenant_id, pool_group_id, action,
    old_ratio, new_ratio, prev_hash, entry_hash, signature, key_id
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7::text::numeric(20,8), $8::text::numeric(20,8), $9, $10, $11, $12
)`,
		entry.OccurredAt,
		entry.ActorID,
		entry.ActorRole,
		entry.TenantID,
		entry.PoolGroupID,
		entry.Action,
		ratioTextArg(entry.OldRatio),
		ratioTextArg(entry.NewRatio),
		prevArg,
		entry.EntryHash,
		entry.Signature,
		entry.KeyID,
	); err != nil {
		return PricingRatioAuditEntry{}, fmt.Errorf("%w: insert pricing ratio audit: %w", ErrBackend, err)
	}
	return entry, nil
}

func signPricingRatioAuditEntry(ctx context.Context, signer *sign.Signer, event pricingRatioAuditEvent, prevHash []byte) (PricingRatioAuditEntry, error) {
	if signer == nil {
		return PricingRatioAuditEntry{}, ErrAuditSignerMissing
	}
	if err := validatePricingRatioAuditEvent(event); err != nil {
		return PricingRatioAuditEntry{}, err
	}
	entry := PricingRatioAuditEntry{
		OccurredAt:  event.OccurredAt.UTC(),
		ActorID:     strings.TrimSpace(event.ActorID),
		ActorRole:   strings.TrimSpace(event.ActorRole),
		TenantID:    event.TenantID,
		PoolGroupID: event.PoolGroupID,
		Action:      event.Action,
		OldRatio:    cloneRatioText(event.OldRatio),
		NewRatio:    cloneRatioText(event.NewRatio),
		PrevHash:    append([]byte(nil), prevHash...),
		KeyID:       signer.Fingerprint(),
	}
	canonical := canonicalPricingRatioAuditPayload(entry)
	sum := sha256.Sum256(append(canonical, entry.PrevHash...))
	entry.EntryHash = append([]byte(nil), sum[:]...)
	entry.Signature = signer.Sign(entry.EntryHash)
	return entry, nil
}

func validatePricingRatioAuditEvent(event pricingRatioAuditEvent) error {
	if strings.TrimSpace(event.ActorID) == "" ||
		strings.TrimSpace(event.ActorRole) == "" ||
		event.TenantID <= 0 ||
		event.PoolGroupID <= 0 ||
		event.OccurredAt.IsZero() {
		return ErrInvalidInput
	}
	switch event.Action {
	case RatioAuditActionUpsert:
		if event.NewRatio == nil || strings.TrimSpace(*event.NewRatio) == "" {
			return ErrInvalidInput
		}
	case RatioAuditActionDelete:
		if event.OldRatio == nil || strings.TrimSpace(*event.OldRatio) == "" || event.NewRatio != nil {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

func latestPricingRatioAuditHash(ctx context.Context, q pricingRatioAuditDBTX) ([]byte, error) {
	var prev []byte
	err := q.QueryRow(ctx, "SELECT entry_hash FROM pricing_ratio_audit_log ORDER BY id DESC LIMIT 1").Scan(&prev)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read pricing ratio audit tail: %w", ErrBackend, err)
	}
	return prev, nil
}

func (s *PostgresStore) VerifyChain(ctx context.Context) (VerifyChainResult, error) {
	if s == nil || s.db == nil {
		return VerifyChainResult{}, fmt.Errorf("%w: store not configured", ErrBackend)
	}
	if s.signer == nil {
		return VerifyChainResult{}, ErrAuditSignerMissing
	}
	entries, err := loadPricingRatioAuditEntries(ctx, s.db)
	if err != nil {
		return VerifyChainResult{}, err
	}
	return VerifyPricingRatioAuditEntries(ctx, s.signer.PublicKey(), entries), nil
}

func loadPricingRatioAuditEntries(ctx context.Context, q pricingRatioAuditQueryer) ([]PricingRatioAuditEntry, error) {
	rows, err := q.Query(ctx, `
SELECT
    id,
    occurred_at,
    actor_id,
    actor_role,
    tenant_id,
    pool_group_id,
    action,
    old_ratio::numeric(20,8)::text,
    new_ratio::numeric(20,8)::text,
    prev_hash,
    entry_hash,
    signature,
    key_id
FROM pricing_ratio_audit_log
ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("%w: query pricing ratio audit chain: %w", ErrBackend, err)
	}
	defer rows.Close()
	var entries []PricingRatioAuditEntry
	for rows.Next() {
		var (
			entry       PricingRatioAuditEntry
			oldRatio    pgtype.Text
			newRatio    pgtype.Text
			prevHash    []byte
			entryHash   []byte
			signature   []byte
			occurredAt  time.Time
			actorID     string
			actorRole   string
			action      string
			keyID       string
			tenantID    int64
			poolGroupID int64
		)
		if err := rows.Scan(
			&entry.ID,
			&occurredAt,
			&actorID,
			&actorRole,
			&tenantID,
			&poolGroupID,
			&action,
			&oldRatio,
			&newRatio,
			&prevHash,
			&entryHash,
			&signature,
			&keyID,
		); err != nil {
			return nil, fmt.Errorf("%w: scan pricing ratio audit chain: %w", ErrBackend, err)
		}
		entry.OccurredAt = occurredAt.UTC()
		entry.ActorID = actorID
		entry.ActorRole = actorRole
		entry.TenantID = tenantID
		entry.PoolGroupID = poolGroupID
		entry.Action = action
		entry.OldRatio = pgTextPtr(oldRatio)
		entry.NewRatio = pgTextPtr(newRatio)
		entry.PrevHash = append([]byte(nil), prevHash...)
		entry.EntryHash = append([]byte(nil), entryHash...)
		entry.Signature = append([]byte(nil), signature...)
		entry.KeyID = keyID
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: iterate pricing ratio audit chain: %w", ErrBackend, err)
	}
	return entries, nil
}

func VerifyPricingRatioAuditEntries(_ context.Context, publicKey ed25519.PublicKey, entries []PricingRatioAuditEntry) VerifyChainResult {
	var previous []byte
	for _, entry := range entries {
		if !bytes.Equal(entry.PrevHash, previous) {
			return VerifyChainResult{RowID: entry.ID, Reason: "prev_hash mismatch"}
		}
		canonical := canonicalPricingRatioAuditPayload(entry)
		sum := sha256.Sum256(append(canonical, entry.PrevHash...))
		if !bytes.Equal(entry.EntryHash, sum[:]) {
			return VerifyChainResult{RowID: entry.ID, Reason: "entry_hash mismatch"}
		}
		if entry.KeyID != sign.Fingerprint(publicKey) {
			return VerifyChainResult{RowID: entry.ID, Reason: "key_id mismatch"}
		}
		if err := sign.Verify(publicKey, entry.EntryHash, entry.Signature); err != nil {
			return VerifyChainResult{RowID: entry.ID, Reason: "signature mismatch"}
		}
		previous = append(previous[:0], entry.EntryHash...)
	}
	return VerifyChainResult{OK: true}
}

func canonicalPricingRatioAuditPayload(entry PricingRatioAuditEntry) []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	writeCanonicalJSONField(&buf, "actor_id", entry.ActorID, true)
	writeCanonicalJSONField(&buf, "actor_role", entry.ActorRole, false)
	writeCanonicalJSONIntField(&buf, "tenant_id", entry.TenantID, false)
	writeCanonicalJSONIntField(&buf, "pool_group_id", entry.PoolGroupID, false)
	writeCanonicalJSONField(&buf, "action", entry.Action, false)
	writeCanonicalOptionalRatioField(&buf, "old_ratio", entry.OldRatio, false)
	writeCanonicalOptionalRatioField(&buf, "new_ratio", entry.NewRatio, false)
	writeCanonicalJSONField(&buf, "occurred_at", entry.OccurredAt.UTC().Format(time.RFC3339Nano), false)
	buf.WriteByte('}')
	return buf.Bytes()
}

func writeCanonicalJSONField(buf *bytes.Buffer, key, value string, first bool) {
	if !first {
		buf.WriteByte(',')
	}
	writeJSONString(buf, key)
	buf.WriteByte(':')
	writeJSONString(buf, value)
}

func writeCanonicalJSONIntField(buf *bytes.Buffer, key string, value int64, first bool) {
	if !first {
		buf.WriteByte(',')
	}
	writeJSONString(buf, key)
	buf.WriteByte(':')
	buf.WriteString(strconv.FormatInt(value, 10))
}

func writeCanonicalOptionalRatioField(buf *bytes.Buffer, key string, value *string, first bool) {
	if !first {
		buf.WriteByte(',')
	}
	writeJSONString(buf, key)
	buf.WriteByte(':')
	if value == nil {
		buf.WriteString("null")
		return
	}
	writeJSONString(buf, *value)
}

func writeJSONString(buf *bytes.Buffer, value string) {
	raw, _ := json.Marshal(value)
	buf.Write(raw)
}

func pricingRatioAuditAdvisoryLockKey() int64 {
	sum := sha256.Sum256([]byte("huakai-pricing-ratio-audit-log-writer"))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

func ratioTextArg(v *string) any {
	if v == nil {
		return nil
	}
	return strings.TrimSpace(*v)
}

func cloneRatioText(v *string) *string {
	if v == nil {
		return nil
	}
	out := strings.TrimSpace(*v)
	return &out
}

func pgTextPtr(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	out := v.String
	return &out
}
