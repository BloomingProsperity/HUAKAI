# 2026-05-27 TRUST-B Codex Lane Plan

## 0. 元信息

| 字段 | 内容 |
| --- | --- |
| Lane | codex-pm-planner |
| UTC | 2026-05-27 |
| Owner directive | "TRUST-B 是 Phase 1 lite 信任链的签名 + 验证落地" |
| 范围 | 只写 TRUST-B plan, 不写实现代码, 不 commit |
| Parallel-draft 约束 | 独立起草; 未读取任何 `2026-05-27-trust-b-claude.md` 内容 |
| Clean-room lane | specifier/planner: 只输出行为观察和 HUAKAI-fit 计划, 不复制参考项目源码/结构/注释/实现 |
| Freshness | 本地 refs HEAD: LiteLLM 2026-05-19, Helicone 2026-05-18, Portkey Gateway 2026-05-18, Trillian 2026-05-11, Rekor 2026-05-11; 均在 30 天 stale window 内 |

Metadata:

- Observed regions: 41 source/doc line regions, listed in §14.
- Inferences: 18 HUAKAI-fit inferences, marked as "推断".
- Open questions: 7 Owner D decisions in §13.

## §1 切片清单 + 工时 + 依赖 + 风险

| Slice | 文件 / 目标包 | 工时 | 前置依赖 | 风险 |
| --- | --- | --- | --- | --- |
| TRUST-B-1 Lite signed payload canonical contract | 新包 `backend/internal/trustreceipt` 或扩展非冻结 `backend/internal/trust`; 测试同包; 不新增 `gatewayhttp/gateway/proto` 文件 | 0.5 天 | TRUST-A headers/status 已落; 现有 `backend/internal/sign` + `backend/internal/audit` receipt hash 可读 | canonical form drift; price snapshot 若用 float 会跨语言不稳定 |
| TRUST-B-2 Signer integration + receipt 派生 | `backend/internal/audit` 既有 receipt formatter/storage 文件可改; 新 helper 落 `backend/internal/trustreceipt`; `backend/cmd/gateway/middleware.go` wire hook; 不新增 DB 表 | 0.75 天 | B-1 canonical payload; `HUAKAI_AUDIT_PRIVATE_KEY_PATH` signer; `user_cost_receipts` + `audit_ledger_entries` + `usage_records` 可 join | D-9 不加表导致 payload 需从 append-only facts 派生; signer down 不能阻断已交付请求 |
| TRUST-B-3 Public key well-known distribution | 新包 `backend/internal/trusthttp` 放 `.well-known` handler, 或修改既有 `backend/internal/gatewayhttp/audit_pubkey_handler.go`; `backend/cmd/gateway/routes.go` mount | 0.5 天 | B-1 fingerprint schema; `audit_signer_pubkeys` registry 已有 active/historical key | CDN tampering / stale cache / revoked key 表示不足 |
| TRUST-B-4 Detached verify endpoint + CLI mode | 新包 `backend/internal/trusthttp` 放 `/v1/trust/verify`; `backend/cmd/huakai-verify/main.go` 扩展 detached mode; `gatewayhttp` 旧 `/v1/audit/verify` 不破 | 0.5 天 | B-1 canonical parser; B-3 pubkey doc; receipt fact lookup policy | verify endpoint DoS; public lookup 若误用 request_id 会泄露 receipt 存在性 |
| TRUST-B-5 Docs / acceptance tests / release gate | `docs/specs/trust-chain-user-verifiable-ledger.md`, `docs/11_ACCEPTANCE_TEST_MATRIX.md`, `docs/10_RISK_REGISTER.md`, `docs/process/reviews/*` | 后续单独切片 | B-1..B-4 验收结果 | 文档过度承诺 Merkle 完整链; Phase 1 lite 被误读成 Phase 2 |

Package discipline: `backend/internal/gatewayhttp`, `backend/internal/gateway`, `backend/internal/proto` 是冻结包; TRUST-B 实现不得给这些包新增文件。HTTP 新职责优先新建 `backend/internal/trusthttp`; canonical/signature 新职责优先新建 `backend/internal/trustreceipt`。冻结包只允许小范围修改既有文件做 route glue 或兼容。

## §2 ed25519 key lifecycle

启动初始化:

- 现状: gateway 现在从 `HUAKAI_AUDIT_PRIVATE_KEY_PATH` 读取 ed25519 私钥; production 模式缺失该 path 会启动失败, dev 模式才生成 ephemeral key [backend/cmd/gateway/config.go:198](../../../backend/cmd/gateway/config.go:198) [backend/cmd/gateway/config.go:201](../../../backend/cmd/gateway/config.go:201)。
- 现状: `loadAuditSigner` 支持 raw 64-byte, PEM PKCS#8, base64/raw-base64/hex 私钥材料 [backend/cmd/gateway/config.go:220](../../../backend/cmd/gateway/config.go:220) [backend/cmd/gateway/config.go:236](../../../backend/cmd/gateway/config.go:236)。
- 计划: Phase 1 production 默认继续 file secret (`HUAKAI_AUDIT_PRIVATE_KEY_PATH`) + Kubernetes/ops secret mount; env raw key 仅 dev/test。Vault/KMS signer adapter 作为 Owner D-TRUST-B-1, 不在本切片默认引入新 runtime dependency。

`backend/internal/sign/` 现有能力:

- `keygen.go` 只提供 dev/启动期初次生成, 注释已说明 production 应从 KMS/vault/env/file 加载 [backend/internal/sign/keygen.go:9](../../../backend/internal/sign/keygen.go:9)。
- `signer.go` 已有 `Signer.Sign`, `PublicKey`, `Fingerprint`; 指纹是 sha256(pubkey) 前 8 bytes 的 16 hex [backend/internal/sign/signer.go:58](../../../backend/internal/sign/signer.go:58) [backend/internal/sign/signer.go:76](../../../backend/internal/sign/signer.go:76)。
- `verifier.go` 已有公钥长度、签名长度、ed25519 verify 错误分类 [backend/internal/sign/verifier.go:17](../../../backend/internal/sign/verifier.go:17)。
- `auditledger` 另有 KMS-compatible `Signer` interface 和 env/KeyProvider loader, 但 gateway 当前 wire 用的是 `internal/sign.Signer` [backend/internal/auditledger/signer.go:33](../../../backend/internal/auditledger/signer.go:33) [backend/internal/auditledger/signer.go:95](../../../backend/internal/auditledger/signer.go:95)。

生产多实例共享 key:

- 推荐 single active key share: 所有 gateway 实例共享同一 active private key, 启动时注册同一个 public key 到 `audit_signer_pubkeys`。现有 lifecycle 已 `EnsureSignerPubkey` 后再 build ledger [backend/cmd/gateway/lifecycle.go:231](../../../backend/cmd/gateway/lifecycle.go:231)。
- 不推荐 Phase 1 per-instance key: 每实例不同 key 会让同一时间窗口多个 active key 并存, 增加 pubkey cache、revocation、mismatch 排障复杂度。若 Owner 后续要求 per-instance key, 必须把 `instance_id`/`key_id` 纳入 receipt metadata 并增加 rotation/runbook。

90 天 rotation:

- 现状: `audit_signer_pubkeys` 已有 `effective_from/effective_to`, old key 可保留 historical verify [backend/sql/migrations/0035_audit_signer_pubkeys.up.sql:3](../../../backend/sql/migrations/0035_audit_signer_pubkeys.up.sql:3) [backend/sql/migrations/0035_audit_signer_pubkeys.up.sql:24](../../../backend/sql/migrations/0035_audit_signer_pubkeys.up.sql:24)。
- 现状: registry `Rotate` 会关闭旧 active key 的 `effective_to`, 插入新 active key [backend/internal/auditledger/pubkey_registry.go:158](../../../backend/internal/auditledger/pubkey_registry.go:158)。
- 计划: 90 天生成新 key, 灰度部署新 private key 到所有实例, `RotateSignerPubkey` 写 registry; `.well-known` 发布 current + grace + historical。旧 fingerprint 继续 verify 老 receipt; verify 输出 `key_status=rotated` 而不是失败。
- Grace window: active 切换后 30 天内 cache headers 降低到 5 分钟; 30 天后仍列 historical key, 但 status 从 `rotated` 转 `historical`/`revoked` 需要 Owner D-TRUST-B-2 选择是否加显式状态来源。

key 泄露应急:

- 计划发布 revocation list 到 `.well-known/huakai-pubkey.json` 的 `revoked` 数组: `fingerprint`, `revoked_at`, `reason_class`, `affected_after`, `affected_until`。
- 不删除旧公钥; verify 对 revoked key 返回 `signature_valid=true/false` 的 cryptographic 结果, 但 trust status 不得是 `verified`, 必须是 `unverified` 或 `mismatch` + `reason=key_revoked`。
- 无新表默认: revocation list 从 operator config file / env JSON / future vault KV 读取并合并 registry 输出。若 Owner 要 DB 管理 revocation, 触发 schema gate。

## §3 Canonical JSON

推荐: Phase 1 使用 **HUAKAI Trust Receipt Canonical JSON v1**, 不承诺完整 RFC 8785/JCS。原因:

- HUAKAI 已有 `auditledger.canonical.go`: top-level ledger payload 手工固定字段顺序, 嵌套 JSON map key 做 lexicographic sort, array 保持顺序, number 用 `json.Number` 原样输出 [backend/internal/auditledger/canonical.go:37](../../../backend/internal/auditledger/canonical.go:37) [backend/internal/auditledger/canonical.go:100](../../../backend/internal/auditledger/canonical.go:100) [backend/internal/auditledger/canonical.go:146](../../../backend/internal/auditledger/canonical.go:146)。
- 现有 receipt hash 不是完整 JCS; 它先经过 privacy redactor `json.Marshal`, 再 SHA-256 [backend/internal/audit/receipt_formatter.go:388](../../../backend/internal/audit/receipt_formatter.go:388) [backend/internal/privacy/default_redactor.go:47](../../../backend/internal/privacy/default_redactor.go:47)。
- 新增完整 JCS library 可能触发 new dependency 审核; 自写完整 JCS 容易在 Unicode/number normalization 上埋 drift。Phase 1 应冻结一个小而可测的 deterministic profile。

字段排序规则:

- Top-level payload 使用固定顺序, 不依赖 struct reflection: `schema_version`, `receipt_id`, `request_id`, `receipt_sequence`, `tenant_scope_ref`, `occurred_at`, `provider`, `requested_model`, `routed_model`, `upstream_model`, `delivered_model`, `cost_cents`, `token_counts`, `price_snapshot`, `validation_state`, `redacted_metadata_allowlist`。
- Nested object 使用 UTF-8 byte lexicographic key sort; arrays 保持业务顺序。
- Unknown fields 在 verify 时允许存在于 envelope, 但不进入 v1 canonical hash; forward-compat 字段必须进入 `extensions` 且被明确排除或纳入 v2。

数字精度:

- `cost_cents` / token counts 用 signed 64-bit integer; 不允许 JSON float。
- 现有 storage 用 `cost_usd_micros` int64 [backend/sql/migrations/0028_user_cost_receipts.up.sql:11](../../../backend/sql/migrations/0028_user_cost_receipts.up.sql:11), existing receipt payload 用 `cost_total_micro_usd` int64 [backend/internal/audit/receipt_formatter.go:122](../../../backend/internal/audit/receipt_formatter.go:122)。推断: TRUST-B payload 应继续用 integer micro-USD 或 integer cents, 不引入 binary float。
- `price_snapshot` 不放浮点单价; 放 `rate_table_snapshot_id`, `snapshot_version`, `currency_code`, 可选 decimal string。若必须放价格明细, 用 integer micro-unit 或 canonical decimal string, 并加 mutation test: `0.10` vs `0.1` 不能双绿。

Unicode escape:

- Parser 先 decode JSON, canonical writer 再输出 deterministic string; `"\u0061"` 与 `"a"` 必须同 hash。
- 不做 Unicode normalization; provider/model/request_id/metadata keys 限制为 UTF-8 + allowlist safe fields。可见标识符建议 ASCII, 非 ASCII 只允许在 redacted metadata 的 safe value 中出现。
- Go `json.Marshal` 会有自身 escaping 行为; B-1 必须记录 golden bytes, CLI 和 server 共用同一 canonical package, 避免跨包各自 marshal。

## §4 双轨签名时机点

Response inline provisional signature:

- Non-streaming: 在 handler 已拿到 route/provider/model/request_id, ledger result 已知, 且 `WriteHeader` 之前签 provisional payload。现有 `WriteHuakaiHeaders` 就是在写 200 前设置 headers [backend/internal/gatewayhttp/chat_completions_handler_headers.go:56](../../../backend/internal/gatewayhttp/chat_completions_handler_headers.go:56)。
- Streaming: headers 在首 token 前发出, final cost 未知; 因此不能把 final cost signature inline 到 response header。只签 provisional payload: provider/model/request_id/occurred_at/validation_state=`provisional`, cost fields omitted or zero with `provisional=true`。final receipt 通过 detached endpoint/receipt URL 取。
- Header 设置顺序: signature header 必须在 `WriteHeader` 前设置; final billing event signature 不尝试通过 stream trailer 承诺, 因为中间代理和 SDK 对 trailer 支持不稳定。

Final billing event detached signature:

- 触发者: 现有 receipt hook 在 `Settler.Settle` 成功后调用 `AppendSettledReceipt` [backend/internal/audit/receipt_worker.go:99](../../../backend/internal/audit/receipt_worker.go:99), cache hit 也补跑 hook [backend/internal/audit/receipt_worker.go:122](../../../backend/internal/audit/receipt_worker.go:122)。
- 计划: TRUST-B-2 复用这个 hook 作为 final billing event detached signature 的唯一默认触发点。它从 append-only facts 派生 payload, 调 signer, 写 `user_cost_receipts.signed_hash` 和 `signer_fingerprint`。
- 写回数据库: D-9=A 不加新表/新列; final signature 继续写入 `user_cost_receipts.signed_hash`, fingerprint 写 `signer_fingerprint`。payload 本身从 `user_cost_receipts` + `audit_ledger_entries` + `usage_records`/provider join 重新派生。

D-7 default header 主:

- Provisional signature header: `X-Huakai-Trust-Signature`.
- 配套 headers: `X-Huakai-Trust-Payload-SHA256`, `X-Huakai-Trust-Pubkey-Fingerprint`, `X-Huakai-Trust-Schema`, `X-Huakai-Trust-Verify`.
- 长度: ed25519 signature base64 约 88 chars; 加 scheme 前缀仍远低于常见 8KB header 限制。不要把完整 payload 放 header; body extension optional。

## §5 Receipt 派生 + ID 格式

Receipt ID 格式:

- 推荐 Phase 1: 复用 `request_id` + `receipt_sequence` 作为 lookup identity。理由: D-9=A 不加新表, 现有 `user_cost_receipts` 已按 `(tenant_id, request_id, receipt_sequence)` 唯一 [backend/sql/migrations/0033_user_cost_receipts_sequence.up.sql:10](../../../backend/sql/migrations/0033_user_cost_receipts_sequence.up.sql:10)。
- 不推荐 `receipt_<sha256前16字符>` 作为服务器 lookup id: 64-bit truncation 在大规模下 collision 风险不可忽略, 且无新表时无法从 hash 反查 request。
- 可选 display-only `receipt_id`: `receipt_` + SHA-256(canonical payload) 前 32 hex chars; 只用于用户复制/对账, 不作为 D-9 lookup key。若 Owner 要它成为 lookup key, 必须批准新索引/新列或新表。

Receipt payload schema (D-1=B):

```json
{
  "schema_version": "trust.receipt.v1",
  "receipt_id": "request_id:receipt_sequence or display hash",
  "request_id": "req_...",
  "receipt_sequence": 0,
  "tenant_scope_ref": "tenant:...",
  "occurred_at": "2026-05-27T00:00:00Z",
  "provider": "anthropic",
  "requested_model": "claude-...",
  "routed_model": "claude-...",
  "upstream_model": "claude-...",
  "delivered_model": "claude-...",
  "cost_cents": 123,
  "token_counts": {"input": 10, "output": 20, "cached": 0},
  "price_snapshot": {"rate_table_snapshot_id": 9, "snapshot_version": "rate-v3", "currency_code": "USD"},
  "validation_state": "valid",
  "redacted_metadata_allowlist": {"cache_hit": false, "settlement_source": "settle"}
}
```

Receipt 怎么存:

- 复用 `user_cost_receipts` 存 final cost/tokens/signature/fingerprint; 复用 `audit_ledger_entries` 存 provider/model chain/request_id/tenant scope; 复用 `usage_records` 存 requested/upstream model/snapshot_version/provider_account, 再 join `provider_accounts/providers` 得 provider code。
- 不新增 `trust_receipts` 表, 不新增 column。实现上新增 `trustreceipt.DerivePayload` 从这些 facts 生成 canonical payload。
- 风险: 如果将来某字段无法从 append-only facts 稳定派生, 必须停下请 Owner 拍 D-9B schema gate, 不得偷偷缩 payload。

Receipt 与 audit ledger 关系:

- `audit_ledger_entries`: 每 request 一条 ledger entry, request_id UNIQUE [backend/sql/migrations/0013_trust_chain_audit_ledger.up.sql:19](../../../backend/sql/migrations/0013_trust_chain_audit_ledger.up.sql:19)。
- `user_cost_receipts`: 同一 request 可多 snapshot, sequence 0 original, sequence >0 refund/correction [backend/sql/migrations/0033_user_cost_receipts_sequence.up.sql:13](../../../backend/sql/migrations/0033_user_cost_receipts_sequence.up.sql:13)。
- 因此关系是 ledger 1 : receipts N。Final billing event detached signature 对每个 receipt snapshot 各签一次。

## §6 Pubkey 分发 + 兼容

格式:

- 推荐 `.well-known/huakai-pubkey.json` 使用 JWK Set-compatible JSON: `keys[]` 中包含 `kty:"OKP"`, `crv:"Ed25519"`, `kid:<fingerprint>`, `x:<base64url-public-key>`, `alg:"EdDSA"`, `use:"sig"`。
- HUAKAI 扩展字段: `schema_version`, `fingerprint`, `public_key_base64`, `status`, `effective_from`, `effective_to`, `revoked_at`, `reason_class`。JWK 客户端可忽略扩展字段; HUAKAI CLI 可读 fingerprint/status。
- 现有 `/v1/audit/pubkey(s)` 已返回 algorithm/fingerprint/public_key_base64/key_status/effective times [backend/internal/gatewayhttp/audit_pubkey_handler.go:20](../../../backend/internal/gatewayhttp/audit_pubkey_handler.go:20)。

兼容路径:

- `.well-known/huakai-pubkey.json`: canonical public path, no auth。
- `/v1/audit/pubkeys`: 返回同一 key set envelope, 或在兼容期保留 old response shape + 增加 `jwks` 字段。
- `/v1/audit/pubkey` 和 `/v1/audit/pubkey/{fingerprint}`: 保留现有 old single-key response, 避免破坏已存在 CLI/tests。

多 key 列法:

- `current`: exactly one active fingerprint。
- `keys`: active + rotated/historical keys。
- `revoked`: compromised fingerprints; verify 可以 cryptographically verify 但 trust status 不得为 verified。
- 现有 registry 只能区分 `active` 与 `rotated` [backend/internal/auditledger/pubkey_registry.go:407](../../../backend/internal/auditledger/pubkey_registry.go:407); `revoked` 需要 config overlay 或后续 schema。

CDN / caching:

- Headers: `Cache-Control: public, max-age=300, stale-while-revalidate=86400` during normal operation。
- Rotation day / incident: max-age 降到 60 秒; response 包含 `generated_at` 和 `next_rotation_after`。
- 不把 private key fingerprint 以外的 secret 放入 doc; public key doc 可 CDN。

验证端获取:

- CLI 默认 HTTPS fetch well-known; `--pubkey-fingerprint` 必填或从 receipt 取。
- SDK/CLI 第一次 pin fingerprint 到本地 cache; 后续如果 current key 变更但 old key 仍 present, verify old receipt 用 old key; 如果 old key missing, 返回 `unknown_signer`。
- 高安全商户可离线 pin `.well-known` doc checksum; Phase 1 不做 CT mirror。

## §7 Verify endpoint 三入口

`POST /v1/trust/verify` detached:

- 输入: `{payload, canonical_hash?, signature, pubkey_fingerprint, observed_wire_metadata?}`。
- 输出: `{valid, status, signature_valid, key_status, reason, fields_mismatch, canonical_hash, schema_version, receipt_id}`。
- 状态映射: signature invalid -> `mismatch`; signature valid + no server lookup -> `signed-only`; signature valid + observed/fact match -> `verified`; required fields absent -> `missing`; key revoked -> `unverified` with `reason=key_revoked`。
- Authn: public, no session. Body max 10KB, no DB lookup by default, rate limit by IP and optional API key。

`GET/POST /v1/audit/verify` Merkle 旧入口:

- 不破坏现有输入: request_id + tenant_scope_ref; 当前 handler 已要求 tenant_scope_ref [backend/internal/gatewayhttp/audit_verify_handler.go:103](../../../backend/internal/gatewayhttp/audit_verify_handler.go:103)。
- 输出继续是 ledger entry + chain proof; 可加 optional `trust_status` 但不得删 `ledger_entry` / `chain_proof`。
- Authn: 保持当前 public-with-tenant_scope_ref 模式; DoS 防护沿用 4KB body max [backend/internal/gatewayhttp/audit_verify_handler.go:55](../../../backend/internal/gatewayhttp/audit_verify_handler.go:55), 加 per-IP rate limit。

`POST /v1/receipts/{id}/verify` inline-by-receipt:

- 现状: `/v1/receipts/{request_id}/verify` 已存在, 需要 session, 会验证 signature/canonical hash, 并可比对 derived receipt 触发 mismatch refund [backend/internal/gatewayhttp/cost_receipt_handler.go:140](../../../backend/internal/gatewayhttp/cost_receipt_handler.go:140) [backend/internal/gatewayhttp/cost_receipt_handler.go:256](../../../backend/internal/gatewayhttp/cost_receipt_handler.go:256)。
- 计划: 在 D-9=A 下 `{id}` 继续是 request_id; server lookup 模式保持 authn/session, 因为 request_id 不是不可枚举 public receipt id。
- 商家不能藏 receipt 的公开路径由 detached `/v1/trust/verify` 满足: 用户已有 payload+signature 即可公开验证; server-side receipt lookup 公开化需要 Owner 批准 opaque receipt_id。

Rate limit:

- detached verify: strict body max + cheap canonical parse before DB; per-IP token bucket; signature verify CPU budget。
- receipt lookup: session/auth + per-user/per-tenant rate limit。
- audit verify: tenant_scope_ref required; no wildcard list endpoint。

## §8 D-8 fail-open 实施细节

signer 不可用检测:

- 启动: production 缺 `HUAKAI_AUDIT_PRIVATE_KEY_PATH` 仍 fail-fast, 这属于 never configured, 不是 runtime fail-open [backend/cmd/gateway/config.go:201](../../../backend/cmd/gateway/config.go:201)。
- 启动 self-check: load 后对 fixed health payload sign+verify, 并 `EnsureSignerPubkey` 注册公钥; registry 失败 production 可启动失败或降级由 Owner D-TRUST-B-3 拍。
- Runtime probe: 统计 `SignReceipt` error、ledger Append signer error、pubkey registry lookup error、连续失败计数; 超阈值标 `signer_down` 并打开 fail-open。

`unverified` 的 6 个触发点:

1. Provisional response: final cost 尚未 settled, 只能证明 provider/model/request_id。
2. Signer runtime error: sign operation timeout/error, request 已可交付。
3. Ledger append deferred/DLQ: `AuditLedgerResult.State=Deferred`, 没有 final fingerprint [backend/internal/auditledger/result.go:51](../../../backend/internal/auditledger/result.go:51)。
4. Receipt source unavailable: billing/usage 已 pending DLQ, `ErrReceiptUnavailable`。
5. Pubkey registry unavailable: signature存在但无法发布/查到对应公钥。
6. Key revoked / outside effective window: cryptographic signature 可能成立, 但信任状态不能成功。

paid request operator review queue:

- 默认复用 legacy `usage_record_dlq` 作为 operator review queue; 该表已有 `status='operator_review'` 和 `operator_review_at` [backend/internal/dlq/types.go:37](../../../backend/internal/dlq/types.go:37) [backend/internal/dlq/store.go:212](../../../backend/internal/dlq/store.go:212)。
- 不新增表。队列 row 使用 existing `event_kind='post_delivery_settlement'` 或 `audit_ledger_entry` 需 Owner D-TRUST-B-4 拍; 若不拍, 只允许把 ledger append/sign failure 放 `audit_ledger_entry`, receipt-only signer failure 先 structured ERROR + admin alert, 不伪装成 ledger replay。
- 消费者: 现有 admin DLQ/operator review 页面 + DLQ worker。若要自动重签 missing receipt, 需要新增 handler 但不新增 table; event kind 若新增是 schema high-risk, 必须 Owner confirm。

signer down vs never configured:

- never configured: production 启动失败; dev/test ephemeral key 会明确 `key_status=ephemeral_dev`/warning, 不标 verified。
- signer down: 启动曾成功且 fingerprint 已注册, runtime sign/probe 失败; request fail-open, response status `unverified`, paid request review queue。

## §9 Mismatch detection 时机

TRUST-A 已在 response writer 比对 meta vs ledger result; `ResponseStatus` 对 persisted ledger mismatch 返回 `mismatch` [backend/internal/trust/status.go:79](../../../backend/internal/trust/status.go:79)。

TRUST-B 后新增三层 mismatch:

- Wire header vs signed payload: 商家 fake provider/model header, 但 payload signature valid。`/v1/trust/verify` 若提交 `observed_wire_metadata`, 必须返回 `status=mismatch`, `signature_valid=true`, `fields_mismatch=["provider"]`。签名通过不等于 verified。
- Signed payload vs stored facts: `/v1/receipts/{id}/verify` 内部 derive stored payload 后比对; 当前 receipt verify 已有 `FieldsMismatch` 输出 [backend/internal/gatewayhttp/cost_receipt_handler.go:80](../../../backend/internal/gatewayhttp/cost_receipt_handler.go:80)。
- Receipt payload vs model-chain verdict: 如果 ledger/model-chain verdict 是 mismatch/unknown, 即使 receipt signature valid, trust status 不得升到 verified。

verify 通过但 meta mismatch:

- 输出 `signature_valid=true`, `valid=false`, `status=mismatch`。
- `verified` 只表示 signature valid + key acceptable + required fields present + observed/fact match。

D-4 红 badge 新场景:

- `signature_mismatch`, `canonical_hash_mismatch`, `wire_payload_mismatch`, `fact_payload_mismatch`, `key_revoked`。
- `signed-only` 不红但 warning; `unverified` 黄色/灰 warning; `mismatch` 和 `missing` 红。

## §10 Test 策略 (mutation 自检)

TRUST-B-1 canonical contract:

- Mutation: canonical writer 改为 input order 不排序。Test: 同 payload 两种 field order hash 必须相同, mutation 后红。
- Mutation: `price_snapshot` 接受 float。Test: payload 中浮点 price 返回 `invalid_canonical_number`, mutation 后误绿会被抓。
- Mutation: Unicode escape 不 decode。Test: `"\u0061"` 和 `"a"` canonical hash 相同, mutation 后红。

TRUST-B-2 signer integration + receipt 派生:

- Mutation: final payload 不含 provider。Test: 改 provider 后 verify 必须 `mismatch`, mutation 后会误绿。
- Mutation: signer key 错。Test: 用 key A 签, key B fingerprint verify 必须 `unknown_signer`/invalid, mutation 后红。
- Mutation: replay guard 字段撤掉 (`receipt_sequence` 或 `occurred_at`)。Test: sequence 0 signature 不能验证 sequence 1/refund receipt, mutation 后红。

TRUST-B-3 pubkey distribution:

- Mutation: `.well-known` 只返回 current key。Test: rotated old receipt 仍能按 old fingerprint 找 key; mutation 后 404/unknown。
- Mutation: fingerprint 不校验 public key。Test: doc 中 fingerprint 与 public key 不匹配必须拒绝; mutation 后红。
- Mutation: cache header 过长。Test: incident/revoked doc max-age <= 60; mutation 后红。

TRUST-B-4 verify endpoint + CLI:

- Mutation: signature_valid 直接等于 status verified。Test: signature valid but observed provider mismatch -> `status=mismatch`, mutation 后红。
- Mutation: fail-open 错变 fail-closed。Test: signer runtime error path response 200 + `unverified` + review enqueue; mutation 后 503 红。
- Mutation: body limit 删除。Test: >10KB detached verify body 返回 413; mutation 后红。

TRUST-B-5 docs / acceptance:

- Mutation: docs 把 Phase 1 lite 写成 Merkle 完整链。Test/grep gate: B docs 必须包含 "Merkle Phase 2/C Mandatory Roadmap"。
- Mutation: risk register 缺 key compromise。Test/grep gate: `docs/10_RISK_REGISTER.md` 必须有 TRUST-B key compromise/revocation risk。
- Mutation: acceptance matrix 只测 happy path。Test/grep gate: AT rows 必须包含 invalid signature、rotated key、signer down fail-open。

## §11 参考项目对照 (CLAUDE.md #15)

Clean-room note: 下表只记录已观察行为, 不复制上游代码、结构、命名作为实现来源。

| Slice | 项目 | 暴露方式 | 签名? | HUAKAI 升级 |
| --- | --- | --- | --- | --- |
| B-1 | LiteLLM | 已观察 routing/header/logging 区域维护 provider/model/cost-like metadata: `~/refs/litellm/litellm/router.py:8643`, `~/refs/litellm/litellm/router.py:8951`, `~/refs/litellm/litellm/types/utils.py:2662` | 已读区域未观察到 detached ed25519 receipt | HUAKAI 把 provider/model/cost/token/price snapshot 变成 canonical signed payload |
| B-1 | Portkey Gateway | 已观察 provider/header metadata 是 request config 一部分: `~/refs/portkey-gateway/src/globals.ts:13`, `~/refs/portkey-gateway/src/handlers/chatCompletionsHandler.ts:19` | 已读区域未观察到用户 receipt 签名 | HUAKAI 不停在 routing header, 而是签 final billing facts |
| B-2 | Helicone AI Gateway | 已观察 gateway response headers 可写 request id/provider/model: `~/refs/helicone/worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts:151`; UI table 展示 provider: `~/refs/helicone/web/components/templates/requests/initialColumns.tsx:90` | 已读区域未观察到 detached user receipt | HUAKAI 把可见 provider/model 与签名、fact-match 状态绑定 |
| B-2 | Portkey Gateway | 已观察 log object 收集 provider request context 和 transformed request metadata: `~/refs/portkey-gateway/src/handlers/services/logsService.ts:165` | 已读区域未观察到 user-verifiable signature | HUAKAI 避免 operator-only log, 输出用户可验证 receipt |
| B-3 | Rekor | 已观察 server 支持多 signer backend 选项, memory/file 仅测试: `~/refs/rekor/cmd/rekor-server/app/root.go:106` | 透明日志 checkpoint signing, 非 AI receipt | HUAKAI pubkey distribution 支持 file secret phase + future KMS/vault |
| B-3 | Rekor | 已观察 client/server 公开 log endpoint 与 upload artifact/public key workflow: `~/refs/rekor/cmd/rekor-server/e2e_test.go:56`, `~/refs/rekor/pkg/client/rekor_client.go:65` | 公开验证基础设施 | HUAKAI `.well-known` 公开 key + fingerprint pin, 但不复制 Rekor API |
| B-4 | Trillian | 已观察 client 等待 inclusion proof 并验证 trusted root: `~/refs/trillian/client/log_client.go:226`, `~/refs/trillian/client/log_client.go:270` | Merkle proof verify, 非 receipt detached verify | HUAKAI B-4 做 lite detached verify; Merkle inclusion 保 Phase 2 |
| B-4 | Trillian | 已观察 verifier 校验 consistency/inclusion proof: `~/refs/trillian/client/log_verifier.go:55`, `~/refs/trillian/client/log_verifier.go:80` | 验证链完整性 | HUAKAI `/v1/audit/verify` 不破, `/v1/trust/verify` 专注签名/fact mismatch |
| B-5 | Trillian | 已观察 client API 区分 wait/proof/verify steps: `~/refs/trillian/client/log_client.go:184`, `~/refs/trillian/client/log_verifier.go:55` | Merkle verification docs must not be conflated with lite receipt signing | HUAKAI docs 明确 Phase 1 signed receipt 与 Phase 2 Merkle inclusion 的边界 |
| B-5 | Rekor | 已观察 e2e workflow 将 upload artifact/public key 与 verify/read path 分开: `~/refs/rekor/cmd/rekor-server/e2e_test.go:56`, `~/refs/rekor/pkg/client/rekor_client.go:65` | 公开验证 workflow, 非 HUAKAI billing receipt | HUAKAI acceptance docs 要分别覆盖 detached verify、pubkey fetch、signer-down fail-open |

