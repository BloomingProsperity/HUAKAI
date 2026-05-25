# User Consumption Transparency — F-AUDIT-001 Spec

| 字段 | 值 |
|---|---|
| Feature ID | F-AUDIT-001 user consumption transparency (HUAKAI 6 大差异化 6) |
| Lane | Claude PM-Orchestrator synthesis (Claude draft + Codex draft, PM 合并版) |
| Base | F-TRUST-001 spec + F-BILL-001 settler + F-BILL-002 voucher + F-PRIV-001 redaction guard |
| Phase | AUDIT-1 (5 sub-phase, 7-11 天 codex) |
| Memory ref | [[project_core_trust_chain_differentiator]] [[feedback_huakai_better_than_sub2api]] |
| Scope | F-AUDIT-001 实施 6 大差异化 6 (用户消费透明) — per-request user-verifiable cost receipt; mismatch 自动退款; public pricing endpoint; user dispute API |
| Out of scope | F-BILL-001 settler 内部 (复用其 cost calc) / F-BILL-002 voucher mechanic (跨 ref) / F-PRIV-001 redaction (cross-chain enforce) / F-TRUST-001 ledger (cross-chain ref) / 真 payment provider 集成 (Stripe / Alipay 留 F-PAY-001) |
| UTC | 2026-05-16T11:10:00Z (synthesis) |

## 1. 问题陈述

现有所有 AI gateway 计费是 **operator 单方账本** — 用户拿月底账单不知:
- 每 request 上游真实多少 token (operator 可虚报)
- substitution (opus → sonnet) 按 sonnet rate 退差价还是 opus 全收
- cache hit 0 charge 还是部分收
- retry attempt 1 次还是 N 次计费
- 上游 free tier (Gemini free quota) 是否被 operator 当付费卖

HUAKAI 6 大差异化 6 = **用户消费透明** — 用户验证 cost = upstream reported tokens × rate; mismatch 自动退款.

F-AUDIT-001 实施 = per-request user-facing cost receipt + cost validation API + mismatch 退款 + public pricing + user dispute API.

## 2. 设计原则 (架构 OCAW)

**derived-first, physical-snapshot-second** (取 Codex 推荐):
- receipt 默认是 **derived view** (从 audit_ledger_entries JOIN billing_events JOIN usage_record JOIN pricing_rate_tables 算出)
- 不引专属 physical table (避免成第二套 billing source of truth, 防 inconsistency)
- 物理 receipt snapshot (`user_cost_receipts`) 仅作 **append-only audit cache** (历史 receipt 不可改, 改 = append 新 row)
- 物理 snapshot 用途: ed25519 sign + dispute reference (避免 derived 每次 recompute 性能)

跟 Claude draft 3 表设计差异: Claude 推 user_cost_receipts 是 first-class source of truth (1 row per request, FK 强约束); Codex 推 derived view 默认 + 后续可选 snapshot. **Synthesis 采 Codex 推荐 (architecture OCAW: derived-first 防 dual ledger inconsistency)**.

## 3. 透明项

每 user request 必须透明 cost 维度:

| 透明项 | 含义 |
|---|---|
| `input_token_count` | 实际计费 input token (跟 upstream API 返 prompt_tokens 一致 + 跟 F-TRUST model_chain.upstream_reported cross-ref) |
| `output_token_count` | 同, upstream completion_tokens |
| `cache_read_token_count` | cache hit input, 按 D-AUDIT-1 charge ratio (默认 0) |
| `cache_write_token_count` | cache write input, 按 D-AUDIT-1 ratio (默认 1x) |
| `cost_rate_version` | rate table 版本 (e.g. anthropic_2025_q1_v3) — historical immutable, 老 receipt 永久按当时 version 验 |
| `cost_total_micro_usd` | 本 request 总 cost (micro-USD = USD × 10^6) |
| `model_actual_billed` | 实际按哪个 model rate 计 (跟 F-TRUST model_chain.route_decided 一致) |
| `retry_attempt_count` | retry 次数; 按 D-AUDIT-3 (默认仅 final attempt 计费, failed pre-output 0 charge) |
| `substitution_refund_micro_usd` | substitution (opus→sonnet) 退差价 (micro-USD = USD × 10^6) |
| `voucher_redeemed_micro_usd` | voucher 抵扣 (F-BILL-002 cross-ref, micro-USD = USD × 10^6) |
| `upstream_free_tier_used` | 是否走上游 free quota (operator policy 决定是否 0 charge user) |
| `usage_source` | `authoritative` (upstream 报数) / `provisional` (streaming-in-flight 估) / `inferred` (上游未报, HUAKAI 推算) — 用户透明区分 |

