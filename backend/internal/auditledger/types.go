// Package auditledger 实现 HUAKAI 信任链 T4：用户可验证 audit ledger。
//
// 与 internal/auth/audit RefreshAuditEntry 的区别：
//   - internal/auth/audit 是凭据 refresh 子系统专用的小 audit；
//   - 本包是**通用 user-verifiable ledger**：每条记录含 ed25519 签名 + Merkle
//     prev/current root，user 可独立 verify 不依赖 HUAKAI 自报。
//
// 与 internal/audit/admin_audit_events、pool_routing_audit_events 的区别：
//   - 那些表是 **operator-facing** audit（管理员查谁干了啥）；
//   - 本包是 **end-user-facing** ledger（用户验证 HUAKAI 没偷换模型 / 没虚报
//     token / 没伪造 cache hit）。
//
// 与 portkey / litellm / new-api / sub2api 的根本差异：
//   - 4 个 ref 项目全部"信任 operator"，user 拿不到验证数据；
//   - HUAKAI 强制每个 request 末端写一条 LedgerEntry，公开 Merkle root，
//     `huakai-verify` CLI 用 PubkeyFingerprint 索引到公钥独立验签。
//
// 本片仅实现 in-memory ledger + Merkle 计算，DB binding 留 T4.x。

package auditledger

import "github.com/BloomingProsperity/HUAKAI/internal/proto"

// LedgerEntry 是 audit_ledger 表的单行（in-memory 形态）。
// 字段顺序与未来 PostgreSQL 列顺序对齐（T4.x 落库时 schema 直接照搬）。
type LedgerEntry struct {
	// LedgerID 必填；每条记录的稳定 ID；推荐 ULID 字符串（按时间排序）。
	LedgerID string

	// Timestamp 必填；RFC3339Nano UTC。
	Timestamp string

	// RequestID 必填；对应 HCSF RequestMeta.RequestID。
	RequestID string

	// TenantID 可选；0 表示无租户上下文。
	TenantID int64

	// HopChain 必填；通常 6 跳。Marshal 后的 bytes 是签名 input 的一部分。
	HopChain []proto.HopAttestation

	// ModelChain 可选；如果非 nil 必须 Consistent（IsConsistent()=true），
	// 不一致由 settler 拒绝写入并改写 divergence 警告条目。
	ModelChain *proto.ModelChain

	// PrevMerkleRoot 必填；前一条 LedgerEntry 的 MerkleRoot；首条用 zero hash。
	PrevMerkleRoot [32]byte

	// MerkleRoot 必填；本条 entry 的根 = sha256(PrevMerkleRoot || EntryHash)。
	MerkleRoot [32]byte

	// PubkeyFingerprint 必填；签发本条用的公钥指纹（sign.Fingerprint 16 hex）。
	PubkeyFingerprint string

	// Signature 必填；ed25519 over EntryHash；base64-stdlib encoding。
	Signature string
}
