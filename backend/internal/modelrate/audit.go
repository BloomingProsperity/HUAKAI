package modelrate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type AuditEntry struct {
	ID         int64
	OccurredAt time.Time
	ActorID    string
	ActorRole  string
	Vendor     string
	Model      string
	Action     string
	OldPayload json.RawMessage
	NewPayload json.RawMessage
	PrevHash   []byte
	EntryHash  []byte
	Signature  []byte
	KeyID      string
}

type auditEvent struct {
	OccurredAt time.Time
	ActorID    string
	ActorRole  string
	Vendor     string
	Model      string
	Action     string
	OldPayload json.RawMessage
	NewPayload json.RawMessage
}

type auditDBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type auditQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func appendAuditInTx(ctx context.Context, tx auditDBTX, signer *sign.Signer, event auditEvent) (AuditEntry, error) {
	if tx == nil {
		return AuditEntry{}, ErrAuditTxMissing
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", auditLockKey()); err != nil {
		return AuditEntry{}, fmt.Errorf("%w: model rate audit lock: %w", ErrBackend, err)
	}
	prev, err := latestAuditHash(ctx, tx)
	if err != nil {
		return AuditEntry{}, err
	}
	entry, err := signAuditEntry(signer, event, prev)
	if err != nil {
		return AuditEntry{}, err
	}
	var prevArg any
	if len(entry.PrevHash) > 0 {
		prevArg = entry.PrevHash
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO model_rate_override_audit_log (
    occurred_at, actor_id, actor_role, vendor, model, action,
    old_payload, new_payload, prev_hash, entry_hash, signature, key_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		entry.OccurredAt,
		entry.ActorID,
		entry.ActorRole,
		entry.Vendor,
		entry.Model,
		entry.Action,
		nullableJSON(entry.OldPayload),
		nullableJSON(entry.NewPayload),
		prevArg,
		entry.EntryHash,
		entry.Signature,
		entry.KeyID,
	); err != nil {
		return AuditEntry{}, fmt.Errorf("%w: insert model rate audit: %w", ErrBackend, err)
	}
	return entry, nil
}

func signAuditEntry(signer *sign.Signer, event auditEvent, prevHash []byte) (AuditEntry, error) {
	if signer == nil {
		return AuditEntry{}, ErrAuditSignerMissing
	}
	if err := validateAuditEvent(event); err != nil {
		return AuditEntry{}, err
	}
	entry := AuditEntry{
		// timestamptz 只保存到微秒;签名时钟若残留纳秒,回读后哈希会对不上。
		OccurredAt: event.OccurredAt.UTC().Truncate(time.Microsecond),
		ActorID:    strings.TrimSpace(event.ActorID),
		ActorRole:  strings.TrimSpace(event.ActorRole),
		Vendor:     normalizeVendor(event.Vendor),
		Model:      normalizeModel(event.Model),
		Action:     strings.TrimSpace(event.Action),
		OldPayload: canonicalizeJSON(event.OldPayload),
		NewPayload: canonicalizeJSON(event.NewPayload),
		PrevHash:   append([]byte(nil), prevHash...),
		KeyID:      signer.Fingerprint(),
	}
	canonical := canonicalAuditPayload(entry)
	sum := sha256.Sum256(append(canonical, entry.PrevHash...))
	entry.EntryHash = append([]byte(nil), sum[:]...)
	entry.Signature = signer.Sign(entry.EntryHash)
	return entry, nil
}

func validateAuditEvent(event auditEvent) error {
	if strings.TrimSpace(event.ActorID) == "" ||
		strings.TrimSpace(event.ActorRole) == "" ||
		normalizeVendor(event.Vendor) == "" ||
		normalizeModel(event.Model) == "" ||
		event.OccurredAt.IsZero() {
		return ErrInvalidInput
	}
	switch event.Action {
	case ActionUpsert:
		if len(event.NewPayload) == 0 {
			return ErrInvalidInput
		}
	case ActionDelete:
		if len(event.OldPayload) == 0 || len(event.NewPayload) != 0 {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

func latestAuditHash(ctx context.Context, q auditDBTX) ([]byte, error) {
	var prev []byte
	err := q.QueryRow(ctx, "SELECT entry_hash FROM model_rate_override_audit_log ORDER BY id DESC LIMIT 1").Scan(&prev)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read model rate audit tail: %w", ErrBackend, err)
	}
	return prev, nil
}

func loadAuditEntries(ctx context.Context, q auditQueryer) ([]AuditEntry, error) {
	rows, err := q.Query(ctx, `
SELECT
    id, occurred_at, actor_id, actor_role, vendor, model, action,
    old_payload, new_payload, prev_hash, entry_hash, signature, key_id
FROM model_rate_override_audit_log
ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("%w: query model rate audit chain: %w", ErrBackend, err)
	}
	defer rows.Close()
	var entries []AuditEntry
	for rows.Next() {
		var entry AuditEntry
		if err := rows.Scan(
			&entry.ID,
			&entry.OccurredAt,
			&entry.ActorID,
			&entry.ActorRole,
			&entry.Vendor,
			&entry.Model,
			&entry.Action,
			&entry.OldPayload,
			&entry.NewPayload,
			&entry.PrevHash,
			&entry.EntryHash,
			&entry.Signature,
			&entry.KeyID,
		); err != nil {
			return nil, fmt.Errorf("%w: scan model rate audit chain: %w", ErrBackend, err)
		}
		entry.OccurredAt = entry.OccurredAt.UTC().Truncate(time.Microsecond)
		entry.OldPayload = canonicalizeJSON(entry.OldPayload)
		entry.NewPayload = canonicalizeJSON(entry.NewPayload)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: iterate model rate audit chain: %w", ErrBackend, err)
	}
	return entries, nil
}

func VerifyAuditEntries(_ context.Context, publicKey ed25519.PublicKey, entries []AuditEntry) VerifyResult {
	var previous []byte
	for _, entry := range entries {
		if !bytes.Equal(entry.PrevHash, previous) {
			return VerifyResult{RowID: entry.ID, Reason: "prev_hash mismatch"}
		}
		sum := sha256.Sum256(append(canonicalAuditPayload(entry), entry.PrevHash...))
		if !bytes.Equal(entry.EntryHash, sum[:]) {
			return VerifyResult{RowID: entry.ID, Reason: "entry_hash mismatch"}
		}
		if entry.KeyID != sign.Fingerprint(publicKey) {
			return VerifyResult{RowID: entry.ID, Reason: "key_id mismatch"}
		}
		if err := sign.Verify(publicKey, entry.EntryHash, entry.Signature); err != nil {
			return VerifyResult{RowID: entry.ID, Reason: "signature mismatch"}
		}
		previous = append(previous[:0], entry.EntryHash...)
	}
	return VerifyResult{OK: true}
}

func canonicalAuditPayload(entry AuditEntry) []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	writeCanonicalString(&buf, "actor_id", entry.ActorID, true)
	writeCanonicalString(&buf, "actor_role", entry.ActorRole, false)
	writeCanonicalString(&buf, "vendor", entry.Vendor, false)
	writeCanonicalString(&buf, "model", entry.Model, false)
	writeCanonicalString(&buf, "action", entry.Action, false)
	writeCanonicalRaw(&buf, "old_payload", canonicalizeJSON(entry.OldPayload), false)
	writeCanonicalRaw(&buf, "new_payload", canonicalizeJSON(entry.NewPayload), false)
	writeCanonicalString(&buf, "occurred_at", formatAuditTime(entry.OccurredAt), false)
	buf.WriteByte('}')
	return buf.Bytes()
}

func writeCanonicalString(buf *bytes.Buffer, key, value string, first bool) {
	if !first {
		buf.WriteByte(',')
	}
	raw, _ := json.Marshal(key)
	buf.Write(raw)
	buf.WriteByte(':')
	raw, _ = json.Marshal(value)
	buf.Write(raw)
}

func writeCanonicalRaw(buf *bytes.Buffer, key string, value json.RawMessage, first bool) {
	if !first {
		buf.WriteByte(',')
	}
	raw, _ := json.Marshal(key)
	buf.Write(raw)
	buf.WriteByte(':')
	if len(value) == 0 {
		buf.WriteString("null")
		return
	}
	buf.Write(value)
}

func auditLockKey() int64 {
	sum := sha256.Sum256([]byte("huakai-model-rate-override-audit-log-writer"))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

func cloneJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func canonicalizeJSON(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	var value any
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return cloneJSON(trimmed)
	}
	out, err := json.Marshal(value)
	if err != nil {
		return cloneJSON(trimmed)
	}
	return out
}

func formatAuditTime(value time.Time) string {
	return value.UTC().Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z")
}
