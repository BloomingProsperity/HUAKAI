package auditledger

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresLedger 是生产 audit ledger 实现，用 pgxpool 写入 audit_ledger_entries
// 表。所有 INSERT 在 transaction 内先获取 advisory lock，再读 latest merkle_root，
// 计算 prev/root/signature，写入，提交；保证链严格 append-only 不断裂。
type PostgresLedger struct {
	pool        *pgxpool.Pool
	signer      Signer
	tenantMu    sync.Mutex
	tenantLocks map[int64]*tenantLockEntry
}

// tenantLockEntry 是某租户进程内写串行锁条目,带引用计数:refs 记录当前持有或等待该锁的写者数,
// 归零时条目从 tenantLocks map 中删除,使 map 规模收敛到「当前并发活跃租户数」而非「历史出现过的全部
// 租户数」(避免无界增长)。refs 受外层 tenantMu 保护,mu 是真正的 per-tenant 写锁。
type tenantLockEntry struct {
	mu   sync.Mutex
	refs int
}

// NewPostgresLedger 构造 PostgresLedger。signer / pool 均不能为 nil。
// 调用方负责 pool 的 lifecycle（HUAKAI 由 internal/db.Open 创建并 defer Close）。
func NewPostgresLedger(pool *pgxpool.Pool, signer any) (*PostgresLedger, error) {
	if pool == nil {
		return nil, errors.New("auditledger: pgxpool.Pool required")
	}
	normalized, err := normalizeSigner(signer)
	if err != nil {
		return nil, err
	}
	return &PostgresLedger{pool: pool, signer: normalized, tenantLocks: make(map[int64]*tenantLockEntry)}, nil
}