## §12 Risk 登记

| Slice | Risk | Severity | Mitigation |
| --- | --- | --- | --- |
| B-1 | canonical form drift: Go server、CLI、第三方 SDK hash 不同 | HIGH | 单一 `trustreceipt` canonical package; golden bytes; 禁止 float; docs 写 exact ordering |
| B-1 | replay attack: 旧 signature 套新 receipt | HIGH | canonical payload 必含 request_id + tenant_scope_ref + receipt_sequence + occurred_at + cost/token facts |
| B-2 | key compromise | HIGH | revocation list + key_status 明示 + no verified on revoked; 90-day rotation; incident max-age 降低 |
| B-2 | signer down 造成 paid request 无 final signature | HIGH | fail-open + `unverified` + operator review queue; never configured production fail-fast |
| B-3 | pubkey CDN tampering/stale | MED | HTTPS + fingerprint pin + low max-age + key doc self-consistency tests |
| B-3 | rotated key 被隐藏导致老 receipt 无法 verify | HIGH | key set 必列 active+rotated; mutation test 删除 old key 必红 |
| B-4 | verify endpoint DoS | MED | body max, rate limit, parse-before-DB, no default fact lookup |
| B-4 | mismatch false positive | MED | response status 分离 `signature_valid` 与 `status`; fact lookup 使用 append-only sources; test exact fields_mismatch |
| B-5 | Phase 1 lite 被误当完整 Merkle 不可删改证明 | MED | docs 明确 Merkle Phase 2/C Mandatory Roadmap; UI copy 区分 signed-only/verified |

## §13 Owner D 决策点 (本切片专属)

D-TRUST-B-1 key storage backend:

- A 推荐: Phase 1 production 用 mounted file secret (`HUAKAI_AUDIT_PRIVATE_KEY_PATH`), Vault/KMS adapter Mandatory Roadmap。
- B: 现在接 Vault/KMS signer adapter。
- 需 Owner 拍: 是否接受 A 作为 Phase 1 production default。

D-TRUST-B-2 canonical standard:

- A 推荐: HUAKAI Trust Receipt Canonical JSON v1 custom deterministic profile。
- B: 完整 RFC 8785/JCS。
- 需 Owner 拍: 是否为了第三方 SDK 标准化承担 JCS 实现/依赖风险。

D-TRUST-B-3 receipt lookup ID:

- A 推荐: D-9 下复用 request_id + receipt_sequence; detached verify public, server lookup authn。
- B: 新 opaque `receipt_<hash>` 可 public lookup, 但需要 schema/index。
- 需 Owner 拍: 是否接受 request_id lookup 的 authn 边界。

D-TRUST-B-4 operator review queue event kind:

- A: 只复用 existing `audit_ledger_entry` / `post_delivery_settlement` 能表达的场景; receipt-only signer failure 先 alert + manual review。
- B: 新增 `trust_receipt_review` event kind 到 `usage_record_dlq` CHECK, 需要 schema gate。
- 需 Owner 拍: paid request 无 final receipt signature 是否必须有专用 event kind。

D-TRUST-B-5 pubkey rotation cycle:

- A 推荐: 90 天 active rotation + 30 天 low-cache grace + historical keys indefinite。
- B: 180/365 天降低 ops 频率。
- 需 Owner 拍: commercial trust posture vs ops burden。

D-TRUST-B-6 verify endpoint authn:

- A 推荐: `/v1/trust/verify` public detached; `/v1/audit/verify` public with tenant_scope_ref; `/v1/receipts/{id}/verify` authn until opaque receipt id exists。
- B: 三入口全 public。
- 需 Owner 拍: receipt-by-id public lookup 是否允许暴露 request existence。

D-TRUST-B-7 pubkey revocation source:

- A 推荐: Phase 1 config overlay (`HUAKAI_AUDIT_REVOKED_PUBKEYS_JSON`) 合并 registry。
- B: 新 DB-managed revocation table。
- 需 Owner 拍: incident response 是否需要 DB UI immediately。

## §14 Source Coverage Proof

HUAKAI regions read:

- `docs/process/plans/2026-05-27-trust-chain-ab-codex.md:1-260` contributed old lane format, TRUST-B gaps, prior D decisions.
- `docs/process/plans/2026-05-27-trust-chain-ab-synthesis.md:1-88` contributed synthesis slices and Owner decisions.
- `docs/specs/trust-chain-user-verifiable-ledger.md:1-247` contributed F-TRUST-001 full-chain target, canonical fields, pubkey rotation and risk boundaries.
- `backend/internal/sign/keygen.go:1-18`, `signer.go:1-83`, `verifier.go:1-30` contributed ed25519 key/sign/verify facts.
- `backend/cmd/gateway/config.go:198-247`, `lifecycle.go:231-248`, `middleware.go:124-164`, `routes.go:38-73` contributed current signer load, pubkey registry, receipt hook, routes.
- `backend/internal/auditledger/canonical.go:1-181`, `types.go:1-62`, `result.go:1-129`, `pubkey_registry.go:1-430`, `signer.go:1-430`, `dlq_producer.go:1-41`, `dlq_worker.go:1-68` contributed ledger canonical/signature/pubkey/DLQ facts.
- `backend/internal/audit/receipt_formatter.go:1-520`, `receipt_storage.go:1-220`, `receipt_worker.go:1-145`, `refund_worker.go:1-140` contributed receipt schema, derivation, signing, storage, mismatch queue.
- `backend/internal/gatewayhttp/cost_receipt_handler.go:1-560`, `audit_pubkey_handler.go:1-145`, `audit_verify_handler.go:1-340`, `chat_completions_handler_headers.go:1-260` contributed current HTTP behavior.
- `backend/internal/trust/status.go:1-121` contributed TRUST-A status/mismatch behavior.
- `backend/internal/privacy/default_redactor.go:47-285`, `redactor.go:1-51`, `types.go:1-20` contributed redaction/canonical payload safety.
- `backend/internal/dlq/types.go:1-127`, `store.go:1-360` and migrations `0013`, `0026`, `0028`, `0033`, `0035`, `0052`, `0053` contributed existing tables and queue constraints.