## 4. Receipt 结构

```json
{
  "schema_version": "audit.receipt.v1",
  "receipt_id": "rcpt_xxxxx",
  "request_id": "req_xxxxx",                       // 跟 F-TRUST audit_ledger_entries.request_id 一致
  "tenant_scope_ref": "tenant-local-ref",         // 不直接暴露裸 tenant_id (跟 F-TRUST 一致)
  "occurred_at": "2026-05-16T...",
  "ledger_id": "ldg_xxxxx",                        // F-TRUST ledger_id cross-ref
  "billing_event_id": "bev_xxxxx",                 // F-BILL-001 cross-ref
  "cost": { /* §3 字段 */ },
  "validation_state": "valid" | "provisional" | "mismatch_pending" | "mismatch_refunded" | "not_billable" | "receipt_unavailable",
  "verdict": "match" | "substitution_refund" | "mismatch_refund_pending" | "unknown",
  "adjustment_refs": [],                           // append-only refund/correction refs
  "canonical_hash": "sha256[:8]",
  "signature": "ed25519-base64",
  "pubkey_fingerprint": "<复用 F-TRUST keypair fingerprint>"
}
```

`validation_state` 取 Codex 加细:
- `valid`: 全字段 authoritative + verdict=match
- `provisional`: usage_source=provisional/inferred (streaming-in-flight 或 upstream 未报), client 知道不算 final
- `mismatch_pending`: F-TRUST verdict=mismatch, 退款 workflow 触发中
- `mismatch_refunded`: 退款 commit, append adjustment_refs
- `not_billable`: F-BILL-001 settler 判 aborted claim (upstream terminal failure pre-output), cost=0
- `receipt_unavailable`: billing_event 存在但 usage_record DLQ/replay 中, 拒绝 false-valid

Receipt 严禁含 prompt/completion (F-PRIV-001 redaction enforced).

## 5. Cost Validation

用户 client-side:
```
cost_expected = sum(token_type × rate) × model_multiplier - substitution_refund_micro_usd - voucher_redeemed_micro_usd
assert(cost_expected == cost_total_micro_usd)
```

rate 从 `/v1/pricing/{cost_rate_version}` 拿 (public endpoint, 可缓存).

**Mismatch 退款 workflow** (D-AUDIT-2):
- F-TRUST verdict=mismatch → F-AUDIT validation_state=mismatch_pending
- 退款 = (requested rate - actual rate) × token_count, 上限 ≤ original cost
- 退款写 voucher_redemption_events.refund_origin='F-AUDIT-001-mismatch' or balance credit
- admin 复审 (24h auto-approve, D-AUDIT-6)
- 退款 commit → append `adjustment_refs` (不 mutate 原 receipt row)

## 6. User Verification API

**Endpoint 1 — `GET /v1/ledger/cost/{request_id}`** (tenant-scoped, F-SESSION-001 bearer):
- Response: `UserCostReceipt` (derived view + signature)
- 用户 client-side verify

**Endpoint 2 — `POST /v1/ledger/cost/verify`** (detached, public if payload self-contained):
- Body: `{canonical_payload, signature, pubkey_fingerprint}`
- Response: `{valid, key_status, age_seconds}` (**不查 DB 防 request_id existence oracle**, Codex 加细)