// Append 把 prepared entry 写入 audit_ledger_entries；自动补 Timestamp / PrevMerkleRoot /
// MerkleRoot / Signature / PubkeyFingerprint。
// 用 advisory lock 串行化所有写者，保证 Merkle 链不断。
func (l *PostgresLedger) Append(ctx context.Context, prepared PreparedEntry) (LedgerEntry, error) {
	entry := prepared.AsLedgerEntry()
	if entry.RequestID == "" {
		return LedgerEntry{}, errors.New("auditledger: RequestID required for Postgres Append")
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	unlock := l.lockTenantWriter(entry.TenantID)
	defer unlock()

	tx, err := l.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("auditledger: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	entry, err = AppendInTransaction(ctx, tx, l.signer, preparedEntryFromLedgerEntry(entry))
	if err != nil {
		return LedgerEntry{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return LedgerEntry{}, fmt.Errorf("auditledger: commit: %w", err)
	}
	return entry, nil
}

func (l *PostgresLedger) AppendInTx(ctx context.Context, tx pgx.Tx, prepared PreparedEntry) (LedgerEntry, error) {
	if l == nil || tx == nil {
		return LedgerEntry{}, errors.New("auditledger: tx required for Postgres AppendInTx")
	}
	entry := prepared.AsLedgerEntry()
	if entry.RequestID == "" {
		return LedgerEntry{}, errors.New("auditledger: RequestID required for Postgres Append")
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	unlock := l.lockTenantWriter(entry.TenantID)
	defer unlock()

	return AppendInTransaction(ctx, tx, l.signer, preparedEntryFromLedgerEntry(entry))
}

// lockTenantWriter 获取某租户的进程内写串行锁,返回释放函数。采用引用计数:无人持有/等待时该租户的锁条目
// 会在释放时被回收,使 tenantLocks map 规模收敛到「当前并发活跃租户数」而非「历史出现过的全部租户数」
// (避免无界增长)。注:跨进程的真串行由 AppendInTransaction 的 pg_advisory_xact_lock 保证,本进程内锁仅为
// 减少 DB 端 advisory lock 等待的优化——故回收/重建锁条目不影响 append-only 链的正确性。
//
// 契约:返回的释放函数**必须且只能被调用一次**(惯例 `unlock := lockTenantWriter(id); defer unlock()`)。
// 重复调用会解锁一把未持有的 mutex(panic)并使 refs 变负导致条目永不回收。
// 正确性命门:e.refs++ 必须在释放 tenantMu 之前完成,否则持有者尚在时条目可能被并发删除/重建,
// 出现同租户两把进程内锁并行(由 TestLockTenantWriter_NeverTwoHeldSameTenant 锁死)。
func (l *PostgresLedger) lockTenantWriter(tenantID int64) func() {
	l.tenantMu.Lock()
	if l.tenantLocks == nil {
		l.tenantLocks = make(map[int64]*tenantLockEntry)
	}
	e := l.tenantLocks[tenantID]
	if e == nil {
		e = &tenantLockEntry{}
		l.tenantLocks[tenantID] = e
	}
	// refs 在 tenantMu 保护下自增,确保「将被使用」的条目绝不会被并发的释放路径删除。
	e.refs++
	l.tenantMu.Unlock()

	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		l.tenantMu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(l.tenantLocks, tenantID)
		}
		l.tenantMu.Unlock()
	}
}

// AppendInTransaction 使用调用方自有的数据库 transaction 追加一条 audit
// ledger entry。commit / rollback 由调用方负责。
func AppendInTransaction(ctx context.Context, q DBTX, signer any, prepared PreparedEntry) (LedgerEntry, error) {
	normalized, err := normalizeSigner(signer)
	if err != nil {
		return LedgerEntry{}, err
	}
	entry := prepared.AsLedgerEntry()
	if entry.RequestID == "" {
		return LedgerEntry{}, errors.New("auditledger: RequestID required for Postgres Append")
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	if _, err := q.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", auditLedgerAdvisoryLockKey(entry.TenantID)); err != nil {
		return LedgerEntry{}, fmt.Errorf("auditledger: advisory lock: %w", err)
	}
	ledgerID, err := nextLedgerID(ctx, q, entry.TenantID)
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("auditledger: next ledger id: %w", err)
	}
	entry.LedgerID = ledgerID

	var prev [32]byte
	prevBytes, err := readLatestMerkleRoot(ctx, q, entry.TenantID)
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("auditledger: read latest merkle: %w", err)
	}
	if prevBytes != nil {
		copy(prev[:], prevBytes)
	}
	entry.PrevMerkleRoot = prev
	fp, err := signerFingerprint(ctx, normalized)
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("auditledger: signer fingerprint: %w", err)
	}
	entry.PubkeyFingerprint = fp

	eh, err := EntryHash(&entry)
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("auditledger: entry hash: %w", err)
	}
	entry.MerkleRoot = NextMerkleRoot(prev, eh)
	sig, fp, err := normalized.Sign(ctx, eh[:])
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("auditledger: sign: %w", err)
	}
	if fp != entry.PubkeyFingerprint {
		entry.PubkeyFingerprint = fp
		eh, err = EntryHash(&entry)
		if err != nil {
			return LedgerEntry{}, fmt.Errorf("auditledger: entry hash: %w", err)
		}
		entry.MerkleRoot = NextMerkleRoot(prev, eh)
		sig, _, err = normalized.Sign(ctx, eh[:])
		if err != nil {
			return LedgerEntry{}, fmt.Errorf("auditledger: sign: %w", err)
		}
	}
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
		if isAuditLedgerRequestIDUniqueViolation(err) {
			return LedgerEntry{}, ErrDuplicateRequestID
		}
		return LedgerEntry{}, fmt.Errorf("auditledger: insert: %w", err)
	}
	return entry, nil
}

func isAuditLedgerRequestIDUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == auditLedgerEntriesRequestIDUniqueConstraint
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

// GetByRequestIDAndTenantScope 通过 request_id + tenant_scope_ref 查 ledger
// entry。request_id 唯一，先取单行再在 Go 里比对公开 tenant scope，避免扫描
// tenants 表，也不受 tenant 软删影响历史验签。
func (l *PostgresLedger) GetByRequestIDAndTenantScope(ctx context.Context, requestID, tenantScopeRef string) (LedgerEntry, error) {
	return getByRequestIDAndTenantScope(ctx, requestID, tenantScopeRef, l.GetByRequestID)
}

