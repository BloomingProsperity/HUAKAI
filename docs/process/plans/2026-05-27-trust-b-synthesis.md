# TRUST-B Synthesis

Lane: claude-pm-synthesis
Time: 2026-05-27T14:15:00Z
Cross-lane:
- [Claude lane](2026-05-27-trust-b-claude.md) (4 切片, 5 D 决策提议)
- [Codex lane](2026-05-27-trust-b-codex.md) (5 切片, 7 D 决策提议, 含 §14 source coverage proof + 真实仓库行号调研)

## §0 Lane 差异概览

| 维度 | Claude lane | Codex lane | Synthesis 选择 |
|---|---|---|---|
| 切片数 | 4 (B-1..B-4, Docs 后续) | 5 (B-1..B-4 + B-5 Docs 同步) | **采 Codex** 5 切片;Docs 与代码并行不积压 |
| 包结构 | 复用 `backend/internal/trust` + 改 frozen `gatewayhttp` 注册路由 | **新 `backend/internal/trustreceipt`** (canonical+payload) + **新 `backend/internal/trusthttp`** (HTTP handler) | **采 Codex** 包结构;冻结 gatewayhttp 不加新文件是 CLAUDE.md #13 硬规则 |
| Key storage | env raw base64 | **file path** `HUAKAI_AUDIT_PRIVATE_KEY_PATH` (现已是) | **采 Codex** — Claude lane 错读现状;file path 已是生产路径 |
| Canonical form | HUAKAI v1 自定义 | HUAKAI v1 (与 `auditledger/canonical.go` 同风格) | **一致** (默认采纳, 不 surface) |
| Receipt 存哪 | 不存,verify 时派生 | **复用 `user_cost_receipts.signed_hash`** (已存在) + payload 从 facts join 派生 | **采 Codex** — Codex 调研发现 user_cost_receipts 已有 signature 字段 |
| Receipt ID | 复用 request_id | **request_id + receipt_sequence** (refund 可多 snapshot) | **采 Codex** — sequence 必要,migration 0033 已建 |
| Pubkey JWK 格式 | HUAKAI 风格直观字段 | **JWK Set-compatible** (`kty:OKP`, `crv:Ed25519`, `kid`, `x`) + HUAKAI 扩展字段 | **采 Codex** — 标准兼容更好;client/JWK library 直接消费 |
| revocation source | DB | config/env overlay | **让 Owner 拍** (D-B-1) |
| operator review queue | 新表 | 复用 audit_ledger_dlq + 加 event_kind | **让 Owner 拍** (D-B-2) |
| client trust anchor | SDK release-time pin | first-use TOFU + cache | **让 Owner 拍** (D-B-3) |
| verify endpoint authn | 公开 IP rate limit | 公开 IP rate limit | **一致** (默认采纳) |
| rate limit profile | 60/min anon | 同 + 10KB body max | **采 Codex** (含 body max) |

## §1 Synthesis 切片清单 (采 Codex 5 切片)

| Slice | 主要文件 / 包 | 工时 | 前置 | Commit |
|---|---|---|---|---|
| TRUST-B-1 canonical contract | 新 `backend/internal/trustreceipt/` (canonical + payload type + tests) | 0.5 天 | TRUST-A commit | 第 1 commit |
| TRUST-B-2 signer integration + receipt 派生 + response inline | 既改 `backend/internal/audit/receipt_worker.go` (settle hook) + 既改 `backend/internal/gatewayhttp/chat_completions_handler_headers.go` (frozen 包既有文件改 ok) + 新 helper in `trustreceipt` | 0.75 天 | B-1 | 第 2 commit |
| TRUST-B-3 final billing detached + db 复用 | 既改 `audit/receipt_worker.go` AppendSettledReceipt + 既改 `auditledger/postgres.go` (复用 signed_hash) | 0.75 天 | B-2 | 第 3 commit |
| TRUST-B-4 pubkey well-known + /v1/trust/verify + CLI | 新 `backend/internal/trusthttp/wellknown_handler.go` + 新 `trusthttp/verify_handler.go` + 既改 `backend/cmd/gateway/routes.go` (mount,允许冻结包既有文件改) + 既改 `backend/cmd/huakai-verify/main.go` (CLI detached mode) | 0.5 天 | B-2 + B-3 | 第 4 commit |
| TRUST-B-5 docs / acceptance / risk register | 既改 `docs/specs/trust-chain-user-verifiable-ledger.md` + `docs/11_ACCEPTANCE_TEST_MATRIX.md` + `docs/10_RISK_REGISTER.md` + 新 `docs/process/reviews/...` | 0.5 天 | B-1..B-4 验收完 | 第 5 commit |

