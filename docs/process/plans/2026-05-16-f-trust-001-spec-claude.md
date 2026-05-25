# Trust Chain User-Verifiable Ledger — F-TRUST-001 Spec

| 字段 | 值 |
|---|---|
| Feature ID | F-TRUST-001 trust chain user-verifiable ledger (Phase 6 commercial foundation, HUAKAI 核心差异化) |
| Lane | Claude PM-Orchestrator + spec writer (HUAKAI 6 大核心差异化卖点之一, 写 spec 不涉反代敏感) |
| Base | commit 0013_trust_chain_audit_ledger migration (已存在 audit_ledger_entries 表 + hop_chain/model_chain/merkle_root/signature schema) + memory `project_core_trust_chain_differentiator` |
| Phase | TRUST-1 (已部分实施 — schema + Go writer pipeline, 本 spec 补 user-facing verify API + transparency dashboard + cross-chain reference 跟 F-CH-002/F-FP-001/F-PACE-001/F-NET-001/F-ADV-001 audit 关联) |
| Memory ref | [[project_core_trust_chain_differentiator]] [[feedback_huakai_better_than_sub2api]] [[feedback_stability_means_stronger]] |
| Scope | F-TRUST-001 用户可验证信任链 — every gateway request 产生 1 条 ledger entry, end user 可拿 request_id 验证 (a) request 走的 hop chain (b) 实际用的 model (c) HUAKAI 没掺水 / 没偷换 / 没用别 vendor 充数 |
| Out of scope | operator-facing internal audit (跟 F-AUDIT-001 区分); 隐私脱敏 (跟 F-PRIV-001 区分); 反代敏感层 audit (各层自身 audit table, 不写本 ledger); 计费 ledger (跟 F-BILL-001 区分) |
| UTC | 2026-05-16T09:45:00Z |

## 1. 问题陈述

所有现有 AI gateway (sub2api / new-api / litellm / portkey / helicone / one-api) 都是 **operator 单方信任模型**: 用户必须信任 gateway operator 没动手脚 (没掺水 / 没换模型 / 没用便宜模型充贵的 / 没用免费 tier 卖付费). **用户无法验证**.

HUAKAI 核心差异化 (memory `project_core_trust_chain_differentiator` 6 要求):
1. **链路公开** — 每 request 的 routing 路径用户可见
2. **无用户数据日志** — gateway 不存 user prompt/completion text
3. **模型校验用户可见** — 用户能验证实际用了什么模型 (跟 requested 不一致也透明)
4. **商家不能做假** — operator 无法事后篡改 ledger
5. **日志只系统报错** — 系统级 error log 跟 user data log 严格分离
6. **用户消费透明** — 用户能验证每 token 计费跟实际 upstream 报告一致

F-TRUST-001 = 实现 1 + 3 + 4 (链路公开 + 模型校验 + 商家不能做假). 跟 F-PRIV-001 (2 + 5 无用户数据) + F-AUDIT-001 (6 消费透明) 共同形成 6 要求闭环.

## 2. 信任链结构

每条 ledger entry (audit_ledger_entries row, schema 已落) 含:

| 字段 | 含义 | 用户验证用途 |
|---|---|---|
| `ledger_id` | 全局唯一 ledger 标识符 (text UNIQUE) | 用户引用 ledger entry 的 stable handle |
| `request_id` | 用户 request 的 UUID (UNIQUE) | 用户从自己 client log 拿这个 ID 查 ledger |
| `tenant_id` | 用户所属 tenant (FK, DR-001) | RLS 隔离, 用户只能查自己 tenant 的 entry |
| `occurred_at` | UTC 时间戳 | 验证时间一致 |
| `hop_chain` | JSON array of HopAttestation — 1 hop = 1 处理步骤 (router decision / pool selection / executor call / upstream forward / response normalize) | 用户看到完整路径, 验证 router 没绕弯 |
| `model_chain` | JSON object: `{requested, route_decided, upstream_reported}` | **模型校验**: 用户验证 requested (e.g. claude-opus-4-7) = route_decided = upstream_reported. 任一不一致 = 透明 substitution |
| `prev_merkle_root` | sha256[:32] 上条 entry 的 merkle root (bytea, 32) | merkle chain — 前一条 root 锁住, operator 无法插入或修改 |
| `merkle_root` | sha256[:32] 本条 entry merkle root = sha256(prev_merkle_root || entry_hash) (bytea, 32) | **append-only Merkle tree**: tampering 立刻 detect |
| `pubkey_fingerprint` | sha256(pubkey)[:8] hex (16 chars) | 用户从 `/.well-known/huakai-pubkey.json` 拿 pubkey + fingerprint match |
| `signature` | base64 ed25519 sig over entry_hash (64-byte sig → 88-char base64) | **operator 不能做假**: ed25519 私钥签名, 没有私钥写不出 valid signature |

## 3. HopAttestation Schema (hop_chain JSON array)

每个 hop entry:
```json
{
  "hop_index": 0,
  "hop_class": "router_decision" | "pool_select" | "credential_select" | "upstream_forward" | "response_normalize" | "channel_health_check",
  "decision_summary": "<short enum or redacted summary>",
  "duration_ms": 12,
  "feature_refs": ["F-POOL-001", "F-CH-002"],
  "alt_event_id": "<optional ref to per-layer audit table row>"
}
```

`decision_summary` 严禁含 user prompt / response / token / cookie / credential bytes. 只允许:
- enum 值 (e.g. "selected_account_id=1234", "channel_id=5678")
- 短 reason class (e.g. "lowest_latency", "ramp_sample")
- redacted ref (e.g. `alt_event_id` 指向 active_detection_events / pacing_session_traces 表 row, 但不 inline content)

典型 6 hop (1 normal request):
1. `router_decision`: requested model → matched route policy
2. `pool_select`: pool 选 account_id
3. `credential_select`: credential version
4. `upstream_forward`: upstream URL + 实际收到 status code
5. `response_normalize`: normalize 后 model_chain 填充
6. `channel_health_check`: F-CH-002 signal emit (alt_event_id = channel_health_audit_events.id)

如 hop > 6 (例 ramp + retry), 全 hop 都进 hop_chain.

## 4. Model Chain Validation

`model_chain` JSON object:
```json
{
  "requested": "claude-opus-4-7",
  "route_decided": "claude-opus-4-7",
  "upstream_reported": "claude-opus-4-7-20251001"
}
```

3 字段必须每条 entry 都填 (streaming-in-flight 时 `model_chain` 可为 NULL, 但 stream 关闭时必须 update fill).

**Substitution policy**:
- `requested == route_decided`: HUAKAI 没换模型 (绿)
- `requested != route_decided`: HUAKAI 做 substitution (黄), 必须 client 收到 `X-Huakai-Model-Substituted` header (per F-MODEL-SUBSTITUTION-001)
- `route_decided != upstream_reported`: upstream 报了不同 model ID (e.g. point-in-time version), 应跟 `route_decided` semantic-compatible. 不一致 user 可投诉 (admin dashboard 显示 mismatch 比例)

## 5. User Verification API

新 endpoint:

`GET /v1/ledger/entries/{request_id}` (tenant-scoped, requires session middleware F-SESSION-001):
- Response: full audit_ledger_entries row + verify hint
- 用户可 client-side 用 ed25519 公钥 verify signature

`GET /v1/.well-known/huakai-pubkey.json` (public, no auth):
- Response: `{pubkeys: [{fingerprint, pubkey_base64, valid_from, valid_until}, ...]}`
- 多 pubkey 支持 rotation; valid_until 过期后用户必须升级

