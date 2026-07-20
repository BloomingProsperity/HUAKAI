package auditledger

import (
	"crypto/sha256"
)

// ZeroRoot 是 Merkle 链起点（首条 entry 的 PrevMerkleRoot）。
var ZeroRoot [32]byte

// EntryHash 计算 canonical entry hash，用作签名 payload 以及 Merkle 链的
// 叶子 hash。
//
// 该 payload 即 CanonicalPayload(entry)，遵循
// docs/specs/trust-chain-user-verifiable-ledger.md §2，且不含 Signature。
func EntryHash(e *LedgerEntry) ([32]byte, error) {
	if e == nil {
		return [32]byte{}, nil
	}
	payload, err := canonicalPayload(*e)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(payload), nil
}

// NextMerkleRoot 计算下一条 entry 的 root = sha256(prev || entryHash)。
// Merkle 在这里退化为单向 hash 链（更准确叫 "hash chain"，trust-chain plan
// 称 Merkle 为通俗叫法）；append-only + 公开 root 即可让 user 验证不被改写。
func NextMerkleRoot(prev [32]byte, entryHash [32]byte) [32]byte {
	h := sha256.New()
	h.Write(prev[:])
	h.Write(entryHash[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// VerifyChain 校验给定 entries 列表中每个租户的 Merkle 链完整性：
//   - 每个租户第一条 PrevMerkleRoot 必须等于 ZeroRoot
//   - 每条 MerkleRoot 必须等于 NextMerkleRoot(PrevMerkleRoot, EntryHash(self))
//   - 每条的 PrevMerkleRoot 必须等于同租户前一条的 MerkleRoot
//
// entries 可以是多个租户按写入时间交错排列的全局快照。
func VerifyChain(entries []LedgerEntry) error {
	if len(entries) == 0 {
		return nil
	}
	previousByTenant := make(map[int64][32]byte)
	for i := range entries {
		e := &entries[i]
		prev := previousByTenant[e.TenantID]
		if e.PrevMerkleRoot != prev {
			return &ChainError{Index: i, Reason: "prev_merkle_root_mismatch"}
		}
		eh, err := EntryHash(e)
		if err != nil {
			return &ChainError{Index: i, Reason: "entry_hash_compute_failed", Wrapped: err}
		}
		expected := NextMerkleRoot(prev, eh)
		if e.MerkleRoot != expected {
			return &ChainError{Index: i, Reason: "merkle_root_mismatch"}
		}
		previousByTenant[e.TenantID] = e.MerkleRoot
	}
	return nil
}

// ChainError 是 Merkle 链校验失败的结构化错误，便于 verify endpoint 给 user
// 具体定位"哪条断了"。
type ChainError struct {
	Index   int
	Reason  string
	Wrapped error
}

func (c *ChainError) Error() string {
	if c.Wrapped != nil {
		return "auditledger: chain entry[" + itoa(c.Index) + "] " + c.Reason + ": " + c.Wrapped.Error()
	}
	return "auditledger: chain entry[" + itoa(c.Index) + "] " + c.Reason
}

func (c *ChainError) Unwrap() error { return c.Wrapped }

// itoa 是 strconv.Itoa 的轻量替代，避免本文件引入额外 import。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