**合计 ~3 天**, 5 commits。每个 commit 含 TDD red→green→R1+R2 review。

## §2 默认采纳 (Owner 不需拍)

| D | 决定 | 理由 |
|---|---|---|
| **D-B-canonical** | HUAKAI v1 fixed-order canonical (与 `auditledger/canonical.go` 同风格) | 两 lane 一致 + 不引第三方 JCS lib + 已有同风格 sample |
| **D-B-key-storage** | file path `HUAKAI_AUDIT_PRIVATE_KEY_PATH` (现已是);Vault/KMS adapter OCAW (Owner Confirmation Approval Window) 待后续 | Codex 调研发现现有 production 路径;不引新依赖 |
| **D-B-receipt-storage** | 复用 `user_cost_receipts.signed_hash` + `signer_fingerprint` 字段;payload 从 `user_cost_receipts` + `audit_ledger_entries` + `usage_records` + `provider_accounts/providers` join 派生 | 严格 D-9=A 不加表/列;Codex 调研发现 user_cost_receipts 已有 signature 字段 |
| **D-B-receipt-id** | `request_id + receipt_sequence` 联合作 lookup id; display-only `receipt_<sha256payload前32字符>` 可选 | migration 0033 已建 sequence;refund/correction 多 snapshot 必要 |
| **D-B-pubkey-format** | JWK Set-compatible (`kty:OKP`, `crv:Ed25519`, `kid:<fingerprint>`, `x:<base64url>`) + HUAKAI 扩展字段 (`status`, `effective_from/to`, `revoked_at`, `reason_class`) | 标准客户端可直接消费;HUAKAI CLI 读扩展字段 |
| **D-B-verify-authn** | `/v1/trust/verify` 公开无 auth;`/v1/audit/verify` 仍 tenant_scope_ref required | trust chain 卖点是 user 独立 verify;商家不能藏 receipt |
| **D-B-rate-limit** | IP-based 60/min anon + body max 10KB + 可选 API key 提到 600/min | DoS 防护 + 公开访问 |
| **D-B-rotation** | 90 天生新 key + 30 天 grace + 灰度 + `effective_to` 列已存在 | migration 0035 已建 |
| **D-B-final-detached-hook** | 复用 `audit/receipt_worker.go` AppendSettledReceipt hook | 现 worker 已存在;不引新触发器 |
| **D-B-streaming-signature** | 流式响应 header 不签 (cost 未知),用户用 `/v1/trust/verify` 或 `/v1/receipts/{id}/verify` detached 拿 final receipt | 流头早出无 cost,无法 inline;现有 user_cost_receipts settle 后写入 |
| **D-B-mismatch-priority** | signature_valid + observed/wire metadata match → verified;signature_valid + mismatch → mismatch;signature_invalid → mismatch | 配 TRUST-A status vocab |

## §3 Owner 已拍 3 个 D 决策 (2026-05-27)

- **D-B-1 = A** 公钥 JSON 里加 revoked 数组 (CRL 思路);Sigstore Fulcio 短证书路径推 Phase 2 Mandatory Roadmap
- **D-B-2 = C** 不存表,只写日志 + Prometheus 告警 (signer down paid request 用日志 + 告警追踪, 不引数据库)
- **D-B-3 = B** TOFU 首次使用缓存 (SSH known_hosts 思路);客户端首次 fetch HTTPS `.well-known` 后本地缓存 fingerprint;轮换时提示用户

(以下原 surface 内容留底,Owner 已答)

### D-B-1 [已选 A]: revocation source

**问题**: signer key 泄露后,怎么发布 revocation 列表给 verify 端?

- **A (config/env overlay)** Claude+Codex 推荐: `.well-known/huakai-pubkey.json` 的 `revoked` 数组从 ops config file / env JSON / 后续 vault KV 读取并合并 pubkey registry 输出。不加 schema。
- **B (DB 新表)** : 加 `audit_signer_revocations` 表 (违 D-9=A 不加表)。
- **C (混合)** : config 当主,DB schema 作为 future migration roadmap (Mandatory Roadmap)。

参考项目:
- Sigstore Rekor 用 config + checkpoint signing,no DB revocation table
- envoy-ai-gateway 无等价 (无 key rotation 概念)
- Helicone 无等价

### D-B-2 [已选 C]: operator_review_queue

**问题**: signer down 时 unverified state paid request 进哪个队列?

