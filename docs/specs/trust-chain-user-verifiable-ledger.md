# Trust Chain User-Verifiable Ledger — F-TRUST-001 Spec

| 字段 | 值 |
|---|---|
| Feature ID | F-TRUST-001 trust chain user-verifiable ledger (Phase 6 commercial foundation, HUAKAI 核心差异化 1 + 3 + 4) |
| Lane | Claude PM-Orchestrator synthesis (Claude draft 在 `docs/process/plans/2026-05-16-f-trust-001-spec-claude.md` + Codex draft 在 `/tmp/codex-f-trust-001-spec-codex-draft.md`, 本 spec 是 PM 合并版) |
| Base | commit 0013 audit_ledger_entries schema (已落) + memory `project_core_trust_chain_differentiator` 6 大差异化卖点 |
| Phase | TRUST-1 (A 已完成 schema + writer; B/C/D/E 待 codex 派, 10-15 天) |
| Memory ref | [[project_core_trust_chain_differentiator]] [[feedback_huakai_better_than_sub2api]] [[feedback_stability_means_stronger]] |
| Scope | F-TRUST-001 用户可验证信任链 — 链路公开 + 模型校验 + 商家不能做假; 跟 F-PRIV-001 (无用户数据) + F-AUDIT-001 (消费透明) 共同形成 6 要求闭环 |
| Out of scope | operator-facing internal audit (F-AUDIT-001 处理操作员行为) / 隐私脱敏标准化 (F-PRIV-001) / 反代敏感层 audit (各层自身表) / 计费 ledger (F-BILL-001) |
| UTC | 2026-05-16T10:15:00Z (synthesis) |

## 1. 问题陈述

所有现有 AI gateway (sub2api / new-api / litellm / portkey / helicone / one-api) 是 **operator 单方信任模型**: 用户必须信任 operator 没动手脚 (没掺水 / 没换模型 / 没用便宜模型充贵的 / 没用免费 tier 卖付费). **用户无法验证**.

HUAKAI 核心差异化 6 要求 (memory `project_core_trust_chain_differentiator`):
1. **链路公开** — 每 request 的 routing 路径用户可见
2. **无用户数据日志** — gateway 不存 user prompt/completion text (F-PRIV-001 处理)
3. **模型校验用户可见** — 用户验证实际用了什么模型 (跟 requested 不一致也透明)
4. **商家不能做假** — operator 无法事后篡改 ledger
5. **日志只系统报错** — 系统级 error log 跟 user data log 严格分离 (F-PRIV-001 处理)
6. **用户消费透明** — 用户验证每 token 计费跟实际 upstream 一致 (F-AUDIT-001 处理)

F-TRUST-001 实施 **1 + 3 + 4**: 链路 + 模型校验 + cryptographic anti-tampering.

## 2. 信任链结构

audit_ledger_entries (schema 已落 commit 0013) 字段映射到用户验证用途:

| 字段 | 现有约束 | 用户验证用途 |
|---|---|---|
| `ledger_id` | text NOT NULL UNIQUE | 用户 receipt 稳定编号 |
| `occurred_at` | timestamptz NOT NULL | 时间一致性验证 + chain head 查询 |
| `request_id` | text NOT NULL UNIQUE | 用户 client log 拿 ID 查 ledger; retry attempt 走 hop_chain 子项不另开 row |
| `tenant_id` | bigint REFERENCES tenants(id) | DR-001 RLS; **公开 receipt 不直接暴露裸 tenant_id, 改用 `tenant_scope_ref`** (canonical entry 内字段, 防 cross-tenant DB 枚举) |
| `hop_chain` | jsonb NOT NULL DEFAULT '[]' | 链路证明数组 — safe metadata + 决策 ref + 引用 id/hash, 严禁 raw user content / secret |
| `model_chain` | jsonb | requested / route_decided / upstream_reported 3 段 + verdict (match / allowed_alias / mismatch / unknown) |
| `prev_merkle_root` | bytea (32) | 前一条 root; 第一条用 32 zero bytes |
| `merkle_root` | bytea (32) | 本条 root = sha256(prev_merkle_root \|\| canonical_entry_hash) |
| `pubkey_fingerprint` | text (16 char) | 用户从 `/v1/.well-known/huakai-pubkey.json` match pubkey |
| `signature` | text (base64 ed25519, ~88 char) | ed25519 sig over canonical entry hash; operator 无私钥写不出 valid sig |