**Endpoint 3 — `GET /v1/pricing/{cost_rate_version}`** (public, 可缓存):
- Response: rate table; 老 version 永久可查

**Endpoint 4 — `GET /v1/ledger/cost-summary?from&to&granularity=day|hour`** (tenant-scoped):
- aggregate cost + verdict breakdown + period

**Endpoint 5 — `POST /v1/ledger/cost/{request_id}/dispute`** (tenant-scoped):
- Body: `{user_calculated_cost, evidence}` (evidence redacted, F-PRIV enforced)
- Response: dispute_id + admin workflow trigger
- 即便 verdict=match 也允许 (防 system bug 漏报)

## 7. 实施机制

### 7.1 Cost Calculator
- 复用 F-BILL-001 settler 内部 cost 计算 (不重写)
- 加 `BuildUserCostReceipt(billing_event_id) → UserCostReceipt` derived view formatter

### 7.2 Receipt Formatter
- 输出 schema 严格 (§4); 经 F-PRIV-001 Redactor.SanitizePayload (double check)
- canonical_hash + ed25519 sign 复用 F-TRUST keypair (省一套 KMS pipeline)

### 7.3 Mismatch Refund Workflow
- F-TRUST writer 写 ledger 后 trigger F-AUDIT receipt 生成
- verdict=mismatch → 写 receipt snapshot + 触发 refund worker (异步 outbox, F-OBS-005 DLQ 共 runtime)
- refund worker: 算金额 + 写 voucher_redemption_events 或 balance credit + admin alert

### 7.4 Pricing Endpoint
- `pricing_rate_tables` 表: per (version, model) row, rate immutable after `valid_until`
- admin CRUD + public GET; rate 改动通过 new version (不溯及既往)

### 7.5 Dispute Handling
- `cost_disputes` 表 (per tenant, status enum + admin notes)
- 24h auto-approve (D-AUDIT-6 可调); admin 可 reject/close

## 8. Storage

新表 (架构 OCAW 决策: derived-first, snapshot-second):

```sql
-- 物理 snapshot, append-only audit cache
CREATE TABLE user_cost_receipts (
  id                    BIGSERIAL PRIMARY KEY,
  receipt_id            TEXT NOT NULL UNIQUE,
  request_id            TEXT NOT NULL REFERENCES audit_ledger_entries(request_id),
  tenant_id             BIGINT NOT NULL REFERENCES tenants(id),  -- DR-001
  occurred_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  ledger_id             TEXT REFERENCES audit_ledger_entries(ledger_id),
  billing_event_id      TEXT,  -- loose FK, 跟 F-BILL-001
  schema_version        TEXT NOT NULL DEFAULT 'audit.receipt.v1',
  cost_payload          JSONB NOT NULL,  -- redaction enforced (F-PRIV)
  validation_state      TEXT NOT NULL CHECK (validation_state IN ('valid','provisional','mismatch_pending','mismatch_refunded','not_billable','receipt_unavailable')),
  verdict               TEXT NOT NULL CHECK (verdict IN ('match','substitution_refund','mismatch_refund_pending','unknown')),
  adjustment_refs       JSONB DEFAULT '[]'::jsonb,  -- append-only refund refs
  canonical_hash        TEXT NOT NULL,
  signature             TEXT NOT NULL,
  pubkey_fingerprint    TEXT NOT NULL
);
CREATE INDEX idx_cost_receipts_tenant_time ON user_cost_receipts (tenant_id, occurred_at DESC);
CREATE INDEX idx_cost_receipts_request ON user_cost_receipts (request_id);
CREATE INDEX idx_cost_receipts_validation ON user_cost_receipts (validation_state) WHERE validation_state != 'valid';
-- append-only trigger 强制 (跟 F-TRUST 同 pattern)
CREATE TRIGGER receipts_append_only_update BEFORE UPDATE ON user_cost_receipts FOR EACH ROW EXECUTE FUNCTION enforce_ledger_append_only();
CREATE TRIGGER receipts_append_only_delete BEFORE DELETE ON user_cost_receipts FOR EACH ROW EXECUTE FUNCTION enforce_ledger_append_only();

-- pricing rate table (per version + per model, historical immutable)
CREATE TABLE pricing_rate_tables (
  id                    BIGSERIAL PRIMARY KEY,
  cost_rate_version     TEXT NOT NULL,
  model                 TEXT NOT NULL,
  input_rate_micro      NUMERIC NOT NULL CHECK (input_rate_micro >= 0),
  output_rate_micro     NUMERIC NOT NULL CHECK (output_rate_micro >= 0),
  cache_read_rate_micro NUMERIC,
  cache_write_rate_micro NUMERIC,
  model_multiplier      NUMERIC NOT NULL DEFAULT 1.0,
  valid_from            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  valid_until           TIMESTAMPTZ,
  notes                 TEXT,
  UNIQUE (cost_rate_version, model)
);

-- user dispute
CREATE TABLE cost_disputes (
  id                    BIGSERIAL PRIMARY KEY,
  dispute_id            TEXT NOT NULL UNIQUE,
  tenant_id             BIGINT NOT NULL REFERENCES tenants(id),
  receipt_id            TEXT NOT NULL REFERENCES user_cost_receipts(receipt_id),
  user_calculated_cost  NUMERIC NOT NULL,
  evidence              JSONB,  -- redacted (F-PRIV)
  status                TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved_refund','rejected','closed')),
  created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  resolved_at           TIMESTAMPTZ,
  admin_notes           TEXT
);
CREATE INDEX idx_disputes_tenant_status ON cost_disputes (tenant_id, status, created_at DESC);
```

