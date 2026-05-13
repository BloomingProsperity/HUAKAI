package auditledger

import (
	"crypto/sha256"
	"encoding/json"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// ZeroRoot 是 Merkle 链起点（首条 entry 的 PrevMerkleRoot）。
var ZeroRoot [32]byte

// EntryHash 计算单条 LedgerEntry 的 hash，作为签名 input 和 Merkle 链 hash 节点。
//
// hash input 拼接顺序（注意：bytewise，禁止任何 user prompt/completion 内容
// 进入此 hash）：
//
//	sha256( ledger_id || ts || request_id || tenant_id_be || canonical_json(hop_chain)
//	       || canonical_json(model_chain) || pubkey_fp )
//
// canonical_json 用 encoding/json 默认 marshal；HopChain / ModelChain 字段
// 均不含 prompt/completion，已通过 T0 redact allowlist 守门。
func EntryHash(e *LedgerEntry) ([32]byte, error) {
	if e == nil {
		return [32]byte{}, nil
	}
	h := sha256.New()
	h.Write([]byte(e.LedgerID))
	h.Write([]byte{0x1f}) // unit separator
	h.Write([]byte(e.Timestamp))
	h.Write([]byte{0x1f})
	h.Write([]byte(e.RequestID))
	h.Write([]byte{0x1f})
	var tidBytes [8]byte
	encInt64BE(tidBytes[:], e.TenantID)
	h.Write(tidBytes[:])
	h.Write([]byte{0x1f})
	hopBytes, err := json.Marshal(e.HopChain)
	if err != nil {
		return [32]byte{}, err
	}
	h.Write(hopBytes)
	h.Write([]byte{0x1f})
	mcBytes, err := json.Marshal(e.ModelChain)
	if err != nil {
		return [32]byte{}, err
	}
	h.Write(mcBytes)
	h.Write([]byte{0x1f})
	h.Write([]byte(e.PubkeyFingerprint))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
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

// VerifyChain 校验给定 entries 列表的 Merkle 链完整性：
//   - 第一条 PrevMerkleRoot 必须等于 ZeroRoot
//   - 每条 MerkleRoot 必须等于 NextMerkleRoot(PrevMerkleRoot, EntryHash(self))
//   - 每条的 PrevMerkleRoot 必须等于前一条的 MerkleRoot
func VerifyChain(entries []LedgerEntry) error {
	if len(entries) == 0 {
		return nil
	}
	prev := ZeroRoot
	for i := range entries {
		e := &entries[i]
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
		prev = e.MerkleRoot
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

// encInt64BE 把 int64 写入 8 bytes big-endian；不引入 binary 包以保持本文件 lean。
func encInt64BE(dst []byte, v int64) {
	u := uint64(v)
	dst[0] = byte(u >> 56)
	dst[1] = byte(u >> 48)
	dst[2] = byte(u >> 40)
	dst[3] = byte(u >> 32)
	dst[4] = byte(u >> 24)
	dst[5] = byte(u >> 16)
	dst[6] = byte(u >> 8)
	dst[7] = byte(u)
}

// 为防 unused import 警告：proto 包通过 LedgerEntry.HopChain/ModelChain 间接引用。
var _ = (*proto.HopAttestation)(nil)