**Canonical entry hash** 输入 (稳定 + 跨语言可复现, 排除 signature 自身):
```
schema_version: trust.ledger.v1
ledger_id
occurred_at
request_id
tenant_scope_ref
hop_chain
model_chain
prev_merkle_root
pubkey_fingerprint
```

**Redaction guard** (严格):
- 严禁: raw prompt / completion / tool input/output / cookie / Authorization header / API key / refresh token / provider credential bytes / proxy credential / raw upstream response/error body / 可逆 PII
- 允许: request_id / ledger_id / tenant_scope_ref / model name / provider family / route policy version / account public fingerprint / token counts (cross-ref to F-AUDIT-001) / cost refs / redacted error class / status class / latency bucket / canonical payload hash
- **opt-in only**: content binding hash (用户本地验证 prompt 没被改) — 默认 OFF, F-PRIV-001 单独 approve 才能 ON

## 3. HopAttestation Schema (hop_chain JSON array)

每个 hop:
```json
{
  "schema_version": "trust.hop.v1",
  "hop_index": 0,
  "hop_kind": "ingress_auth" | "policy_match" | "pool_select" | "credential_select" | "upstream_dispatch" | "response_finalize" | "channel_health",
  "actor": "gateway" | "executor" | "control_plane",
  "started_at": "<rfc3339 utc>",
  "ended_at": "<rfc3339 utc>",
  "decision_ref": "<safe enum or hash>",
  "feature_refs": ["F-POOL-001", "F-CH-002"],
  "alt_event_id": "<optional: ref to per-layer audit table row, e.g. channel_health_audit_events.id>"
}
```

`decision_ref` 严禁含 user prompt / response / token / cookie / credential bytes; 只允许:
- enum 值 (e.g. `selected_account_fingerprint=<pubkey_fp_8byte>`, `channel_id=5678`)
- 短 reason class (e.g. `lowest_latency`, `ramp_sample`, `failover_after_retry`)
- redacted ref (`alt_event_id` 指向各层 audit table row, 但不 inline content)

典型 6 hop (1 normal request):
1. `ingress_auth`: session middleware 认证 + tenant_id 解析
2. `policy_match`: requested model → matched route policy
3. `pool_select`: pool 选 account_id (含 PASR cache locality decision)
4. `credential_select`: credential version
5. `upstream_dispatch`: upstream URL + 实际收 status code
6. `response_finalize`: normalize 后 model_chain 填充

含 retry / ramp 时 hop > 6, 全 hop 都进 hop_chain (一 request 一 ledger entry).

`channel_health` hop 是 optional (F-CH-002 触发 signal 时加 1 hop), `alt_event_id` 指向 `channel_health_audit_events.id`.

## 4. Model Chain Validation

```json
{
  "schema_version": "trust.model.v1",
  "requested": "claude-opus-4-7",
  "route_decided": "claude-opus-4-7",
  "upstream_reported": "claude-opus-4-7-20251001",
  "verdict": "match" | "allowed_alias" | "mismatch" | "unknown"
}
```

3 字段 + verdict 必填. Streaming-in-flight 时 model_chain 可暂 null, stream 关闭时必须 fill.

**Verdict 判定**:
- `match`: requested == route_decided == upstream_reported semantic-equivalent (绿)
- `allowed_alias`: route_decided 是 requested 在 policy 内的 allowed substitution (黄, 必须 client 收 `X-Huakai-Model-Substituted` header per F-MODEL-SUBSTITUTION-001)
- `mismatch`: route_decided 跟 requested 语义不同 (红, **fail-closed** — request reject 或 transparent X-Huakai-Mismatch header 上报 user); admin dashboard 显示 mismatch ratio
- `unknown`: streaming 中或 upstream 没报 model ID (灰, admin dashboard 显示 unknown ratio, > 阈值 alert)