- **A (复用 audit_ledger_dlq + event_kind="trust_review")** Codex 推荐: 现有 DLQ infra 加新 event_kind 字段值,不加表。
- **B (新表 operator_review_queue)** Claude 推荐: 字段更专 (request_id, tenant_id, reason, status, assigned_to)。schema gate review (违 D-9=A)。
- **C (config-driven log only)** : 写日志 + Prometheus alert,不存表。

参考项目:
- HUAKAI 已有 `audit_ledger_dlq` (audit_ledger 写失败兜底,Owner 2026-05-22 决策定 [[project_trust_ledger_failclosed_policy]])
- LiteLLM 无等价 (不区分 review state)
- Portkey 无等价

### D-B-3 [已选 B]: client trust anchor

**问题**: 客户端 (SDK / CLI) 怎么 trust HUAKAI 公钥?

- **A (SDK release-time pin)** Claude 推荐: 客户端 SDK 内嵌 trusted fingerprint set,release 更新时同步更新 pin
- **B (TOFU first-use cache)** Codex 推荐: 客户端第一次 fetch `.well-known/huakai-pubkey.json` 并 pin fingerprint 到本地 cache;后续校 fingerprint 不变。如 current key 变更但 old key 仍 present,verify old receipt 用 old key
- **C (HTTPS-only, no pin)** : 完全信赖 TLS,不 pin。简单但 CA 被攻破或 mid-stream proxy 替换 → 失效。

参考项目:
- Sigstore: 用 transparency log (Rekor) + checkpoint signing,无 client-side fingerprint pin (依赖 log 验证)
- WebPKI: 无 pin (TLS 是唯一 root)
- SSH: TOFU + known_hosts 缓存
- HUAKAI: 第一个 release 前没有兼容包袱,可选 A 严格 / B 易部署 / C 信赖 TLS

## §4 参考项目对照 (CLAUDE.md #15)

| 项目 | 暴露方式 | 签名? | HUAKAI 升级 |
|---|---|---|---|
| Sigstore (Rekor) | DSSE envelope + ed25519/ECDSA + Trillian log | ✓ | 架构: 不引 Trillian over-engineer (chat 量级足);算法: ed25519 only |
| Trillian | tile-based log + 公开 root | ✓ | 算法: 简化为 hash chain (Phase 1 lite),Phase 2 接 tile-based |
| LiteLLM | `_hidden_params` cost/model | ✗ | 升: 全 receipt 签 + canonical;LiteLLM 用户 dict 拿不到 verify |
| Helicone | proxy 透传 + log | ✗ | 升: wire signature + audit ledger Merkle |
| Portkey | virtual key + cost/model | ✗ | 同 LiteLLM |
| envoy-ai-gateway | upstream filter + log | ✗ | 同上 |

**HUAKAI fusion-upgrade (CLAUDE.md #12)**: **7 项目无任何项目签 response side**。TRUST 链是 HUAKAI 与所有 AI gateway 的根本差异 ([[project_core_trust_chain_differentiator]])。

三维:
- **架构升级**: 自研 5 状态 vocab + 三 verify endpoint 入口 + `trustreceipt`/`trusthttp` 包分离 + `user_cost_receipts.sequence` 多 snapshot 支持
- **算法升级**: HUAKAI Canonical JSON v1 (fixed-order + integer micro-USD + UTF-8 byte lexicographic) + Merkle hash chain 简化 + 状态升级链 (provisional→signed-only→verified|mismatch)
- **生态升级**: `.well-known/huakai-pubkey.json` JWK 标准 + operator review queue + user-facing CLI verify + rotation grace registry

## §5 与 F-TRUST-001 (Merkle 完整 C) 路径

- TRUST-A + TRUST-B 完成 = F-TRUST-001 Phase 1 lite
- Phase 2 (full Merkle C) Mandatory Roadmap:
  - Trillian-style tile-based log
  - 公开 root checkpoint signing
  - Witness co-signing
  - Inclusion proof + consistency proof
- 不删除现有 F-TRUST-001 编号 / spec
- schema 字段 prev_root/curr_root/merkle_proof 已存在 (migration 0013) Phase 2 启用

## §6 验收 + Release Gate

按 [[feedback_small_closed_increments]] 每切片闭合:
- TDD red → green (单 commit)
- R1 codex review (read-only, severity-based S0/S1 必修)
- R1 fix (如有 S0/S1)
- R2 codex review (R1 fix verify)
- Commit (S0=0 + S1=0)

切片间不滚动 spec drip (CLAUDE.md #8)。

---

Lane: claude-pm-synthesis
Time: 2026-05-27T14:15:00Z UTC