// ListByRange 针对给定时间区间，按 append 顺序返回经 tenant 范围过滤的
// ledger entry。先把公开的 tenant scope 解析为 ledger 行中历史的
// tenant_id，再让最终查询约束 tenant_id。
func (l *PostgresLedger) ListByRange(ctx context.Context, tenantScopeRef string, from, to time.Time, limit int) ([]LedgerEntry, error) {
	tenantScopeRef = strings.TrimSpace(tenantScopeRef)
	if tenantScopeRef == "" || limit <= 0 {
		return nil, nil
	}
	tenantID, ok, err := l.tenantIDForScopeInRange(ctx, tenantScopeRef, from, to)
	if err != nil || !ok {
		return nil, err
	}
	rows, err := l.pool.Query(ctx,
		`SELECT ledger_id, occurred_at, request_id, tenant_id,
		        hop_chain, model_chain, prev_merkle_root, merkle_root,
		        pubkey_fingerprint, signature
		 FROM audit_ledger_entries
		 WHERE tenant_id = $1
		   AND occurred_at >= $2
		   AND occurred_at <= $3
		 ORDER BY id ASC
		 LIMIT $4`,
		tenantID, from.UTC(), to.UTC(), limit,
	)
	if err != nil {
		return nil, err
	}
	return scanLedgerEntries(rows)
}

// ListByRequestIDs 按 append 顺序返回所请求的、经 tenant 范围过滤的
// ledger entry。未知的 id 以及属于其他 tenant 的 id 都会被略去。
func (l *PostgresLedger) ListByRequestIDs(ctx context.Context, tenantScopeRef string, requestIDs []string, limit int) ([]LedgerEntry, error) {
	tenantScopeRef = strings.TrimSpace(tenantScopeRef)
	requestIDs = normalizeRequestIDs(requestIDs)
	if tenantScopeRef == "" || len(requestIDs) == 0 || limit <= 0 {
		return nil, nil
	}
	tenantID, ok, err := l.tenantIDForScopeInRequestIDs(ctx, tenantScopeRef, requestIDs)
	if err != nil || !ok {
		return nil, err
	}
	rows, err := l.pool.Query(ctx,
		`SELECT ledger_id, occurred_at, request_id, tenant_id,
		        hop_chain, model_chain, prev_merkle_root, merkle_root,
		        pubkey_fingerprint, signature
		 FROM audit_ledger_entries
		 WHERE tenant_id = $1
		   AND request_id = ANY($2::text[])
		 ORDER BY id ASC
		 LIMIT $3`,
		tenantID, requestIDs, limit,
	)
	if err != nil {
		return nil, err
	}
	return scanLedgerEntries(rows)
}

func getByRequestIDAndTenantScope(
	ctx context.Context,
	requestID, tenantScopeRef string,
	getByRequestID func(context.Context, string) (LedgerEntry, error),
) (LedgerEntry, error) {
	tenantScopeRef = strings.TrimSpace(tenantScopeRef)
	if tenantScopeRef == "" {
		return LedgerEntry{}, ErrLedgerEntryNotFound
	}
	entry, err := getByRequestID(ctx, requestID)
	if err != nil {
		if errors.Is(err, ErrLedgerEntryCorrupt) {
			if !tenantScopeMatches(entry, tenantScopeRef) {
				return LedgerEntry{}, ErrLedgerEntryNotFound
			}
		}
		return LedgerEntry{}, err
	}
	if !tenantScopeMatches(entry, tenantScopeRef) {
		return LedgerEntry{}, ErrLedgerEntryNotFound
	}
	return entry, nil
}

func (l *PostgresLedger) tenantIDForScopeInRange(ctx context.Context, tenantScopeRef string, from, to time.Time) (int64, bool, error) {
	rows, err := l.pool.Query(ctx,
		`SELECT DISTINCT tenant_id
		 FROM audit_ledger_entries
		 WHERE tenant_id IS NOT NULL
		   AND occurred_at >= $1
		   AND occurred_at <= $2`,
		from.UTC(), to.UTC(),
	)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	return scanTenantIDForScope(rows, tenantScopeRef)
}

func (l *PostgresLedger) tenantIDForScopeInRequestIDs(ctx context.Context, tenantScopeRef string, requestIDs []string) (int64, bool, error) {
	rows, err := l.pool.Query(ctx,
		`SELECT DISTINCT tenant_id
		 FROM audit_ledger_entries
		 WHERE tenant_id IS NOT NULL
		   AND request_id = ANY($1::text[])`,
		requestIDs,
	)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	return scanTenantIDForScope(rows, tenantScopeRef)
}