## 5. User Verification API

**Endpoint 1 — `GET /v1/ledger/entries/{request_id}`** (tenant-scoped, session middleware F-SESSION-001):
- Response: full row + verify hint (canonical_payload bytes)
- 用户 client-side 用 ed25519 公钥 verify signature

**Endpoint 2 — `POST /v1/ledger/verify`** (detached verification, public if entry already known):
- Body: `{canonical_payload, signature, pubkey_fingerprint}`
- Response: `{valid: bool, key_status: 'active'|'rotated'|'revoked', timestamp_age_seconds}`
- 用户带自己存的 receipt 离线验证 (不必跟 HUAKAI 实时 round-trip)

**Endpoint 3 — `GET /v1/.well-known/huakai-pubkey.json`** (public, no auth):
- Response: `{pubkeys: [{fingerprint, pubkey_base64, valid_from, valid_until, status}, ...]}`
- 多 pubkey rotation 都列 (active + 30 天 grace period)

**Endpoint 4 — `GET /v1/ledger/chain-head` + `/v1/ledger/verify/{request_id}/proof`** (tenant-scoped):
- chain-head: `{last_ledger_id, last_merkle_root, occurred_at, total_entries}` (daily snapshot 验 chain 完整)
- proof: Sparse Merkle inclusion proof `{entry, prev_merkle_root, sibling_hash_path[]}` (单 entry 在 chain 内验证, 无需下载整 chain)

## 6. Append-Only Enforcement