`GET /v1/ledger/chain-head` (tenant-scoped):
- Response: `{last_ledger_id, last_merkle_root, occurred_at, total_entries}`
- 用户 daily snapshot 验证 chain 完整 (deep verify: 历史所有 entry merkle 链 recompute)

`GET /v1/ledger/verify/{request_id}/proof` (tenant-scoped):
- Response: `{entry, prev_merkle_root, sibling_hash_path[]}`
- Sparse Merkle proof: 用户验证单 entry 在 chain 内 (不需要下载整 chain)

## 6. Append-Only Enforcement

Migration 0013 schema 已含 INSERT-only design (no UPDATE no DELETE in writer code). 加强:

```sql
-- 新增 trigger 防 UPDATE / DELETE (即便 admin 误操作)
CREATE OR REPLACE FUNCTION enforce_ledger_append_only() RETURNS TRIGGER AS $$
BEGIN
  RAISE EXCEPTION 'audit_ledger_entries is append-only: %', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER ledger_append_only_update BEFORE UPDATE ON audit_ledger_entries
  FOR EACH ROW EXECUTE FUNCTION enforce_ledger_append_only();
CREATE TRIGGER ledger_append_only_delete BEFORE DELETE ON audit_ledger_entries
  FOR EACH ROW EXECUTE FUNCTION enforce_ledger_append_only();
```

写入只走 **ledger_writer** goroutine (单线程串行, 防 chain 断裂); 失败 retry 但绝不 INSERT 跳 sequence (merkle 必须连续).

## 7. Cross-Chain Reference

各反代 audit 表跟 F-TRUST ledger 关系:

| 反代层 audit 表 | 是否进 0013 ledger | 关系 |
|---|---|---|
| `active_detection_events` (F-ADV-001) | 不进 | system-level audit, 不是 per-request user-verifiable. 但 detection 触发的 channel cooldown 影响某 user request 时, 该 request 的 hop_chain 含 `channel_switched_due_to_detection_class` attestation |
| `device_fingerprint_bindings` (F-FP-001) | 不进 | per-account state, 不是 per-request. user request 用了某 fingerprint binding 时, hop_chain 含 `fingerprint_profile_id` ref (不暴露 fingerprint detail) |
| `channel_health_audit_events` (F-CH-002) | 不进 | per-channel state change, 不是 per-request. hop_chain 含 `channel_id` + `channel_status` ref |
| `pacing_session_traces` (F-PACE-001) | 不进 | per-session state, 不是 per-request. hop_chain 含 `pacing_session_id` ref |
| `outbound_ip_burn_events` (F-NET-001) | 不进 | per-IP state, 不是 per-request. user-facing 不暴露 IP detail (operator-internal) |
| `audit_ledger_entries` (本 F-TRUST-001) | 自身 | per-request user-facing ledger, 1 row per user request |
| `admin_audit_events` (F-AUDIT-001) | 不进 | operator 行为 audit, 不是 user-verifiable |
| `billing_events` (F-BILL-001) | 不进 | 计费 ledger, 但每 billing_event 含 request_id, 用户可 cross-reference 跟 0013 ledger entry |

跨 chain reference 通过 `alt_event_id` 在 hop_chain HopAttestation 内 + `request_id` 在各 audit 表 column.

## 8. Pubkey Rotation

- 每 90 天 rotate ed25519 keypair (新 pubkey 发布到 `/v1/.well-known/huakai-pubkey.json`, 老 pubkey 仍 valid 30 天 grace period)
- 老 entry 永久用当时 pubkey_fingerprint, 用户 verify 时按 fingerprint match pubkey list
- 私钥存 HUAKAI KMS / KeyProvider (跟 F-AUTH-005 credential 加密同基础设施), 严禁明文存盘

## 9. 跟其它项目对比 (HUAKAI 强差异化)