Reference source regions read:

- `/home/codex/refs/litellm/litellm/router.py:8610-8665`, `:8935-8985`; `/home/codex/refs/litellm/litellm/types/utils.py:2648-2685`.
- `/home/codex/refs/helicone/worker/src/routers/gatewayRouter.ts:118-155`; `/home/codex/refs/helicone/worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts:140-175`; `/home/codex/refs/helicone/web/components/templates/requests/initialColumns.tsx:82-105`.
- `/home/codex/refs/portkey-gateway/src/globals.ts:1-28`; `/home/codex/refs/portkey-gateway/src/handlers/chatCompletionsHandler.ts:15-58`; `/home/codex/refs/portkey-gateway/src/handlers/services/logsService.ts:160-190`.
- `/home/codex/refs/trillian/client/log_client.go:60-90`, `:184-290`; `/home/codex/refs/trillian/client/log_verifier.go:1-150`.
- `/home/codex/refs/rekor/cmd/rekor-server/app/root.go:58-118`; `/home/codex/refs/rekor/cmd/rekor-server/e2e_test.go:48-80`; `/home/codex/refs/rekor/pkg/client/rekor_client.go:60-90`.

Source files read: docs/process/plans/2026-05-27-trust-chain-ab-codex.md; docs/process/plans/2026-05-27-trust-chain-ab-synthesis.md; docs/specs/trust-chain-user-verifiable-ledger.md; backend/internal/sign/keygen.go; backend/internal/sign/signer.go; backend/internal/sign/verifier.go; backend/cmd/gateway/config.go; backend/cmd/gateway/lifecycle.go; backend/cmd/gateway/middleware.go; backend/cmd/gateway/routes.go; backend/internal/auditledger/canonical.go; backend/internal/auditledger/types.go; backend/internal/auditledger/result.go; backend/internal/auditledger/pubkey_registry.go; backend/internal/auditledger/signer.go; backend/internal/auditledger/dlq_producer.go; backend/internal/auditledger/dlq_worker.go; backend/internal/audit/receipt_formatter.go; backend/internal/audit/receipt_storage.go; backend/internal/audit/receipt_worker.go; backend/internal/audit/refund_worker.go; backend/internal/gatewayhttp/cost_receipt_handler.go; backend/internal/gatewayhttp/audit_pubkey_handler.go; backend/internal/gatewayhttp/audit_verify_handler.go; backend/internal/gatewayhttp/chat_completions_handler_headers.go; backend/internal/trust/status.go; backend/internal/privacy/default_redactor.go; backend/internal/privacy/redactor.go; backend/internal/privacy/types.go; backend/internal/dlq/types.go; backend/internal/dlq/store.go; backend/sql/migrations/0013_trust_chain_audit_ledger.up.sql; backend/sql/migrations/0026_obs_dlq.up.sql; backend/sql/migrations/0028_user_cost_receipts.up.sql; backend/sql/migrations/0033_user_cost_receipts_sequence.up.sql; backend/sql/migrations/0035_audit_signer_pubkeys.up.sql; backend/sql/migrations/0052_user_cost_receipt_owners.up.sql; backend/sql/migrations/0053_post_delivery_settlement_dlq_kind.up.sql; /home/codex/refs/litellm/litellm/router.py; /home/codex/refs/litellm/litellm/types/utils.py; /home/codex/refs/helicone/worker/src/routers/gatewayRouter.ts; /home/codex/refs/helicone/worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts; /home/codex/refs/helicone/web/components/templates/requests/initialColumns.tsx; /home/codex/refs/portkey-gateway/src/globals.ts; /home/codex/refs/portkey-gateway/src/handlers/chatCompletionsHandler.ts; /home/codex/refs/portkey-gateway/src/handlers/services/logsService.ts; /home/codex/refs/trillian/client/log_client.go; /home/codex/refs/trillian/client/log_verifier.go; /home/codex/refs/rekor/cmd/rekor-server/app/root.go; /home/codex/refs/rekor/cmd/rekor-server/e2e_test.go; /home/codex/refs/rekor/pkg/client/rekor_client.go
Lane: specifier/planner
Agent: GPT-5 Codex lane planner
UTC timestamp: 2026-05-27T00:00:00Z

中文摘要: 本 lane 的真观察是: HUAKAI 已有 ed25519 signer、audit ledger canonical/signature、pubkey registry、receipt hook、receipt verify 和 TRUST-A 五态 headers, 但 final TRUST-B payload 还没有把 provider/model/request_id/cost/token/price snapshot/validation/redacted metadata 绑定成同一个 canonical signed receipt。合理推断是: TRUST-B 应新建 `trustreceipt`/`trusthttp` 边界, 避免冻结包继续膨胀, 并在 D-9=A 下从现有 append-only facts 派生 payload 而不是新建表。Open questions 共 7 个, 最高优先级是 key storage、canonical standard、receipt lookup authn 和 operator review queue event kind; 本计划没有功能缩水, Merkle 完整链继续作为 Phase 2/C Mandatory Roadmap。