### DB trigger 强制 (新加 migration)
```sql
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

### Writer pattern
- 单线程 `ledger_writer` goroutine (per tenant 一个, 防 chain 断裂)
- ledger_id 通过 monotonic counter 保证连续 (不跳号)
- 失败 → DLQ + retry; 永不 INSERT 跳 sequence
- writer 用 PostgreSQL advisory lock 防双 writer 同 tenant race
- canonical_entry_hash 计算 + ed25519 sign 全部 in-tx (commit 前必须 sig 完成)

## 7. Cross-Chain Reference

各反代层 audit 表跟 F-TRUST ledger 关系:

| 反代层 audit 表 | 是否进 0013 ledger | 关系 |
|---|---|---|
| `active_detection_events` (F-ADV-001) | 不进 | system-level, 不是 per-request. detection 触发 channel cooldown 影响某 request 时, 该 request hop_chain 含 `channel_switched_due_to_detection_class` decision_ref |
| `device_fingerprint_bindings` (F-FP-001) | 不进 | per-account state. user request 用了某 fingerprint binding 时, hop_chain 含 `fingerprint_profile_id_pubkey_fp` ref (不暴露 fingerprint detail) |
| `channel_health_audit_events` (F-CH-002) | 不进 | per-channel state. hop_chain 含 `alt_event_id` 指向本 audit row |
| `pacing_session_traces` (F-PACE-001) | 不进 | per-session. hop_chain 含 `pacing_session_id` ref |
| `outbound_ip_burn_events` (F-NET-001) | 不进 | per-IP. user-facing 不暴露 IP detail (operator-internal) |
| `audit_ledger_entries` (本 F-TRUST-001) | 自身 | per-request user-facing, 1 row per user request |
| `admin_audit_events` (F-AUDIT-001 计划) | 不进 | operator 行为 audit, 不是 user-verifiable |
| `billing_events` (F-BILL-001) | 不进 | 计费 ledger. 每 billing_event 含 request_id, 用户 cross-reference 跟 0013 |

跨 chain reference: HopAttestation `alt_event_id` 字段 + 各 audit 表 `request_id` column (允许 user 用 request_id 单向 trace 到所有相关层 audit).

## 8. Pubkey Rotation

- 每 90 天 rotate ed25519 keypair (新 pubkey 立刻发布到 `/v1/.well-known/huakai-pubkey.json` + status=active; 老 pubkey status=rotated 仍 valid 30 天 grace; 30 天后 status=revoked 仍可 verify 历史 entry)
- 老 entry 永久用当时 pubkey_fingerprint, verify 时按 fingerprint match pubkey list
- 私钥存 HUAKAI KMS / KeyProvider (跟 F-AUTH-005 同基础设施); ed25519 sign 在 KMS API 内, 私钥永不出 KMS
- key compromise 应急: 立刻 revoke (status=revoked), 该 key 期间所有 entry 标 `key_status=revoked`, admin alert + user transparency dashboard 显示

## 9. 跟其它项目对比 (HUAKAI 强差异化)

| 项目类别 | trust chain 处理 | HUAKAI 升级 |
|---|---|---|
| operator-only audit gateway (sub2api / new-api / one-api 类) | audit log operator-only (admin 可改), 用户无 verify | HUAKAI Sigstore/Trillian 风格 append-only Merkle chain + ed25519 sig + user-verifiable API |
| observability tracing (litellm / portkey / helicone) | OTel tracing 是 observability 不是信任链 (无 sig + 可改) | HUAKAI cryptographic guarantee + trust chain 跟 observability 分两套 (各有用途) |
| 云厂自家 gateway (AWS Bedrock / Azure OpenAI / Vertex) | 上游 audit 不暴露 caller, caller 必须信任云厂 | HUAKAI 把 audit 暴露给 end user, user 可自己 verify operator + 上游 |
| 公开公证 / Sigstore / Trillian | 用 Merkle chain + sig 做 cryptographic transparency log, 通用 | HUAKAI 把 Sigstore 思路应用到 AI gateway: per-request receipt + model verdict + cross-chain reference 是 HUAKAI 独有 |

**HUAKAI F-TRUST-001 独有**:
- 用户可验证 (ed25519 + Merkle + public pubkey + detached verify)
- model_chain verdict 透明 (match / allowed_alias / mismatch / unknown 4 分类)
- hop_chain 透明 (7 hop_kind 分类 + alt_event_id 跟反代各层 cross-ref)
- append-only DB trigger 强制 + writer 单线程 + 私钥 KMS
- tenant_scope_ref (receipt 不暴露裸 DB tenant_id 防枚举)
- schema_version (trust.hop.v1 / trust.model.v1 / trust.ledger.v1, backward compat)
- 90 天 pubkey rotation + 老 entry 永久可 verify

## 10. 实施 Phase (Phase TRUST-1, 5 sub-phase)

- **Phase TRUST-1-A** (commit 0013 已完成): migration `audit_ledger_entries` 表 + writer pipeline 骨架. Canonical contract + privacy allowlist 也算 1-A 范围 (写入策略已 enforce)
- **Phase TRUST-1-B** (3-5 天 codex): trigger 强制 append-only + ed25519 sig integration (KMS API + canonical_payload 计算) + writer Merkle continuity 完整 + DB immutability test
- **Phase TRUST-1-C** (3-5 天 codex): 4 user verification endpoint (entry / detached verify / pubkey / chain-head + proof) + session middleware 集成
- **Phase TRUST-1-D** (2-3 天 codex): hop_chain HopAttestation 真填 (在 router / pool / credential / executor / forwarder / normalizer / channel-health 各 hop 注入) + model_chain verdict 真判
- **Phase TRUST-1-E** (2-3 天 codex): pubkey rotation 自动化 + chain head daily checkpoint 公开 + admin transparency dashboard (model substitution ratio / chain verify status / pubkey rotation timeline) + release gate criteria

## 11. Owner 后续 OCAW

- (D-TRUST-1) **ledger write 失败是否 fail-closed** — request reject 还是 fall-through 接受 ledger 缺?
- (D-TRUST-2) pubkey 存储 backend — HUAKAI 自管 KMS / 集成云 KMS (AWS / GCP) / HSM?
- (D-TRUST-3) chain 容量 — 1B entries 后是否切分 (per-tenant chain vs global chain vs 时间窗 partition)?
- (D-TRUST-4) deep verify 频率 — daily snapshot / real-time / on-demand?
- (D-TRUST-5) pubkey rotation 周期 — 90 天 (推) / 180 天 / 365 天?
- (D-TRUST-6) **mismatch verdict 是否自动退款** — F-AUDIT-001 联动?
- (D-TRUST-7) account public fingerprint 粒度 — pubkey_fp[:8] vs hash(account_id + tenant)? 影响 receipt 信息量 vs 隐私
- (D-TRUST-8) opt-in content binding hash — Owner 是否启用 (用户 own prompt 可验, 但需 F-PRIV-001 单独 approve)?

## 12. Acceptance test outline (AT-TRUST-001-001..010)

- AT-TRUST-001-001: 每 gateway request 创建 1 row in audit_ledger_entries; request_id UNIQUE; tenant_id 一致
- AT-TRUST-001-002: hop_chain JSON array 含完整 hop (ingress_auth → response_finalize), 每 hop 有 decision_ref + safe schema
- AT-TRUST-001-003: model_chain 3 字段填 + verdict 4 分类 (match / allowed_alias / mismatch / unknown); mismatch 时 client 收 X-Huakai-Mismatch header
- AT-TRUST-001-004: prev_merkle_root + merkle_root 32-byte; chain 连续 (entry N prev_merkle_root == entry N-1 merkle_root)
- AT-TRUST-001-005: ed25519 signature 用 pubkey verify PASS; tamper any byte → verify FAIL
- AT-TRUST-001-006: UPDATE audit_ledger_entries row → trigger 拒绝; DELETE → 拒绝
- AT-TRUST-001-007: GET entry endpoint 返完整 row + 用户 client-side detached verify PASS
- AT-TRUST-001-008: pubkey rotation — 老 pubkey rotated/revoked 仍可 verify 历史 entry; 新 entry 用新 pubkey
- AT-TRUST-001-009: cross-tenant query — tenant A 查 tenant B request_id → 404 (RLS + tenant_scope_ref 不暴露裸 tenant_id)
- AT-TRUST-001-010: hop_chain.decision_ref / model_chain 严禁含 raw prompt / completion / token / cookie / PII

## 13. 风险表

| 风险 | Severity | 缓解 |
|---|---|---|
| ledger writer goroutine 阻塞致 request 慢 | MED | async outbox 模式 (跟 F-OBS-005 DLQ 共 runtime, 不阻塞 critical path); 失败 DLQ retry |
| Merkle chain 断裂 (writer race) | HIGH | 单线程 writer + 顺序 INSERT + last-merkle-root advisory lock (per tenant) |
| private key leak | HIGH | KMS / KeyProvider, 私钥永不出 KMS; ed25519 sign 在 KMS API; 立刻 rotate + 历史 entry 标 revoked |
| chain growth (10M/月 → 1B/年) | MED | per-tenant chain partition (D-TRUST-3); cold archive 老 entry; deep verify 用 Merkle proof 不必下载全 chain |
| user verification overhead (每 request 都 verify 太慢) | LOW | daily snapshot + deep verify on-demand + client SDK 提供 verify helper |
| substitution 不透明 (HUAKAI bug 不填 model_chain) | HIGH | AT-TRUST-001-003 测试覆盖; admin dashboard NULL model_chain ratio alert |
| append-only trigger 误 trigger | LOW | trigger 仅 UPDATE/DELETE, INSERT 不影响; admin 误 INSERT 顺序错 → ledger_id UNIQUE 触发 INSERT 失败 |
| writer 写失败 fail-closed vs fall-through | MED | D-TRUST-1 OCAW; 默认推 fail-closed (保 trust chain 完整 > 单 request availability) |
| receipt 字段泄漏 cross-tenant DB id 枚举 | MED | tenant_scope_ref 抽象 (不直接暴露裸 tenant_id) |
| key 轮换期间签名版本不一致 | LOW | pubkey list 含 valid_from/valid_until + status; verify 端 match fingerprint |
| 隐私字段误入 hop_chain | HIGH | redaction allowlist 强制 + AT-TRUST-001-010 验证; 实施前 codex review 必查 |
| Too little hop detail 信任值低 | LOW | OCAW D-TRUST-7 决定 account fingerprint 粒度 |

## 14. Source files read + 中文摘要

### Source files read (synthesis lane)
- commit 0013 backend/sql/migrations/0013_trust_chain_audit_ledger.up.sql (audit_ledger_entries 表锚定)
- commit a122a16 docs/specs/active-anti-detection.md §6 (cross-chain F-ADV reference)
- commit 07e575e docs/specs/request-pacing-mimicry.md §6 (cross-chain F-PACE reference)
- commit 07e575e docs/specs/outbound-ip-pool.md §6 (cross-chain F-NET reference)
- commit 06f0ff2 docs/specs/device-fingerprint-binding.md (F-FP-001 reference)
- commit 06f0ff2 docs/specs/channel-health-auto-disable.md (F-CH-002 reference)
- docs/process/plans/2026-05-16-f-trust-001-spec-claude.md (Claude lane parallel-draft, 16KB)
- /tmp/codex-f-trust-001-spec-codex-draft.md (Codex lane parallel-draft, 30KB)
- memory: `project_core_trust_chain_differentiator` (HUAKAI 6 大差异化)
- 不读任何上游项目源码 (clean-room 保持)

### Synthesis decisions (Claude + Codex diff)
- 取 Codex: `schema_version` (trust.hop.v1 / trust.model.v1 / trust.ledger.v1, backward compat) + `tenant_scope_ref` (防 cross-tenant DB 枚举) + model_chain verdict 4 分类 (match/allowed_alias/mismatch/unknown) + 风险表 Severity 列 + Endpoint 2 detached verification (用户离线 verify)
- 取 Claude: cross-chain 8 行表 + Phase TRUST-1-A 已完成标注 + AT 列表 + hop_kind 具体 enum (7 类)
- 合并: hop_kind enum 用 Codex 7 类 (ingress_auth / policy_match / pool_select / credential_select / upstream_dispatch / response_finalize / channel_health) — 比 Claude 6 类多一项 `ingress_auth`; HopAttestation schema 加 `actor` 字段 (Codex); opt-in content binding hash 默认 OFF (Claude+Codex 一致)

### OWNER 中文摘要
F-TRUST-001 用户可验证信任链 synthesis spec 落档 (Claude+Codex 平行 draft 合并). HUAKAI 6 大核心差异化 1+3+4 项 (链路公开 + 模型校验 + 商家不能做假). 复用 commit 0013 已落 audit_ledger_entries schema. 关键设计: ed25519 sig + append-only Merkle chain + DB trigger 强制 + 单线程 writer + KMS 私钥 + 90 天 pubkey rotation + 4 user verification endpoint + hop_chain 7 类 hop_kind + model_chain verdict 4 分类 + tenant_scope_ref 防枚举 + redaction allowlist 严. 实施 Phase TRUST-1-A 已完成 (schema + writer), TRUST-1-B/C/D/E 待 codex 派 (10-15 天). 跟所有现有 gateway (sub2api/new-api/litellm/portkey/helicone/云厂) 的根本差异 = 用户可 cryptographic verify, 不必信任 operator. Phase 6 商业基础. 8 Owner OCAW (write fail-closed / KMS backend / chain partition / verify 频率 / rotation 周期 / mismatch 自动退款 / fingerprint 粒度 / content binding hash 是否启). AT-TRUST-001-001..010. 风险表 12 项含 Severity (HIGH: Merkle 断裂 / private key leak / substitution 不透明 / 隐私字段误入). Synthesis 决策列在 §14.