| 项目 | trust chain 处理 | HUAKAI 升级 |
|---|---|---|
| sub2api / new-api / one-api | 没 trust chain 概念, audit log 是 operator-only (admin 可改) | HUAKAI Sigstore/Trillian 风格 append-only Merkle chain + ed25519 sig + user-verifiable API |
| litellm / portkey | log + tracing 但没 cryptographic chain (operator 可篡改) | HUAKAI cryptographic guarantee |
| helicone | OTel tracing 是 observability 不是信任链 (无 sig) | HUAKAI 是 trust chain (有 sig + Merkle) + observability 分两套 |
| (云厂自家 gateway: AWS Bedrock / Azure OpenAI / Vertex) | 上游 audit 不暴露给 caller, caller 必须信任云厂 | HUAKAI 把 audit 暴露给 end user, user 可自己 verify |

**HUAKAI F-TRUST-001 独有**:
- 用户可验证 (ed25519 sig + Merkle chain + public pubkey)
- model_chain 透明 (substitution 必须显式)
- hop_chain 透明 (router decision 可审计)
- append-only 强制 (trigger + INSERT-only writer + tampering 立刻 detect)
- cross-tenant 隔离 (tenant_id RLS, 用户只见自己)
- per-90 天 pubkey rotation (legacy entry 永久 valid)

## 10. 实施 Phase (Phase TRUST-1, 已部分完成)

- **Phase TRUST-1-A** (commit 0013 已完成): migration audit_ledger_entries 表 + writer pipeline
- **Phase TRUST-1-B** (3-5 天 codex, 待派): 加 trigger 强制 append-only + ledger_id 生成 + ed25519 sig integration (KMS / KeyProvider)
- **Phase TRUST-1-C** (3-5 天 codex, 待派): 4 user verification endpoint (GET entry / pubkey / chain-head / proof) + session middleware 集成
- **Phase TRUST-1-D** (2-3 天 codex, 待派): hop_chain HopAttestation 在各 gateway hop (router / pool / executor / forwarder / normalizer / channel-health) 真实填充 + model_chain 真填
- **Phase TRUST-1-E** (2-3 天 codex, 待派): admin transparency dashboard (model substitution 比例 / chain head verify / pubkey rotation timeline)

## 11. Owner 后续 OCAW

- (D-TRUST-1) pubkey 存储 backend — HUAKAI 自管 KMS vs 集成云 KMS (AWS / GCP)?
- (D-TRUST-2) chain 容量 — 1B entries 后是否切分 (per-tenant chain vs global chain)?
- (D-TRUST-3) deep verify 频率 — daily snapshot vs real-time vs on-demand?
- (D-TRUST-4) pubkey rotation 周期 — 90 天 (推) / 180 天 / 365 天?
- (D-TRUST-5) user-facing transparency 展示 — 默认 opt-out (操作员开启 toggle) vs opt-in (默认显示)? — 影响 Owner 商业策略

## 12. Acceptance test outline (AT-TRUST-001-001..010, 加进 docs/11_ACCEPTANCE_TEST_MATRIX.md)

- AT-TRUST-001-001: 每 gateway request 创建 1 row in audit_ledger_entries; request_id UNIQUE; tenant_id 跟 request 一致
- AT-TRUST-001-002: hop_chain JSON array 含完整 6 hop (router / pool / credential / upstream / normalize / channel-health) 每 hop 有 decision_summary
- AT-TRUST-001-003: model_chain `{requested, route_decided, upstream_reported}` 真填; substitution 时 3 字段不同 + client 收 `X-Huakai-Model-Substituted` header
- AT-TRUST-001-004: prev_merkle_root + merkle_root 32-byte 严格; 第 N entry 的 prev_merkle_root == 第 N-1 entry 的 merkle_root (chain 连续验证)
- AT-TRUST-001-005: ed25519 signature 真用 pubkey 验证 (pass = entry not tampered)
- AT-TRUST-001-006: UPDATE audit_ledger_entries row → trigger 拒绝; DELETE → trigger 拒绝
- AT-TRUST-001-007: GET /v1/ledger/entries/{request_id} 返完整 row + 用户 client-side 用 pubkey verify sig PASS
- AT-TRUST-001-008: GET /v1/.well-known/huakai-pubkey.json 公开 + 多 pubkey rotation 都列
- AT-TRUST-001-009: cross-tenant query — tenant A 查 tenant B 的 request_id → 404 (RLS 生效)
- AT-TRUST-001-010: hop_chain decision_summary 严禁含 raw user prompt / completion / token / cookie / PII