cost_payload JSONB redaction: F-PRIV-001 `Redactor.SanitizePayload` 强制. 严禁 raw prompt/completion/cookie/token/raw upstream body.

## 9. 实施 Phase (Phase AUDIT-1)

- **Phase AUDIT-1-A** (1-2 天): migration 0025 (3 表) + Go types + Redactor 集成
- **Phase AUDIT-1-B** (2-3 天): Cost Calculator 复用 F-BILL-001 + Receipt Formatter (derived view + signature)
- **Phase AUDIT-1-C** (2 天): 5 user verification endpoint + admin pricing CRUD
- **Phase AUDIT-1-D** (1-2 天): Mismatch refund workflow (异步 outbox, F-OBS-005 DLQ 共)
- **Phase AUDIT-1-E** (1-2 天): AT-AUDIT-001 测试 + admin transparency dashboard (cost mismatch ratio / refund total / dispute count)

## 10. 跟其它项目对比 (HUAKAI 强差异化)

| 项目类别 | 计费透明度 | HUAKAI 升级 |
|---|---|---|
| operator-only billing (sub2api / new-api / one-api 类) | 月底总账, 不知 per-request token, 不能验证 | HUAKAI per-request receipt + ed25519 sign + client-side verify |
| observability tracing (litellm / portkey / helicone) | per-request cost trace operator-only | HUAKAI user-visible + verify + dispute |
| 云厂 gateway (Bedrock / Azure / Vertex) | 上游账单, caller 信任云厂 | HUAKAI 暴露上游 token count, cross-check upstream API self-report |
| stripe / 商业 metering | invoice 可见, 无法 per-event verify | HUAKAI client-side `tokens × rate = cost` 计算 (rate 公开), match receipt |

**HUAKAI 独有**:
- per-request user-facing cost receipt + ed25519 sign (复用 F-TRUST keypair)
- derived-first 设计 (避免 dual billing ledger inconsistency)
- mismatch verdict 自动退款 (D-AUDIT-2 OCAW 触发)
- public pricing endpoint + version (用户独立计算)
- substitution refund 透明 (allowed_alias 时按差价退)
- cache hit charging 默认 0 (D-AUDIT-1 OCAW)
- cost dispute API (用户可发争议即便 verdict=match)
- upstream_free_tier_used 标识
- validation_state 6 类 (取 codex: provisional / not_billable / receipt_unavailable 等防 false-valid)
- detached verify 不查 DB (防 request_id existence oracle)

