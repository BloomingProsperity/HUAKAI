package auditledger

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

// Ledger 是 HUAKAI 信任链 audit ledger 的抽象。生产用 PostgresLedger（T4.x
// 待实现），test / dev 用 MemoryLedger，禁用场景用 NoopLedger。
type Ledger interface {
	// Append 把 entry 写入 ledger 末尾；自动计算 PrevMerkleRoot / MerkleRoot /
	// Signature / PubkeyFingerprint（调用方提交时这 4 个字段可留空）。
	// 返回写入后的 LedgerEntry（含 4 个补齐字段）。
	Append(ctx context.Context, entry LedgerEntry) (LedgerEntry, error)

	// GetByRequestID 通过 request_id 取出对应的 ledger entry；
	// not found 返回 ErrLedgerEntryNotFound。
	GetByRequestID(ctx context.Context, requestID string) (LedgerEntry, error)

	// LatestMerkleRoot 返回当前链尾 Merkle root；空 ledger 返回 ZeroRoot。
	LatestMerkleRoot(ctx context.Context) ([32]byte, error)

	// Size 返回当前 entry 数量。
	Size(ctx context.Context) int
}

// ErrLedgerEntryNotFound get 不到时返回。
var ErrLedgerEntryNotFound = errors.New("auditledger: ledger entry not found")

// ErrSignerNil Append 时 signer 未设。
var ErrSignerNil = errors.New("auditledger: signer not set")

// NoopLedger 是禁用 ledger 时的零开销实现；Append 直接返回不存。
// Personal Edition 默认配置可用 NoopLedger；SaaS Edition 必须用 MemoryLedger /
// PostgresLedger。
type NoopLedger struct{}

// Append 不存任何东西，返回原 entry 不动。
func (NoopLedger) Append(ctx context.Context, entry LedgerEntry) (LedgerEntry, error) {
	return entry, nil
}

// GetByRequestID 永远返回 not found。
func (NoopLedger) GetByRequestID(ctx context.Context, requestID string) (LedgerEntry, error) {
	return LedgerEntry{}, ErrLedgerEntryNotFound
}

// LatestMerkleRoot 永远返回 ZeroRoot。
func (NoopLedger) LatestMerkleRoot(ctx context.Context) ([32]byte, error) {
	return ZeroRoot, nil
}

// Size 永远 0。
func (NoopLedger) Size(ctx context.Context) int { return 0 }

// MemoryLedger 是 append-only in-memory ledger，用于 dev / test。
// 并发安全。
type MemoryLedger struct {
	signer *sign.Signer
	mu     sync.RWMutex
	chain  []LedgerEntry
	byReq  map[string]int // request_id → index
}

// NewMemoryLedger 用给定 signer 构造 MemoryLedger；signer 为 nil 会返回错误。
func NewMemoryLedger(signer *sign.Signer) (*MemoryLedger, error) {
	if signer == nil {
		return nil, ErrSignerNil
	}
	return &MemoryLedger{
		signer: signer,
		byReq:  make(map[string]int),
	}, nil
}

// Append 把 entry 写入末尾，自动补 PrevMerkleRoot / MerkleRoot / Signature /
// PubkeyFingerprint / Timestamp（如果空）。
func (m *MemoryLedger) Append(ctx context.Context, entry LedgerEntry) (LedgerEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	prev := ZeroRoot
	if len(m.chain) > 0 {
		prev = m.chain[len(m.chain)-1].MerkleRoot
	}
	entry.PrevMerkleRoot = prev
	entry.PubkeyFingerprint = m.signer.Fingerprint()

	eh, err := EntryHash(&entry)
	if err != nil {
		return LedgerEntry{}, err
	}
	entry.MerkleRoot = NextMerkleRoot(prev, eh)

	sig := m.signer.Sign(eh[:])
	entry.Signature = base64.StdEncoding.EncodeToString(sig)

	idx := len(m.chain)
	m.chain = append(m.chain, entry)
	if entry.RequestID != "" {
		m.byReq[entry.RequestID] = idx
	}
	return entry, nil
}

// GetByRequestID 通过 request_id 取出对应 entry。
func (m *MemoryLedger) GetByRequestID(ctx context.Context, requestID string) (LedgerEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	idx, ok := m.byReq[requestID]
	if !ok {
		return LedgerEntry{}, ErrLedgerEntryNotFound
	}
	return m.chain[idx], nil
}

// LatestMerkleRoot 返回当前链尾的 Merkle root。
func (m *MemoryLedger) LatestMerkleRoot(ctx context.Context) ([32]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.chain) == 0 {
		return ZeroRoot, nil
	}
	return m.chain[len(m.chain)-1].MerkleRoot, nil
}

// Size 返回当前 entry 数。
func (m *MemoryLedger) Size(ctx context.Context) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.chain)
}

// Snapshot 返回当前 chain 的深拷贝，verify endpoint / dashboard 用。
func (m *MemoryLedger) Snapshot() []LedgerEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]LedgerEntry, len(m.chain))
	copy(out, m.chain)
	return out
}