## 13. 风险表

| 风险 | 缓解 |
|---|---|
| ledger writer goroutine 阻塞致 request 慢 | writer 是 async outbox 模式 (跟 F-OBS-005 DLQ 共用 runtime, 不阻塞 request critical path); 失败后 DLQ + retry |
| Merkle chain 断裂 (writer race) | 单线程 writer + 顺序 INSERT + last-merkle-root 锁 (per tenant) |
| private key leak | KMS / KeyProvider 集成, 私钥永不出 KMS; ed25519 sig 算 在 KMS sign API |
| chain growth (10M entries / month → 1B+ entries / year) | per-tenant chain partition (D-TRUST-2 OCAW); cold archive 老 entry |
| user verification overhead (用户每 request 都 verify 太慢) | daily snapshot + deep verify on-demand; client SDK 提供 verify helper |
| substitution 不透明 (HUAKAI 自己 bug 不填 model_chain) | AT-TRUST-001-003 测试覆盖; admin dashboard 显示 NULL model_chain ratio |
| append-only trigger 误 trigger 致 normal 操作 fail | trigger 严格只覆盖 UPDATE/DELETE, INSERT 不影响; admin 误操作 INSERT 顺序错 → ledger_id UNIQUE constraint 触发 INSERT 失败 |

## 14. Source files read (Claude lane)

- commit 0013 backend/sql/migrations/0013_trust_chain_audit_ledger.up.sql (audit_ledger_entries 表 schema 锚定)
- commit a122a16 docs/specs/active-anti-detection.md §6 (cross-chain reference 反向参考)
- commit 07e575e docs/specs/request-pacing-mimicry.md §6 (cross-chain reference 反向参考)
- commit 07e575e docs/specs/outbound-ip-pool.md §6 (cross-chain reference 反向参考)
- commit 06f0ff2 docs/specs/device-fingerprint-binding.md (F-FP-001 reference)
- commit 06f0ff2 docs/specs/channel-health-auto-disable.md (F-CH-002 reference)
- memory: `project_core_trust_chain_differentiator` (HUAKAI 6 大核心差异化卖点)
- 不读任何上游项目源码 (clean-room保持)

## 15. OWNER 中文摘要

F-TRUST-001 用户可验证信任链 spec 落档 (HUAKAI 核心差异化 6 卖点之一 — 链路公开 + 模型校验 + 商家不能做假). 复用 commit 0013 已落 audit_ledger_entries schema (hop_chain + model_chain + prev_merkle_root + merkle_root + signature). 补 4 user verification endpoint (GET entry / pubkey / chain-head / Merkle proof). 实施分 5 sub-phase TRUST-1-A..E (A 已完成 schema + writer, B/C/D/E 待 codex 派, 10-15 天). 跟所有现有 gateway (sub2api / new-api / litellm / portkey / helicone) 的根本差异 = 用户可 cryptographic verify, 不必信任 operator. Phase 6 商业基础, 跟 F-PRIV-001 (无用户数据) + F-AUDIT-001 (消费透明) 共同形成 6 要求闭环. 5 Owner OCAW (pubkey 存储 backend / chain 容量分片 / deep verify 频率 / pubkey rotation 周期 / user-facing transparency 默认 opt-in vs out). AT-TRUST-001-001..010. 风险表含 ledger writer 阻塞 / Merkle 断裂 / private key leak / chain growth / verification overhead / substitution 不透明 / trigger 误触发.