## 11. Owner 后续 OCAW

- (D-AUDIT-1) cache hit charging 默认 — 0 (推, HUAKAI 差异化) / 50% / 100%?
- (D-AUDIT-2) mismatch 自动退款触发阈值 — 立刻退 / 累 N 次 / admin 手动?
- (D-AUDIT-3) retry attempt 计费 — 每 attempt 全计 / 仅 final / failed pre-output 0 charge (推)?
- (D-AUDIT-4) upstream free tier 是否 0 charge user — 是 (推, HUAKAI 透明) / 否 (operator markup)?
- (D-AUDIT-5) receipt 默认状态 — 自动生成 (推) / opt-in / opt-out?
- (D-AUDIT-6) dispute auto-approve — 24h / 72h / 7 天?
- (D-AUDIT-7) cost_rate_version 改动是否溯及既往 — 不溯及 (推) / 溯及当月?
- (D-AUDIT-8) physical snapshot table vs pure derived view (架构 OCAW) — snapshot for sign + dispute (推 Codex 设计) / 全 derived?

## 12. Acceptance test outline (AT-AUDIT-001-001..016)

| Test ID | Scenario | Expected |
|---|---|---|
| AT-001 | Happy-path receipt validates exact cost | API `valid`; recomputed cost = final |
| AT-002 | Cross-tenant request_id no leak | safe 404; no timing/message hint |
| AT-003 | Receipt redaction-safe | 无 prompt/completion/tool body/cookie/raw voucher code |
| AT-004 | F-TRUST mismatch → refund | mismatch_pending → mismatch_refunded + adjustment_refs |
| AT-005 | Allowed cheaper substitution lower rate | alias + lower rate + no hidden higher charge |
| AT-006 | Cache-hit charge explicit | separate cost lines for cache create/read |
| AT-007 | Retry attempts no double charge | failed pre-output 0 charge; final charge once |
| AT-008 | Aborted claim not billable | `not_billable`, cost=0, balance delta=0 |
| AT-009 | Provisional inferred usage visible | initial `provisional`; later `valid` or adjustment |
| AT-010 | Late authoritative usage appends correction only | original usage row 不动; reconciliation/adjustment refs listed |
| AT-011 | Rate version historical | 老 receipt 按 stored version 验, 不被新 version 影响 |
| AT-012 | Voucher top-up vs usage 不混淆 | receipt 引 usage billing_event; voucher 仅 balance source ref |
| AT-013 | Detached verify no request oracle | 不查 DB; refuse 或 non-oracle error |
| AT-014 | Billing event 存 + usage DLQ | receipt `receipt_unavailable` 不 false-valid |
| AT-015 | Decimal precision exact | many tiny token charges 求和 = numeric precision |
| AT-016 | Receipt generation idempotent | same canonical_hash before adjustment; new state appends refs not change original |

## 13. 风险表

