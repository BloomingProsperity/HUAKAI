package auditledger

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

// auditLedgerAdvisoryLockID 是 pg_advisory_xact_lock 用的固定 ID，确保任意
// 时刻只有一个 transaction 在 INSERT audit_ledger_entries（防 Merkle 链断裂）。
//
// 取值 = sha256("huakai_audit_ledger_writer")[:8] 转 int64，固定不变；
// 这里硬编码避免运行期算。
const auditLedgerAdvisoryLockID int64 = 0x4855414B41495F4C // "HUAKAI_L" 前缀辨识

// PostgresLedger 是生产 audit ledger 实现，用 pgxpool 写入 audit_ledger_entries
// 表。所有 INSERT 在 transaction 内先获取 advisory lock，再读 latest merkle_root，
// 计算 prev/root/signature，写入，提交；保证链严格 append-only 不断裂。
type PostgresLedger struct {
	pool   *pgxpool.Pool
	signer *sign.Signer
}

// NewPostgresLedger 构造 PostgresLedger。signer / pool 均不能为 nil。
// 调用方负责 pool 的 lifecycle（HUAKAI 由 internal/db.Open 创建并 defer Close）。
func NewPostgresLedger(pool *pgxpool.Pool, signer *sign.Signer) (*PostgresLedger, error) {
	if pool == nil {
		return nil, errors.New("auditledger: pgxpool.Pool required")
	}
	if signer == nil {
		return nil, ErrSignerNil
	}
	return &PostgresLedger{pool: pool, signer: signer}, nil
}

