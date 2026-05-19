# F-AUDIT-001 User Consumption Transparency Spec — Claude Lane Draft

| 字段 | 值 |
|---|---|
| Feature ID | F-AUDIT-001 user consumption transparency (6 大差异化 6) |
| Lane | Claude PM-Orchestrator parallel-draft (跟 codex specifier lane parallel per CLAUDE.md #10; final spec 等 synthesis) |
| Plan path | `docs/process/plans/2026-05-16-f-audit-001-spec-claude.md` (本文件) |
| Intended final path after synthesis | `docs/specs/user-consumption-transparency.md` |
| Closure partner | F-TRUST-001 [[trust-chain-user-verifiable-ledger]] / F-PRIV-001 (并行 lane) / F-BILL-001 settler / F-BILL-002 voucher |
| Memory ref | [[project_core_trust_chain_differentiator]] [[feedback_huakai_better_than_sub2api]] |
| UTC | 2026-05-16T10:35:00Z |

## 1. 问题陈述

现有所有 AI gateway 的计费是 **operator 单方账本** — 用户拿到月底账单不知:
- 每 request 上游真实返回多少 token (operator 可虚报)
- substitution (e.g. opus → sonnet) 是按 sonnet rate 退差价还是按 opus 全收
- cache hit 是 0 charge 还是部分收
- retry attempt 是 1 次计费还是 N 次计费
- 上游 free tier (e.g. Gemini free quota) 是否被 operator 当付费卖

HUAKAI 6 大差异化 6: **用户消费透明** — 用户能验证 cost = upstream reported tokens × rate; mismatch 自动退款.

F-AUDIT-001 实施 **6 透明** = per-request user-facing cost receipt + cost validation API + mismatch 自动退款规则.

## 2. 透明项

每 user request 必须透明的 cost 维度:

| 透明项 | 含义 | 用户验证用途 |
|---|---|---|
| `input_token_count` | 实际计费 input token | 验证 = 上游 API 返回的 prompt_tokens (跟 F-TRUST-001 model_chain.upstream_reported 一致) |
| `output_token_count` | 实际计费 output token | 同, 上游 completion_tokens |
| `cache_read_token_count` | cache hit input token | 验证按 D-AUDIT-1 OCAW 决定 charge ratio (默认 0) |
| `cache_write_token_count` | cache write input token | 验证按 D-AUDIT-1 charge ratio (默认 1x) |
| `cost_rate_version` | rate table 版本 (e.g. anthropic_2025_q1_v3) | 用户从 `/v1/pricing/{version}` 拿 rate table verify cost = tokens × rate |
| `cost_total_microcents` | 本 request 总 cost (microcents = 1/1000 cent) | 用户 client side 计算 cost_total = sum(tokens × rate) match |
| `model_actual_billed` | 实际计费按哪个 model 的 rate | 跟 F-TRUST-001 model_chain.route_decided 一致; mismatch 走 D-AUDIT-2 退款 |
| `retry_attempt_count` | 本 request retry 次数 | 按 D-AUDIT-3 决定全计费/仅 final attempt 计费 |
| `substitution_refund_microcents` | substitution 退款 (e.g. opus → sonnet 退差价) | 透明显示退款金额 |
| `voucher_redeemed_microcents` | voucher 抵扣 (F-BILL-002 联动) | 跟 voucher_redemption_events.id cross-ref |
| `upstream_free_tier_used` | 本 request 是否走上游 free quota | bool; true 时 operator policy 决定是否 0 charge user |

## 3. Receipt 结构

每 user request 产生 1 个 `UserCostReceipt`:

```json
{
  "schema_version": "audit.cost.v1",
  "receipt_id": "rcpt_xxxxx",
  "request_id": "req_xxxxx",                       // 跟 F-TRUST-001 audit_ledger_entries.request_id 一致
  "tenant_scope_ref": "tenant-local-ref",
  "occurred_at": "2026-05-16T...",
  "ledger_id": "ldg_xxxxx",                        // F-TRUST-001 ledger_id cross-ref
  "billing_event_id": "bev_xxxxx",                 // F-BILL-001 billing_events.id cross-ref
  "cost": {
    "input_token_count": 1234,
    "output_token_count": 567,
    "cache_read_token_count": 0,
    "cache_write_token_count": 0,
    "cost_rate_version": "anthropic_2025_q1_v3",
    "cost_total_microcents": 1850,
    "model_actual_billed": "claude-opus-4-7",
    "retry_attempt_count": 1,
    "substitution_refund_microcents": 0,
    "voucher_redeemed_microcents": 0,
    "upstream_free_tier_used": false
  },
  "verdict": "match" | "substitution_refund" | "mismatch_refund_pending" | "unknown",
  "signature": "ed25519-base64"                    // 复用 F-TRUST-001 ed25519 keypair, 防 operator 篡改 receipt
}
```

verdict:
- `match`: cost calc 一致 + F-TRUST-001 model_chain.verdict=match (绿)
- `substitution_refund`: F-TRUST-001 verdict=allowed_alias, substitution_refund_microcents > 0 (按差价)
- `mismatch_refund_pending`: F-TRUST-001 verdict=mismatch (红), 自动触发退款 workflow (D-AUDIT-2)
- `unknown`: F-TRUST-001 verdict=unknown (灰), receipt 标 pending 直到 stream 关闭 / upstream 报 model

Receipt 严禁含 prompt/completion content (F-PRIV-001 redaction allowlist 一致, content 永不进 receipt).

## 4. Cost Validation

用户 client-side 验证 cost:

```
cost_expected = (input_token_count × input_rate
                + output_token_count × output_rate
                + cache_read_token_count × cache_read_rate
                + cache_write_token_count × cache_write_rate) × model_actual_billed_multiplier
                - substitution_refund_microcents
                - voucher_redeemed_microcents

assert(cost_expected == cost_total_microcents)
```

`rate` 从 `/v1/pricing/{cost_rate_version}` 拿 (public endpoint, 可缓存).

**Mismatch 自动退款 workflow (D-AUDIT-2 OCAW)**:
- F-TRUST-001 model_chain.verdict=mismatch 触发 → F-AUDIT-001 `mismatch_refund_pending` verdict
- 退款金额 = (requested model rate - actual_billed model rate) × token_count, 上限 ≤ original cost
- 退款记 voucher_redemption_events.refund_origin='F-AUDIT-001-mismatch' or 直接退用户余额
- 操作员可 admin 复审 (admin can confirm/reject within 24h, default auto-approve)

## 5. User Verification API

**Endpoint 1 — `GET /v1/ledger/cost/{request_id}`** (tenant-scoped, F-SESSION-001 bearer):
- Response: 完整 `UserCostReceipt`
- 用户验证 cost 计算

**Endpoint 2 — `GET /v1/pricing/{cost_rate_version}`** (public, 可缓存):
- Response: full rate table (per model + per token-type)
- 用户 client side 计算 expected cost

**Endpoint 3 — `GET /v1/ledger/cost-summary`** (tenant-scoped, F-SESSION-001 bearer):
- Query: `?from=<date>&to=<date>&granularity=day|hour`
- Response: aggregate cost + count + verdict breakdown
- 用户 dashboard 显示自家用量 trend

**Endpoint 4 — `POST /v1/ledger/cost/{request_id}/dispute`** (tenant-scoped):
- Body: `{user_calculated_cost_microcents, evidence}`
- Response: dispute_id + admin 审核 workflow trigger
- 用户发起争议 (即使 verdict=match 也允许 — 防 system bug 漏报)

## 6. 实施机制

### 6.1 Cost Calculator
- 复用 F-BILL-001 settler 内部 cost 计算逻辑
- 添加 `BuildUserCostReceipt(billing_event_id) → UserCostReceipt` formatter
- 跟 F-TRUST-001 `BuildLedgerEntry()` 同步 (1 request → 1 ledger + 1 receipt + 1 billing_event)

### 6.2 Receipt Formatter
- 输出 schema 严格 (跟 §3 一致, schema_version 强制)
- 经 F-PRIV-001 `Redactor.SanitizePayload` (虽然 receipt 本身白名单, double check)
- ed25519 sign 复用 F-TRUST-001 keypair (省一套 KMS)

### 6.3 Mismatch Refund Workflow
- F-TRUST-001 writer 写 ledger 后 trigger F-AUDIT-001 receipt 生成
- verdict=mismatch → 写 receipt + 触发 refund worker (异步 outbox 跟 F-OBS-005 DLQ 共 runtime)
- refund worker: 算退款金额 + 写 voucher_redemption_events 或 直接 balance credit + 写 admin alert

### 6.4 Pricing Endpoint
- `pricing_rate_tables` 表 (新): `{cost_rate_version PK, model, input_rate_micro, output_rate_micro, cache_read_rate_micro, cache_write_rate_micro, valid_from, valid_until}`
- admin CRUD 5 endpoint; public GET endpoint
- rate 改动通过 new version (不动旧 version 防溯及既往)

## 7. Cross-Chain Reference

| Family | 关系 |
|---|---|
| F-TRUST-001 | F-AUDIT receipt 跟 F-TRUST ledger 同 request_id; receipt.ledger_id cross-ref; F-AUDIT verdict 依赖 F-TRUST model_chain.verdict (mismatch → refund); receipt ed25519 sign 复用 F-TRUST keypair |
| F-PRIV-001 | receipt 字段严格 redaction allowlist; pre-write check 经 Redactor |
| F-BILL-001 settler | F-AUDIT receipt 跟 billing_events 同 request_id; cost calc 复用 settler 逻辑 |
| F-BILL-002 voucher | refund 可走 voucher_redemption_events.refund_origin='F-AUDIT-001-mismatch'; voucher_redeemed_microcents 跨 ref |
| F-CH-002 channel health | channel_id 在 receipt.cost 不暴露 (是 system internal), 但 admin dashboard 可见 (跟 channel cooldown 关联看错误率影响 cost) |
| F-ADV-001 / F-FP-001 / F-PACE-001 / F-NET-001 | 不直接 cross-ref (反代各层不影响 user cost) |

## 8. Storage

新表 `user_cost_receipts`:
```sql
CREATE TABLE user_cost_receipts (
  id                    BIGSERIAL PRIMARY KEY,
  receipt_id            TEXT NOT NULL UNIQUE,
  request_id            TEXT NOT NULL UNIQUE REFERENCES audit_ledger_entries(request_id),  -- 1-to-1 with F-TRUST ledger
  tenant_id             BIGINT NOT NULL REFERENCES tenants(id),  -- DR-001
  occurred_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  ledger_id             TEXT REFERENCES audit_ledger_entries(ledger_id),
  billing_event_id      TEXT,  -- ref billing_events.id (loose FK, 跟 F-BILL-001 schema 一致)
  schema_version        TEXT NOT NULL DEFAULT 'audit.cost.v1',
  cost_payload          JSONB NOT NULL,  -- §3 cost subfield, redaction enforced
  verdict               TEXT NOT NULL CHECK (verdict IN ('match','substitution_refund','mismatch_refund_pending','unknown')),
  signature             TEXT NOT NULL,
  pubkey_fingerprint    TEXT NOT NULL  -- 复用 F-TRUST-001 pubkey
);
CREATE INDEX idx_user_cost_receipts_tenant_time ON user_cost_receipts (tenant_id, occurred_at DESC);
CREATE INDEX idx_user_cost_receipts_request ON user_cost_receipts (request_id);
CREATE INDEX idx_user_cost_receipts_verdict ON user_cost_receipts (verdict) WHERE verdict != 'match';
```

新表 `pricing_rate_tables`:
```sql
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
CREATE INDEX idx_pricing_active ON pricing_rate_tables (cost_rate_version, model) WHERE valid_until IS NULL;
```

新表 `cost_disputes` (用户争议):
```sql
CREATE TABLE cost_disputes (
  id                    BIGSERIAL PRIMARY KEY,
  dispute_id            TEXT NOT NULL UNIQUE,
  tenant_id             BIGINT NOT NULL REFERENCES tenants(id),
  receipt_id            TEXT NOT NULL REFERENCES user_cost_receipts(receipt_id),
  user_calculated_cost  NUMERIC NOT NULL,
  evidence              JSONB,  -- redacted; 用户提供的 rate version + 计算过程
  status                TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved_refund','rejected','closed')),
  created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  resolved_at           TIMESTAMPTZ,
  admin_notes           TEXT
);
CREATE INDEX idx_cost_disputes_tenant_status ON cost_disputes (tenant_id, status, created_at DESC);
```

## 9. 实施 Phase (Phase AUDIT-1)

- **Phase AUDIT-1-A** (1-2 天 codex): migration 0025 (假设 voucher 已占 0023, PKCE 0024) — 3 表 schema + Go types
- **Phase AUDIT-1-B** (2-3 天): Cost Calculator (复用 F-BILL-001) + Receipt Formatter + ed25519 sign (复用 F-TRUST-001 keypair)
- **Phase AUDIT-1-C** (2 天): 4 user verification endpoint (cost / pricing / cost-summary / dispute) + admin pricing CRUD
- **Phase AUDIT-1-D** (1-2 天): Mismatch refund workflow (异步 outbox, 跟 F-OBS-005 DLQ 共 runtime)
- **Phase AUDIT-1-E** (1-2 天): AT-AUDIT-001 测试 + admin transparency dashboard (cost mismatch ratio / refund total / dispute count)

## 10. 跟其它项目对比 (HUAKAI 强差异化)

| 项目类别 | 计费透明度 | HUAKAI 升级 |
|---|---|---|
| operator-only billing (sub2api / new-api / one-api) | 用户看月底总账; 不知 per-request token 数; 不能验证 | HUAKAI per-request user-facing receipt + ed25519 sign + 用户 client side verify |
| observability tracing (litellm / portkey / helicone) | 有 per-request cost trace 但 operator-visible only | HUAKAI 用户可见 + 可 verify + dispute |
| 云厂 gateway (Bedrock / Azure / Vertex) | 上游账单, caller 必须信任云厂报数 | HUAKAI 把上游 token count 暴露到用户 receipt, cross-check upstream API self-reported |
| stripe / 商业 metering | 用户可见 invoice 但无法 verify per-event 计算 | HUAKAI 用户 client side 算 expected cost = tokens × rate (rate 公开), match receipt |

**HUAKAI F-AUDIT-001 独有**:
- per-request user-facing cost receipt + ed25519 sign (用户 verify operator 没虚报)
- mismatch verdict 自动退款 (D-AUDIT-2 OCAW 触发)
- public pricing endpoint + version (用户拿 rate table 可独立计算)
- substitution refund 透明 (allowed_alias 时按差价退)
- cache hit charging 默认 0 (D-AUDIT-1 OCAW)
- cost dispute API (用户可发争议即便 verdict=match)
- upstream_free_tier_used 标识 (operator policy 决定是否 0 charge)

## 11. Owner 后续 OCAW

- (D-AUDIT-1) **cache hit charging 默认** — 0 (推, HUAKAI 差异化) / 50% / 100%? 影响商业策略
- (D-AUDIT-2) **mismatch 自动退款触发阈值** — 立刻退 / 累积 N 次 / 仅 admin 手动? 影响用户信任 vs operator 风险
- (D-AUDIT-3) **retry attempt 计费** — 每 attempt 全计 / 仅 final attempt 计 / 失败 attempt 0 charge?
- (D-AUDIT-4) **upstream free tier 是否 0 charge user** — 是 (推, HUAKAI 透明 — 不能"白嫖上游加价卖") / 否 (operator 收 markup)
- (D-AUDIT-5) **receipt 默认状态** — 自动生成 (推) / opt-in only / opt-out only? 影响 storage cost
- (D-AUDIT-6) **dispute auto-approve 时间** — 24h / 72h / 7 天?
- (D-AUDIT-7) **cost_rate_version 改动是否溯及既往** — 不溯及 (推, 老 entry 用老 version) / 溯及到当月? 影响计费稳定性

## 12. Acceptance test outline (AT-AUDIT-001-001..010, 加进 docs/11_ACCEPTANCE_TEST_MATRIX.md)

- AT-AUDIT-001-001: 每 user request 生成 1 row user_cost_receipts + receipt_id UNIQUE + tenant_id 一致
- AT-AUDIT-001-002: cost_total 跟 sum(token × rate) match (verdict=match); 引 F-TRUST-001 ledger_id cross-ref
- AT-AUDIT-001-003: F-TRUST verdict=allowed_alias → F-AUDIT verdict=substitution_refund + substitution_refund_microcents > 0 (按差价)
- AT-AUDIT-001-004: F-TRUST verdict=mismatch → F-AUDIT verdict=mismatch_refund_pending + refund worker 触发 + voucher credit
- AT-AUDIT-001-005: GET /v1/ledger/cost/{request_id} 返完整 receipt + client side cost calc match
- AT-AUDIT-001-006: GET /v1/pricing/{version} 返 rate table + 老 version 仍可查 (不溯及既往)
- AT-AUDIT-001-007: GET /v1/ledger/cost-summary?from&to 返 aggregate cost + verdict breakdown
- AT-AUDIT-001-008: POST /v1/ledger/cost/{request_id}/dispute → dispute_id + admin workflow + 24h auto-approve (D-AUDIT-6)
- AT-AUDIT-001-009: cross-tenant — tenant A 查 tenant B request_id → 404
- AT-AUDIT-001-010: receipt 字段无 user prompt / completion / token / cookie (F-PRIV-001 redaction enforced)

## 13. 风险表

| 风险 | Severity | 缓解 |
|---|---|---|
| cost calc 错 (HUAKAI 实施 bug 致计费虚高) | HIGH | F-BILL-001 settler 单测 + AT-AUDIT-001-002 验证 + dispute API 用户兜底 |
| upstream 报 token 不准 (vendor bug or 故意) | MED | 多 vendor 对比 token count vs 历史 baseline; admin alert > X% drift |
| Mismatch refund 滥用 (恶意构造 mismatch 套退款) | MED | refund 上限 ≤ original cost; admin manual review 大额; refund_origin audit |
| pricing rate table 版本管理混乱 (多 version 重叠时 cost calc 错) | HIGH | valid_from + valid_until 严格; AT-AUDIT-001-006 测试; admin UI 显示 active version |
| dispute API 滥用 (用户每 request 都发 dispute) | LOW | per-tenant rate limit + auto-reject 已 resolved 的重复 dispute |
| receipt 体积大 (每 request 1 row) | MED | per-tenant cold archive (90 天后 archive); 不影响验证 (历史 row 永久可查) |
| ed25519 sign 性能 (每 request sign) | MED | 复用 F-TRUST-001 KMS pipeline; benchmark + async sign if needed |
| upstream_free_tier_used 误判 (HUAKAI 不知道上游是否真免费) | MED | per-vendor free tier rules 配置; admin alert 异常率高 |
| cache hit charging 改动追溯 (D-AUDIT-1 改 default 0→50%) | HIGH | 改 default 必须 new cost_rate_version + admin 通告 + grace period |
| operator 故意篡改 receipt | HIGH | ed25519 sign + Merkle chain (跟 F-TRUST-001 共享); user dispute API 兜底 |

## 14. Source files read + 中文摘要

### Source files read (Claude lane plan-then-draft)
- docs/specs/trust-chain-user-verifiable-ledger.md (F-TRUST-001 closure partner; D-TRUST-6 mismatch refund OCAW 联动)
- docs/specs/voucher-system.md (F-BILL-002 spec, refund 走 voucher 借)
- docs/03_FEATURE_PARITY_MATRIX.md + docs/11_ACCEPTANCE_TEST_MATRIX.md (matrix format 锚定)
- memory: `project_core_trust_chain_differentiator` (6 大差异化 6 用户消费透明)
- (假设) backend/sql/migrations 含 F-BILL-001 billing_events + usage_record (cross-ref)
- 不读任何上游项目源码 (clean-room)

### OWNER 中文摘要

F-AUDIT-001 用户消费透明 spec Claude lane draft 落档. HUAKAI 6 大差异化 6 实施 spec. 关键 = per-request user-facing cost receipt + ed25519 sign (复用 F-TRUST keypair) + mismatch verdict 自动退款 + public pricing endpoint + 用户 dispute API. 3 新表 (user_cost_receipts + pricing_rate_tables + cost_disputes, DR-001 tenant-aware). Receipt 严格 redaction allowlist (跟 F-PRIV-001 一致, 不含 prompt/completion). 跟 F-TRUST-001 model_chain.verdict 联动 (mismatch → 退款), 跟 F-BILL-002 voucher 联动 (refund 走 voucher credit). Phase AUDIT-1 (5 sub-phase 7-11 天 codex). 7 Owner OCAW 含商业策略 (cache hit charging default / mismatch 退款阈值 / retry attempt 计费 / upstream free tier / receipt 默认 / dispute auto-approve / rate version 溯及). AT-AUDIT-001-001..010. 风险表 10 项含 HIGH (cost calc bug / pricing version 混乱 / operator 篡改 receipt / cache hit charging 改动追溯). HUAKAI 跟所有现有 gateway 差异 = 用户 cryptographic verify cost + mismatch 自动退款 + public pricing + dispute API. Phase 6 商业基础 + Trust family 闭环.