func scanTenantIDForScope(rows pgx.Rows, tenantScopeRef string) (int64, bool, error) {
	for rows.Next() {
		var tenantID int64
		if err := rows.Scan(&tenantID); err != nil {
			return 0, false, err
		}
		if TenantScopeRef(tenantID) == tenantScopeRef {
			return tenantID, true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func scanLedgerEntries(rows pgx.Rows) ([]LedgerEntry, error) {
	defer rows.Close()
	out := make([]LedgerEntry, 0)
	for rows.Next() {
		entry, err := scanLedgerEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeRequestIDs(requestIDs []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(requestIDs))
	for _, requestID := range requestIDs {
		requestID = strings.TrimSpace(requestID)
		if requestID == "" {
			continue
		}
		if _, ok := seen[requestID]; ok {
			continue
		}
		seen[requestID] = struct{}{}
		out = append(out, requestID)
	}
	return out
}

// LatestMerkleRoot 返回最新链尾 root；空 ledger 返回 ZeroRoot。
func (l *PostgresLedger) LatestMerkleRoot(ctx context.Context) ([32]byte, error) {
	prevBytes, err := readLatestMerkleRootAny(ctx, l.pool)
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

func (l *PostgresLedger) LatestMerkleRootForTenant(ctx context.Context, tenantID int64) ([32]byte, error) {
	prevBytes, err := readLatestMerkleRoot(ctx, l.pool, tenantID)
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

const auditLedgerEntriesRequestIDUniqueConstraint = "audit_ledger_entries_request_id_key"

func readLatestMerkleRoot(ctx context.Context, q pgxQuerier, tenantID int64) ([]byte, error) {
	var prev []byte
	tenantArg := tenantDBArg(tenantID)
	err := q.QueryRow(ctx,
		"SELECT merkle_root FROM audit_ledger_entries WHERE tenant_id IS NOT DISTINCT FROM $1 ORDER BY id DESC LIMIT 1",
		tenantArg,
	).Scan(&prev)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return prev, nil
}

func readLatestMerkleRootAny(ctx context.Context, q pgxQuerier) ([]byte, error) {
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

func auditLedgerAdvisoryLockKey(tenantID int64) int64 {
	sum := sha256.Sum256([]byte("huakai-audit-ledger-writer:" + itoa64(tenantID)))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

func nextLedgerID(ctx context.Context, q pgxQuerier, tenantID int64) (string, error) {
	var n int64
	if err := q.QueryRow(ctx,
		"SELECT COUNT(*) FROM audit_ledger_entries WHERE tenant_id IS NOT DISTINCT FROM $1",
		tenantDBArg(tenantID),
	).Scan(&n); err != nil {
		return "", err
	}
	return fmt.Sprintf("ldg_%s_%020d", ledgerIDTenantPart(tenantID), n+1), nil
}

func tenantDBArg(tenantID int64) any {
	if tenantID > 0 {
		return tenantID
	}
	return nil
}

func ledgerIDTenantPart(tenantID int64) string {
	if tenantID == 0 {
		return "global"
	}
	return strings.ReplaceAll(TenantScopeRef(tenantID), ":", "_")
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
		LedgerID:  ledgerID,
		Timestamp: occurredAt.UTC().Format(time.RFC3339Nano),
		RequestID: requestID,
	}
	if tenantID != nil {
		out.TenantID = *tenantID
	}
	corruptOut := out
	out.PubkeyFingerprint = fp
	out.Signature = sig
	if len(hopJSON) > 0 {
		if err := json.Unmarshal(hopJSON, &out.HopChain); err != nil {
			return corruptOut, corruptLedgerEntryError("hop_chain json", err)
		}
	}
	if len(modelJSON) > 0 {
		if err := json.Unmarshal(modelJSON, &out.ModelChain); err != nil {
			return corruptOut, corruptLedgerEntryError("model_chain json", err)
		}
	}
	if len(prevRoot) != 32 {
		return corruptOut, corruptLedgerEntryError("prev_merkle_root length", nil)
	}
	copy(out.PrevMerkleRoot[:], prevRoot)
	if len(merkleRoot) != 32 {
		return corruptOut, corruptLedgerEntryError("merkle_root length", nil)
	}
	copy(out.MerkleRoot[:], merkleRoot)
	return out, nil
}

func corruptLedgerEntryError(field string, cause error) error {
	if cause != nil {
		return fmt.Errorf("%w: %s: %v", ErrLedgerEntryCorrupt, field, cause)
	}
	return fmt.Errorf("%w: %s", ErrLedgerEntryCorrupt, field)
}