| 风险 | Severity | 缓解 |
|---|---|---|
| Receipt 成 second source of money truth | HIGH | Derived view first; physical 仅 snapshot/hash/status, 不替 billing rows (D-AUDIT-8 已决) |
| Cross-tenant receipt leak via request_id | HIGH | Tenant + user filter before lookup; safe not-found; AT-002/013 |
| Receipt 含 prompt/response/raw body | HIGH | Allowlist formatter + scan tests + F-PRIV alignment; 无 arbitrary JSON passthrough |
| F-TRUST nullable tenant scope schema | HIGH | Fail closed for null entries; Owner-approve backfill/constraint before Released |
| Mismatch charged as normal | HIGH | F-TRUST verdict integration; mismatch 必须 pending/refunded |
| Retry double charge | HIGH | Attempt-level lines + idempotency + default no failed pre-output charge |
| Cache token hidden in blended rate | MED | Separate token/rate/cost lines |
| Rate version 改后老 receipt 失效 | HIGH | Historical rate immutable/retained; receipt validate against stored version |
| Provisional 当 verified | MED | `provisional` state + reconciliation SLA + 无 green status until authoritative |
| Refund 写 mutate 原 row | HIGH | Append-only adjustment_refs only; trigger 强制 |
| Detached verify 成 request existence oracle | MED | Public mode 仅 verify supplied payload; auth fetch 查 DB |
| Physical receipt 表 migration risk | MED | 用 derived view 兜底; migration 谨慎 |
| Over-refunding 模糊 upstream 数据 | MED | OCAW threshold/SLA; dispute state for ambiguous |
| ed25519 sign 性能 (每 request) | MED | 复用 F-TRUST KMS; benchmark + async sign if needed |
| Operator 篡改 receipt | HIGH | ed25519 sign + Merkle chain (复用 F-TRUST) + user dispute API 兜底 |
| Clean-room contamination | LOW | 本 lane 未读 ref source; §10 仅 category-level |

## 14. Source files read + 中文摘要

### Source files read (synthesis lane)
- docs/specs/trust-chain-user-verifiable-ledger.md (F-TRUST-001 closure partner)
- docs/specs/voucher-system.md (F-BILL-002 cross-ref)
- backend/sql/migrations/0013_trust_chain_audit_ledger.up.sql (audit_ledger_entries anchor)
- backend/sql/migrations/0023_voucher_system.up.sql (voucher cross-ref)
- docs/process/plans/2026-05-16-f-audit-001-spec-claude.md (Claude lane parallel-draft)
- /tmp/codex-f-audit-001-spec-codex-draft.md (Codex lane parallel-draft, 1.4MB)
- memory: `project_core_trust_chain_differentiator`

### Synthesis decisions (Claude + Codex diff)
- 取 Codex: derived-first 架构 (D-AUDIT-8 OCAW); 6 validation_state (provisional / not_billable / receipt_unavailable 等防 false-valid); 16 AT (vs Claude 10); detached verify 不查 DB 防 request oracle; usage_source 标 authoritative/provisional/inferred; aborted claim not_billable; late reconciliation 不 mutate; decimal precision PostgreSQL numeric; append-only trigger (跟 F-TRUST 同 pattern)
- 取 Claude: receipt schema 字段细 (cost subfield); 7 OCAW (codex 加 D-AUDIT-8 architecture OCAW → 8 total); HUAKAI 独有列表 (per-vendor free tier 等); 实施 Phase AUDIT-1 sub-phase
- 合并: 14 章 + Codex 取 derived-first + 验证 state 细分; Claude 取 schema 字段 + OCAW + Phase

### OWNER 中文摘要
F-AUDIT-001 用户消费透明 synthesis spec 落档. 6 大差异化 6 实施. 关键设计 = derived-first receipt (避免 dual billing ledger inconsistency) + per-request user-facing cost receipt + ed25519 sign (复用 F-TRUST keypair) + 6 validation_state 防 false-valid + mismatch 自动退款 + public pricing endpoint + 用户 dispute API + 16 AT 覆盖. 3 新表 (user_cost_receipts append-only snapshot + pricing_rate_tables immutable historical + cost_disputes user-initiated, DR-001 tenant-aware). 跟 F-TRUST-001 (verdict 联动 mismatch refund) + F-BILL-002 (voucher 退款 origin) + F-PRIV-001 (redaction allowlist) 闭环. Phase AUDIT-1 (5 sub-phase 7-11 天 codex). 8 Owner OCAW 含商业策略 + 架构决策. 风险表 16 项含 HIGH 7. HUAKAI 跟所有现有 gateway 差异 = 用户 cryptographic verify cost + mismatch 自动退款 + public pricing + dispute API + derived-first 防 dual ledger.