// Append 把 entry 写入 audit_ledger_entries；自动补 Timestamp / PrevMerkleRoot /
// MerkleRoot / Signature / PubkeyFingerprint。
// 用 advisory lock 串行化所有写者，保证 Merkle 链不断。
func (l *PostgresLedger) Append(ctx context.Context, entry LedgerEntry) (LedgerEntry, error) {
	if entry.RequestID == "" {
		return LedgerEntry{}, errors.New("auditledger: RequestID required for Postgres Append")
	}
	if entry.LedgerID == "" {
		return LedgerEntry{}, errors.New("auditledger: LedgerID required for Postgres Append")
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	tx, err := l.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("auditledger: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	entry, err = AppendInTransaction(ctx, tx, l.signer, entry)
	if err != nil {
		return LedgerEntry{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return LedgerEntry{}, fmt.Errorf("auditledger: commit: %w", err)
	}
	return entry, nil
}

// AppendInTransaction appends an audit ledger entry using the caller-owned
// database transaction. The caller is responsible for commit/rollback.
func AppendInTransaction(ctx context.Context, q DBTX, signer *sign.Signer, entry LedgerEntry) (LedgerEntry, error) {
	if signer == nil {
		return LedgerEntry{}, ErrSignerNil
	}
	if entry.RequestID == "" {
		return LedgerEntry{}, errors.New("auditledger: RequestID required for Postgres Append")
	}
	if entry.LedgerID == "" {
		return LedgerEntry{}, errors.New("auditledger: LedgerID required for Postgres Append")
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	if _, err := q.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", auditLedgerAdvisoryLockID); err != nil {
		return LedgerEntry{}, fmt.Errorf("auditledger: advisory lock: %w", err)
	}

	var prev [32]byte
	prevBytes, err := readLatestMerkleRoot(ctx, q)
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("auditledger: read latest merkle: %w", err)
	}
	if prevBytes != nil {
		copy(prev[:], prevBytes)
	}
	entry.PrevMerkleRoot = prev
	entry.PubkeyFingerprint = signer.Fingerprint()

	eh, err := EntryHash(&entry)
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("auditledger: entry hash: %w", err)
	}
	entry.MerkleRoot = NextMerkleRoot(prev, eh)
	sig := signer.Sign(eh[:])
	entry.Signature = base64.StdEncoding.EncodeToString(sig)

	hopJSON, err := json.Marshal(entry.HopChain)
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("auditledger: marshal hop_chain: %w", err)
	}
	var modelJSON []byte
	if entry.ModelChain != nil {
		modelJSON, err = json.Marshal(entry.ModelChain)
		if err != nil {
			return LedgerEntry{}, fmt.Errorf("auditledger: marshal model_chain: %w", err)
		}
	}

	// 解析 timestamp
	occurredAt, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("auditledger: parse timestamp: %w", err)
	}

	var tenantArg any
	if entry.TenantID > 0 {
		tenantArg = entry.TenantID
	}

	_, err = q.Exec(ctx,
		`INSERT INTO audit_ledger_entries (
			ledger_id, occurred_at, request_id, tenant_id,
			hop_chain, model_chain, prev_merkle_root, merkle_root,
			pubkey_fingerprint, signature
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		entry.LedgerID,
		occurredAt,
		entry.RequestID,
		tenantArg,
		hopJSON,
		modelJSON,
		entry.PrevMerkleRoot[:],
		entry.MerkleRoot[:],
		entry.PubkeyFingerprint,
		entry.Signature,
	)
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("auditledger: insert: %w", err)
	}
	return entry, nil
}

// GetByRequestID 通过 request_id 查 ledger entry。
func (l *PostgresLedger) GetByRequestID(ctx context.Context, requestID string) (LedgerEntry, error) {
	row := l.pool.QueryRow(ctx,
		`SELECT ledger_id, occurred_at, request_id, tenant_id,
		        hop_chain, model_chain, prev_merkle_root, merkle_root,
		        pubkey_fingerprint, signature
		 FROM audit_ledger_entries
		 WHERE request_id = $1`,
		requestID,
	)
	return scanLedgerEntry(row)
}

// LatestMerkleRoot 返回最新链尾 root；空 ledger 返回 ZeroRoot。
func (l *PostgresLedger) LatestMerkleRoot(ctx context.Context) ([32]byte, error) {
	prevBytes, err := readLatestMerkleRoot(ctx, l.pool)
	if err != nil {
		return ZeroRoot, err
	}
	if prevBytes == nil {
		return ZeroRoot, nil
	}
	var out [32]byte
	copy(out[:], prevBytes)
	return out, nil
}

// Size 返回当前 entry 数量。
func (l *PostgresLedger) Size(ctx context.Context) int {
	var n int64
	row := l.pool.QueryRow(ctx, "SELECT COUNT(*) FROM audit_ledger_entries")
	_ = row.Scan(&n)
	return int(n)
}

// pgxQuerier 是 pgxpool.Pool 与 pgx.Tx 共同实现的子集，使 readLatestMerkleRoot
// 在事务内外都能用。
type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func readLatestMerkleRoot(ctx context.Context, q pgxQuerier) ([]byte, error) {
	var prev []byte
	err := q.QueryRow(ctx,
		"SELECT merkle_root FROM audit_ledger_entries ORDER BY id DESC LIMIT 1",
	).Scan(&prev)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return prev, nil
}

// scanLedgerEntry 把一行 PG row 转为 LedgerEntry。
// 接受 pgx.Row（QueryRow 返回值）。
func scanLedgerEntry(row pgx.Row) (LedgerEntry, error) {
	var (
		ledgerID, requestID, fp, sig string
		occurredAt                   time.Time
		tenantID                     *int64
		hopJSON                      []byte
		modelJSON                    []byte
		prevRoot, merkleRoot         []byte
	)
	err := row.Scan(&ledgerID, &occurredAt, &requestID, &tenantID,
		&hopJSON, &modelJSON, &prevRoot, &merkleRoot, &fp, &sig)
	if errors.Is(err, pgx.ErrNoRows) {
		return LedgerEntry{}, ErrLedgerEntryNotFound
	}
	if err != nil {
		return LedgerEntry{}, err
	}
	out := LedgerEntry{
		LedgerID:          ledgerID,
		Timestamp:         occurredAt.UTC().Format(time.RFC3339Nano),
		RequestID:         requestID,
		PubkeyFingerprint: fp,
		Signature:         sig,
	}
	if tenantID != nil {
		out.TenantID = *tenantID
	}
	if len(hopJSON) > 0 {
		_ = json.Unmarshal(hopJSON, &out.HopChain)
	}
	if len(modelJSON) > 0 {
		_ = json.Unmarshal(modelJSON, &out.ModelChain)
	}
	if len(prevRoot) == 32 {
		copy(out.PrevMerkleRoot[:], prevRoot)
	}
	if len(merkleRoot) == 32 {
		copy(out.MerkleRoot[:], merkleRoot)
	}
	return out, nil
}
