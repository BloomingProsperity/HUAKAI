# HUAKAI 前端工程 Brief — Gemini 项目总览（2026-05-12）

**目标读者**: Gemini AI（前端工程师），不需要阅读本仓库源码  
**形式**: 自包含的 markdown brief，包含所有 API、数据模型、业务逻辑、设计定位  
**有效期**: Phase C / N+5b 代码状态（2026-05-09 commit 5d1fbd7 后）

---

## 1. 产品定位与目标

### 1.1 一句话定位

**HUAKAI** 是一个 MIT-compatible AI 网关 + 账号中心 + 管理运营平台，融合 Sub2API / New API / One API / Portkey / Helicone / LiteLLM / Envoy AI Gateway 等 8 个开源项目的核心算法，目标是**多账号套利 + SaaS 运营双模式**（[CLAUDE.md](../CLAUDE.md) §Mission；[01_PROJECT_BRIEF.md](../01_PROJECT_BRIEF.md) §Owner-Stated Goal）。

### 1.2 产品模式

两个独立的商业版本，共用一套代码库（[DR-002 Product Editions](../process/decisions/DR-002-product-editions.md)）：

| 版本 | 用户 | 收入模式 | 界面需求 |
|------|------|---------|---------|
| **Personal Edition** | 个人运营者 | 自部署卖 API（token 套利） | Dashboard + Provider 账号管理 + User API Key 管理 + Usage 看板 |
| **SaaS Edition** | 多租户管理平台 | 向租户收费 | + Tenant 管理 + Per-Tenant 隔离界面 |

当前**仅规划 Personal Edition**；SaaS Edition 是 Phase 10+ 路线图项（[16_PHASED_DELIVERY_PLAN.md](../16_PHASED_DELIVERY_PLAN.md) §Phase 0-9）。

### 1.3 核心价值

**Relay-Station（中转站）模式**：运营者自备多个上游订阅（如 Anthropic Pro 20/月 + Gemini Advanced 20/月 + Azure OpenAI 按量），HUAKAI 将这些账号池化为一个逻辑容量，end-user 通过 HUAKAI-issued API Key 消费这个池子，HUAKAI 从中获得套利收益或为租户提供 SaaS 运营壳（[01_PROJECT_BRIEF.md](../01_PROJECT_BRIEF.md) §Product Identity）。

---

## 2. 后端 HTTP API 表面

### 2.1 三大路由族

#### 客户端入口 `/v1/*` — OpenAI 兼容协议

用户通过 HUAKAI-issued API Key 访问；支持流式；响应规范化为 OpenAI Chat Completions / Anthropic Messages / Bedrock 标准格式。

**已实现的端点**（[backend/cmd/gateway/main.go](../backend/cmd/gateway/main.go) L30-45 imports；[docs/specs/api-contract.md](../specs/api-contract.md) §Capability）：

| 方法 | 路径 | 功能 | 入参 | 出参 |
|------|------|------|------|------|
| **POST** | `/v1/chat/completions` | OpenAI 兼容聊天 | model, messages, temperature, stream, ... | ChatCompletion \| Stream[SSE] |
| **POST** | `/v1/messages` | Anthropic Messages 直转发 | model, max_tokens, messages, ... | Message \| Stream[SSE] |
| **POST** | `/v1/responses` | Bedrock Converse 适配（预留） | — | — |

**鉴权**: 请求头 `Authorization: Bearer <HUAKAI_API_KEY>`，由 `internal/auth.APIKeyResolver` 验证（[backend/internal/auth/api_key_resolver.go](../backend/internal/auth/api_key_resolver.go)；schema: 0007 migration）。

**流式**: Server-Sent Events（SSE），行协议，支持 13 种终态分类（cache_hit / fresh / error / tool_use / ...）（[02_HUAKAI_FUSION_ARCHITECTURE.md](../02_HUAKAI_FUSION_ARCHITECTURE.md) Part B §表 A F-GW-002；[docs/specs/streaming-forwarder.md](../specs/streaming-forwarder.md) §End Class）。

#### 管理员入口 `/admin/v1/*` — 多资源 CRUD + 观测

操作员权限鉴权；两种角色：`tenant_operator`（默认，管理自己的账号/用户）、`platform_admin`（跨租户，DLQ 重放）（[docs/specs/api-contract.md](../specs/api-contract.md) §Actor）。

**已实现的端点群**（按资源分组；[frontend/app/page.tsx](../frontend/app/page.tsx) L6-54 面板定义 + [backend/internal/adminhttp/](../backend/internal/adminhttp/) handlers）：

##### API Key 管理（L0 完成率 ~50%）
| 方法 | 路径 | 功能 | 权限 |
|------|------|------|------|
| **POST** | `/admin/v1/api-keys` | 签发新 API Key（给 end-user） | tenant_operator |
| **GET** | `/admin/v1/api-keys` | 列表查询（按 tenant 隔离） | tenant_operator |
| **GET** | `/admin/v1/api-keys/{id}` | 单个 key 详情（无明文返回） | tenant_operator |
| **POST** | `/admin/v1/api-keys/{id}/revoke` | 撤销 key，立即失效 | tenant_operator |

实现位置: [backend/internal/adminhttp/api_keys_handler.go](../backend/internal/adminhttp/api_keys_handler.go)（POST/GET/revoke）；schema: 0007 (`api_keys` + `users` 表，bcrypt 密码哈希，[02_HUAKAI_FUSION_ARCHITECTURE.md](../02_HUAKAI_FUSION_ARCHITECTURE.md) L0-1/L0-2）。

##### Provider 账号管理（L1 实现）
| 方法 | 路径 | 功能 | 鉴权 | 示例 |
|------|------|------|------|------|
| **GET** | `/admin/v1/provider-accounts` | 列表（含凭证状态、quota、health） | tenant_operator | 显示 Anthropic Pro / Gemini Advanced 账号 |
| **POST** | `/admin/v1/provider-accounts` | 新增账号（粘贴 token / OAuth link） | tenant_operator | 粘贴 Claude API key，系统自动刷新管理 |
| **PATCH** | `/admin/v1/provider-accounts/{id}` | 修改（enable/disable、cap 调整） | tenant_operator | 关闭过期账号；调 concurrency 上限 |
| **POST** | `/admin/v1/provider-accounts/{id}/clear-rate-limit` | 重置速率限制状态 | tenant_operator | — |

schema: 0001（[0001_pool_routing.up.sql](../backend/sql/migrations/0001_pool_routing.up.sql) L104-207 `provider_accounts` 表）  
关键字段: `account_type` (oauth/api_key/service_account) + `credential_state` (valid/refreshing/failed/revoked) + `health_state` (operational/degraded/failed/cooling_down) + `in_flight_count` (并发计数) + `cap_concurrency` (并发上限) + `quota_status` (active/exhausted) — 对应 F-POOL-001 §Phase A 9-gate 链（[02_HUAKAI_FUSION_ARCHITECTURE.md](../02_HUAKAI_FUSION_ARCHITECTURE.md) L86-97 渠道调度轴）。

##### Pool / Channel 管理（L1 实现）
| 方法 | 路径 | 功能 |
|------|------|------|
| **GET** | `/admin/v1/pools` | 列表 Pool Group（逻辑容量分组） |
| **POST** | `/admin/v1/pools` | 创建 Pool Group（如"Anthropic Pro Pool") |
| **PATCH** | `/admin/v1/pools/{id}` | 修改 Pool 配置（routing_policy, top_k, wait budget) |
| **GET** | `/admin/v1/pools/{id}/channels` | 该 Pool 下的 Channel 列表 |
| **POST** | `/admin/v1/pools/{id}/channels` | 新增 Channel（failover status code list 可定制） |

schema: 0001（`pool_groups` L52-81；`channels` L88-101）  
关键逻辑: 每个 Pool 有 `routing_policy_version` + `top_k_default` + `sticky_wait_*` + `fallback_wait_*` 等参数，驱动账号选中算法（[02_HUAKAI_FUSION_ARCHITECTURE.md](../02_HUAKAI_FUSION_ARCHITECTURE.md) L86-97 Sub2API 5 层+9-gate）。

##### Usage 和 Billing（L1 后端 70% 完成）
| 方法 | 路径 | 功能 | 返回字段 |
|------|------|------|---------|
| **GET** | `/admin/v1/usage` | 聚合查询（按 tenant/user/account/model 维度） | token_count, cost, request_count, cache_hit_rate |
| **GET** | `/admin/v1/billing/claims` | Claim 行列表（Tx1 预留阶段） | claim_id, api_key_id, predicted_cost, status |
| **GET** | `/admin/v1/billing/events` | 结算事件审计日志 | claim_id, actual_cost, event_type |

实现位置: [backend/internal/obs/](../backend/internal/obs/) （Slice 4 已落，commit 5d1fbd7）；schema: 0002（[0002_observability_billing.up.sql](../backend/sql/migrations/0002_observability_billing.up.sql)）  
关键表: `usage_records`（每次请求一行，token 粒度）+ `billing_ledger_claims`（money-grade Tx1）+ `billing_events`（审计）。

##### 其他端点（预留/Mock）
| 方法 | 路径 | 功能 | 状态 |
|------|------|------|------|
| **GET** | `/admin/v1/auth-credentials/{id}/renew-status` | OAuth token 刷新状态 | Mock（#5 面板） |
| **POST** | `/admin/v1/auth-credentials/{id}/renew` | 手动触发刷新 | Mock（#5 面板） |
| **GET** | `/admin/v1/mimicry-profiles` | Mimicry Plan（强伪装 6-step） | Mock（#6 面板） |
| **PATCH** | `/admin/v1/mimicry-profiles/{id}` | 编辑伪装配置 | Mock（#6 面板） |

#### 调试入口 `/debug/*` — 实时可观测性

Go `expvar` 标准库；轮询间隔 2 秒（[frontend/app/observability/page.tsx](../frontend/app/observability/page.tsx) L88 POLL_INTERVAL_MS）。

| 端点 | 功能 | 返回数据结构 |
|------|------|------------|
| **GET** | `/debug/vars` | 实时计数器快照（嵌套 JSON） | `{cache_token_count: {creation_total, read_total, request_count}, cache_token_count_by_account: {<account_id>: {...}}, ...}` |

当前展示: Cache token 创建 vs 命中率（cache_hit_audit 分项；[02_HUAKAI_FUSION_ARCHITECTURE.md](../02_HUAKAI_FUSION_ARCHITECTURE.md) Part A 异步轴 0%）。

### 2.2 共同错误信封

所有响应（成功/失败）使用统一 `ErrorResponse` envelope（[docs/specs/api-contract.md](../specs/api-contract.md) §Failure Path）：

```typescript
interface ErrorResponse {
  error: {
    code: string;  // HUAKAI typed enum (e.g. QUOTA_EXHAUSTED, RATE_LIMIT_5H_EXCEEDED)
    message: string;  // human-readable
    request_id?: string;  // correlation ID
    retry_after_seconds?: number;  // 429/503 only
    protocol_loss?: string[];  // per F-PROTO-002
    details?: unknown;
  };
}
```

**标准 HTTP 状态码映射**（[docs/specs/api-contract.md](../specs/api-contract.md) §Failure Path）：
- **400 Bad Request**: 入参校验失败
- **401 Unauthorized**: API Key 无效 / 过期
- **402 Payment Required**: 额度不足（SaaS Edition）
- **403 Forbidden**: 权限不足（admin endpoint 角色检查）
- **404 Not Found**: 资源不存在
- **409 Conflict**: 幂等性冲突（FINGERPRINT_CONFLICT）
- **429 Too Many Requests**: 速率限制（F-RATE-001；附加 Retry-After）
- **503 Service Unavailable**: 上游不可用；附加 Retry-After

---

## 3. 数据模型 / 核心实体

### 3.1 主要表结构（schema fragments）

所有表均携带 `tenant_id` 用于多租户隔离（[DR-001 Multi-Tenancy](../process/decisions/DR-001-multi-tenancy.md)）；金额字段统一 `numeric(20,8)`（精度至 satoshis）；敏感凭证加密存储（[DR-006 Database](../process/decisions/DR-006-database.md)）。

#### tenants（租户）
```sql
-- 0001 migration
CREATE TABLE tenants (
  id          bigserial PRIMARY KEY,
  name        text NOT NULL UNIQUE,  -- 'default' for Personal Edition MVP
  status      text DEFAULT 'active',
  created_at  timestamptz DEFAULT now(),
  updated_at  timestamptz
);
```

#### users（end-user）
```sql
-- 0007 migration （L0 完成）
CREATE TABLE users (
  id           bigserial PRIMARY KEY,
  tenant_id    bigint NOT NULL REFERENCES tenants(id),
  email        text NOT NULL,
  name         text,
  status       text DEFAULT 'active',
  balance      numeric(20,8) DEFAULT 0,  -- wallet balance for deduction model
  created_at   timestamptz DEFAULT now()
);
CREATE UNIQUE INDEX uq_users_tenant_email ON users(tenant_id, email);
```

#### api_keys（用户 API 凭证）
```sql
-- 0007 migration （L0 完成）
CREATE TABLE api_keys (
  id            bigserial PRIMARY KEY,
  tenant_id     bigint NOT NULL REFERENCES tenants(id),
  user_id       bigint NOT NULL REFERENCES users(id),
  key_prefix    text NOT NULL,  -- first 8 chars; exposed in UI
  key_hash      bytea NOT NULL,  -- bcrypt
  name          text,            -- label for operator
  status        text DEFAULT 'active',  -- active/revoked/expired
  expires_at    timestamptz,
  created_at    timestamptz DEFAULT now(),
  revoked_at    timestamptz
);
CREATE UNIQUE INDEX uq_api_keys_tenant_prefix ON api_keys(tenant_id, key_prefix);
```

**重要**: 完整的 plaintext API Key 仅在 POST `/admin/v1/api-keys` 响应体中返回一次；之后所有存储/展示使用 prefix（前 8 字符）+ 省略号（如 `sk-ant-xxxxxx****`）（[backend/internal/adminhttp/api_keys_handler.go](../backend/internal/adminhttp/api_keys_handler.go) CMB-5 约束）。

#### providers（上游供应商目录）
```sql
-- 0001 migration
CREATE TABLE providers (
  id                   bigserial PRIMARY KEY,
  tenant_id            bigint NOT NULL REFERENCES tenants(id),
  code                 text NOT NULL,  -- 'anthropic', 'openai', 'gemini', 'bedrock'
  display_name         text NOT NULL,
  upstream_protocol    text NOT NULL,  -- 'anthropic_messages', 'openai_chat', 'openai_responses', 'gemini', 'bedrock'
  enabled              boolean DEFAULT true,
  created_at           timestamptz DEFAULT now()
);
CREATE UNIQUE INDEX uq_providers_tenant_code ON providers(tenant_id, code);
```

#### pool_groups（逻辑容量分组 — relay-station 核心）
```sql
-- 0001 migration
CREATE TABLE pool_groups (
  id                        bigserial PRIMARY KEY,
  tenant_id                 bigint NOT NULL REFERENCES tenants(id),
  name                      text NOT NULL,  -- 'Anthropic Pro Pool', 'Azure OpenAI'
  routing_policy_version    text DEFAULT '1.0',
  top_k_default             integer DEFAULT 1 CHECK (1 <= top_k_default AND top_k_default <= 10),
  capability_default        text DEFAULT 'exact_capability_only',
  allow_last_resort         boolean DEFAULT false,
  -- wait budgets (F-POOL-001 §sticky vs fallback)
  sticky_wait_max_waiting   integer DEFAULT 2,
  fallback_wait_max_waiting integer DEFAULT 8,
  sticky_wait_timeout_ms    integer DEFAULT 5000,
  fallback_wait_timeout_ms  integer DEFAULT 30000,
  enabled                   boolean DEFAULT true,
  created_at                timestamptz DEFAULT now()
);
```

#### channels（Pool 内的子路由过滤）
```sql
-- 0001 migration
CREATE TABLE channels (
  id                    bigserial PRIMARY KEY,
  tenant_id             bigint NOT NULL REFERENCES tenants(id),
  pool_group_id         bigint NOT NULL REFERENCES pool_groups(id),
  name                  text NOT NULL,  -- 'fast', 'reliable', 'cheapest'
  failover_status_codes integer[] DEFAULT ARRAY[401, 403, 429, 529],  -- HUAKAI improvement
  enabled               boolean DEFAULT true,
  created_at            timestamptz DEFAULT now()
);
```

#### provider_accounts（上游凭证 + 容量单位）
```sql
-- 0001 migration （核心）；0006 migration（凭证加密）；0012 migration（proxy_url）
CREATE TABLE provider_accounts (
  id                      bigserial PRIMARY KEY,
  tenant_id               bigint NOT NULL REFERENCES tenants(id),
  provider_id             bigint NOT NULL REFERENCES providers(id),
  channel_id              bigint NOT NULL REFERENCES channels(id),
  name                    text NOT NULL,
  account_type            text NOT NULL CHECK (account_type IN ('oauth', 'api_key', 'service_account')),
  enabled                 boolean DEFAULT true,
  expires_at              timestamptz,
  
  -- 9-gate chain （F-POOL-001 §Phase A）
  health_state            text DEFAULT 'operational' CHECK (health_state IN ('operational', 'degraded', 'failed', 'cooling_down')),
  health_state_until      timestamptz,
  credential_state        text DEFAULT 'valid' CHECK (credential_state IN ('valid', 'refreshing', 'refresh_failed', 'revoked')),
  credentials             bytea NOT NULL,  -- KMS-encrypted envelope （DR-006）
  
  -- 并发限制（F-POOL-001 §6.13）
  cap_concurrency         integer DEFAULT 4 CHECK (cap_concurrency >= 1),
  in_flight_count         integer DEFAULT 0,  -- Tx1 SELECT FOR UPDATE 竞争
  cap_queue_sticky        integer DEFAULT 2,
  cap_queue_fallback      integer DEFAULT 8,
  
  -- 额度（按 daily/weekly/total 粒度）
  cap_quota_total         numeric(20,8),
  quota_used_total        numeric(20,8) DEFAULT 0,
  cap_quota_daily         numeric(20,8),
  quota_used_daily        numeric(20,8) DEFAULT 0,
  quota_window_daily_start timestamptz,
  quota_status            text DEFAULT 'active' CHECK (quota_status IN ('active', 'exhausted')),
  
  -- 模型白名单
  model_allow_list        text[] DEFAULT ARRAY[]::text[],
  capability_flags        text[] DEFAULT ARRAY[]::text[],  -- 'vision', 'tool_use', 'reasoning_high'
  
  created_at              timestamptz DEFAULT now()
);
```

#### billing_ledger_claims（money-grade Tx1 行 — 唯一金钱源）
```sql
-- 0002 migration
CREATE TABLE billing_ledger_claims (
  id                      bigserial PRIMARY KEY,
  tenant_id               bigint NOT NULL REFERENCES tenants(id),
  idempotency_key         text NOT NULL,  -- HASH(tenant, api_key, request_hash)
  api_key_id              bigint NOT NULL,
  user_id                 bigint NOT NULL,
  logical_request_id      text NOT NULL,  -- request_id
  
  provider_account_id     bigint,  -- NULL until Pool acquire（Pattern B）
  acquisition_token       uuid,    -- Pool 写回
  attempt_seq             integer DEFAULT 1,
  
  predicted_cost          numeric(20,8) DEFAULT 0,
  actual_cost             numeric(20,8),  -- NULL until Tx2 settle
  
  status                  text DEFAULT 'reserving' CHECK (status IN ('reserving', 'committed', 'aborted')),
  aborted_reason          text,
  
  reserved_at             timestamptz DEFAULT now(),
  settled_at              timestamptz,
  lease_expires_at        timestamptz NOT NULL  -- orphan sweep window
);
CREATE UNIQUE INDEX uq_claims_idempotency ON billing_ledger_claims(tenant_id, api_key_id, idempotency_key);
```

**关键不变式**（[02_HUAKAI_FUSION_ARCHITECTURE.md](../02_HUAKAI_FUSION_ARCHITECTURE.md) L72-83）：
- `request_id` — 一次用户请求，全链路唯一
- `attempt_id` — 一次上游尝试（fallback 后多个）
- `lease_id = pool_slot_acquisitions.id` — 一次资源占用
- 审计链: request_id → claim_id → attempt_id → lease_id → usage_record

#### usage_records（观测数据 — 非金钱源）
```sql
-- 0002 migration
CREATE TABLE usage_records (
  id                      bigserial PRIMARY KEY,
  tenant_id               bigint NOT NULL REFERENCES tenants(id),
  claim_id                bigint REFERENCES billing_ledger_claims(id),  -- FK late bind
  api_key_id              bigint NOT NULL,
  user_id                 bigint NOT NULL,
  provider_account_id     bigint NOT NULL,
  request_id              text NOT NULL,
  
  model                   text NOT NULL,
  endpoint_family         text NOT NULL,  -- 'chat', 'messages', 'embeddings'
  
  input_token_count       integer,
  output_token_count      integer,
  cache_creation_tokens   integer,  -- new-api / Claude cache 差价计费
  cache_read_tokens       integer,
  
  cost_input              numeric(20,8),
  cost_output             numeric(20,8),
  cost_cache_creation     numeric(20,8),
  cost_cache_read         numeric(20,8),
  total_cost              numeric(20,8),
  
  end_class               text NOT NULL,  -- 'cache_hit', 'fresh', 'error', 'tool_use', ...（13 classes）
  latency_ms              integer,
  
  created_at              timestamptz DEFAULT now()
);
```

### 3.2 关键关系

```
User (end-user)
  ├─ has-many: APIKey (Platform issues for auth)
  ├─ has-many: UsageRecord (每请求一行，token 粒度)
  └─ has-one: Wallet (balance for deduction model; SaaS Edition)

Tenant (default='default' in MVP)
  ├─ has-many: User
  ├─ has-many: Provider (上游供应商目录)
  ├─ has-many: PoolGroup (逻辑容量)
  │  ├─ has-many: Channel (子过滤)
  │  │  ├─ has-many: ProviderAccount (凭证单位)
  │  │  │  ├─ belongs-to: Provider
  │  │  │  ├─ has-many: PoolSlotAcquisition (并发占用)
  │  │  │  └─ has-many: UsageRecord
  │  │  └─ has-many: RoutingRule（路由）
  │  └─ has-many: BillingEvent (结算审计)
  └─ has-many: APIKey

BillingLedgerClaim (Tx1 — money 源)
  ├─ has-one: BillingEvent (Tx2 settled)
  └─ has-many: UsageRecord (async write; orphan sweep)
```

---

## 4. 业务功能脑图

### 4.1 五大复杂度轴

HUAKAI 的核心难度集中在 5 个正交维度（[02_HUAKAI_FUSION_ARCHITECTURE.md](../02_HUAKAI_FUSION_ARCHITECTURE.md) L85-97）。前端需要理解每个轴对 UI 的影响：

#### 轴 1: 上下文状态（sticky session / 跨账号 fallback）
- **当前实现 10%** — sticky_bindings 表 schema 有；逻辑代码 0 行
- **UI 影响**: 展示请求的 "sticky binding" 状态（"黏在 Anthropic Pro 账号"）；fallback 链可视化

#### 轴 2: 渠道调度（5 层 routing + 9-gate claim-gate）
- **当前实现 60%** — 9-gate selector + Phase C.2 实现已落
- **UI 影响**: Pool Group 详情页展示 9-gate 每层的通过/过滤数；account 列表按 gate 状态着色（green=all pass, yellow=1 fail, red=multiple fail）

#### 轴 3: 协议转换（OpenAI ↔ Anthropic ↔ Gemini）
- **当前实现 15%** — 仅 anthropic_sse upstream；OpenAI client adapter 0
- **UI 影响**: 不直接影响；后端问题。但需要 model 别名映射页（用户选 claude-3.5 → 后端转发到 claude-3-5-sonnet）

#### 轴 4: 计费补偿（claim-gate Tx1 + 5-effect Tx2）
- **当前实现 70%** — F-OBS-001 + 50 不变式 + Slice 4 实现已落
- **UI 影响**: Usage & Billing 页是**最核心的 UI 页面**；需要展示 claim 预留 → 结算 → 调整 的完整链路；per-account cost 钻取；缓存差价（如 5m vs 1h cache）

#### 轴 5: 异步任务（orphan-sweep / DLQ replay）
- **当前实现 0%** — spec 提名字；实现 0 行
- **UI 影响**: Admin UI 需要"DLQ 消息队列"页 + "Failed Usage Records"页；重放按钮

### 4.2 业务工作流（3-ID 系统）

每个请求经历这个流程（[02_HUAKAI_FUSION_ARCHITECTURE.md](../02_HUAKAI_FUSION_ARCHITECTURE.md) Part B sequence diagram）：

```
User API Key Request
  ↓ [Auth resolver] → request_id
  ↓ [Registry resolver] → model 规范化
  ↓ [Router.Plan] → RoutePlan（候选账号列表）
  ↓ [Pool.Claim] → Tx1 claim row (status=reserving), attempt_id
  ↓ [CredentialVault.GetToken] → OAuth refresh / token cache
  ↓ [UpstreamDispatcher.Forward] → HTTP 请求，lease_id 占用
  ↓ [SSE Scanner + Proto Adapter] → 规范化响应，13-class 分类
  ↓ [Settler.Settle] → Tx2 UPDATE claim (status=committed), 写 usage_record
  ↓ [Response Write] → 返回给客户
  ↓ [Orphan Sweep Cron] → 清理超时的 reserving claim + usage record DLQ
```

**关键决策点**（UI 需要可视化）：

1. **Model Registry** — 用户请求 `gpt-4o`，系统能不能转换到某个 Anthropic 等效品？ (Supported Models / Model Aliases 页)
2. **Pool Selection** — 给定 model + user，哪个 PoolGroup 有？(Routing Rules 页)
3. **Account Selection** — Pool 内的 9-gate 过滤，哪个 account 通过？ (Provider Accounts 页，per-account gate 状态指示器)
4. **Sticky Binding** — 这个 user 上次用过 Account#5，这次是否应该粘在它上面还是重新选？ (Dashboard 实时观测)
5. **Cost Optimization** — 多个 account 都能用，哪个最便宜？（考虑缓存命中率） (Usage Analytics 页，per-account cost drill-down)
6. **Fallback Chain** — Account#5 失败，是否要自动 fallback 到 Account#3？(Router 配置，UI 展示候选链)

### 4.3 HUAKAI 与 8 个开源项目的融合

**融合模式不是简单的 checklist，而是选择性吸收每个项目的"灵魂"**（[02_HUAKAI_FUSION_ARCHITECTURE.md](../02_HUAKAI_FUSION_ARCHITECTURE.md) L99-181）：

| 项目 | License | 灵魂 | HUAKAI 吸收形态 | UI 体现 |
|------|---------|------|-----------------|--------|
| **Sub2API** | LGPL | 5 层 routing + 9-gate + claim-gate Pattern B | F-POOL-001 § Layers 1-5 + gate 链 | Pool 详情页，9-gate 逐层展示 |
| **One API** | MIT | 多租户 + channel + 2-gate auto-disable | tenant schema + channel table + auto-disable 逻辑（默认开） | Account 着色指示（自动禁用状态） |
| **New API** | AGPL | 缓存差价计费 + reasoning effort | 3-bucket pricing（5m vs 1h 1.6× 差价） | Usage 页，cache_read vs cache_creation 成本分离 |
| **Portkey** | MIT | per-provider stream 状态机 + fallback | 13-class 终态分类 + fallback chain planner | Request Log 详情，stream 分类和 fallback 足迹 |
| **Helicone** | GPL | 透明代理日志（不动客户端） | usage_record 写入 70%；prompt body cold store | Usage Analytics，每个请求的完整链路 |
| **LiteLLM** | MIT | 100+ provider normalization + retry 优先级 | proto 抽象；provider catalog 100+ model mapping | Model Aliases 页，model 规范化映射表 |
| **All-API-Hub** | AGPL | (反例) 浏览器自动登录抓凭证 | **绝不能抄** — KMS encryption mandatory | — |
| **Envoy AI GW** | Apache | K8s CRD 声明式配置 | SaaS Edition Phase 9+ 参考；Personal 不用 | — |

---

## 5. 现有 Frontend 状态

### 5.1 现状：Vertical Closure 测试 Wedge

当前 `frontend/` 是最小的**垂直测试楔**，仅 2 页，用于 smoke test 和 E2E 路径验证，**不是完整 Admin UI**（[frontend/README.md](../frontend/README.md)；[frontend/app/page.tsx](../frontend/app/page.tsx)）。

**当前 2 页**:

| 页面 | 路径 | 用途 | 实现度 |
|------|------|------|--------|
| **ChatPage** | `/` | 直接向网关发 Anthropic Messages 请求，支持 SSE 流式 | ✅ ~250 LoC；手测用 |
| **ObservabilityPage** | `/observability` | 每 2 秒 poll `/debug/vars`，展示 cache token 命中率 | ✅ ~250 LoC；指标看板 |

**技术栈**:
- **Next.js 14** App Router + TypeScript strict mode
- **无 UI 库**（无 shadcn / mui / antd） — 纯原生 CSS
- **无 SSE 第三方库** — fetch + ReadableStream 行解析
- **反代**: next.config.mjs `rewrites` 把 `/v1/*` + `/debug/*` 转发到 `:8080`（网关地址）

### 5.2 当前缺陷

- 页面只覆盖"聊天调试"和"观测"两个垂直；**完全缺少 Admin UI**
- 0 个管理页面（account 管理、key 签发、billing 查询）
- 0 个资源 CRUD 界面
- 没有认证流程（hardcoded 或 localStorage token）

---

## 6. 目标前端 Scope（最重要）

基于后端 API 表面 + 数据模型 + 业务工作流，完整 Admin/Ops UI 应覆盖以下 **12 个核心页面**。按优先级分三层（L0 must-have、L1 should-have、L2 nice-to-have）。

### 6.1 L0 — MVP 必须有（能赚钱的最小集）

#### Page 1: Dashboard（总览）
**用户故事**: 运营者打开首页，看到当前运营状态一览。  
**主要操作**: 显示指标卡片；导航到其他模块  
**需要的 Backend API**:
- `GET /admin/v1/usage` — 今日 token 总量、cost 总量、cache 命中率
- `GET /debug/vars` — 实时账号在线状态、per-account 请求量
- `GET /admin/v1/provider-accounts?limit=5` — 最近 5 个账号的健康状态

**预期 UI 组件**:
- 卡片: 今日成本、今日请求数、平均延迟、cache hit ratio
- 表格: Top 5 Provider Accounts（account_name, health_state, in_flight_count, quota_status）
- 告警条: 如果任何账号健康状态 = degraded/failed，红色横幅提示

---

#### Page 2: API Key Management（用户 API 凭证）
**用户故事**: 运营者为 end-user 签发 API Key，并可撤销。  
**主要操作**: 签发新 key → 复制 plaintext → 给用户 → 列表查看 → 撤销  
**需要的 Backend API**:
- `POST /admin/v1/api-keys` — 签发，body `{user_id, name?, expires_at?}`，response 含 `plaintext_key`（仅此处显示）
- `GET /admin/v1/api-keys` — 列表，query `?user_id=123&status=active`
- `POST /admin/v1/api-keys/{id}/revoke` — 撤销

**预期 UI 组件**:
- 表单: "签发新 Key"，input user email/id，optional name + 过期时间，submit 按钮
- 模态: 签发后弹出 modal，显示完整 key（仅一次），复制按钮，"我已保存"确认后关闭
- 表格: 已签发的 key 列表（key_prefix + ****，用户邮箱，名称，创建时间，状态，过期时间，操作按钮）
- 按钮: "Revoke"（icon 确认），revoke 后变灰

---

#### Page 3: Provider Account Management（上游凭证库）
**用户故事**: 运营者添加、启用、禁用自己的 Anthropic Pro / Gemini Advanced 凭证。  
**主要操作**: 新增 → 填 token/OAuth link → 保存 → 启用/禁用 → 查看状态  
**需要的 Backend API**:
- `GET /admin/v1/provider-accounts` — 列表，query `?enabled=true&channel_id=123`
- `POST /admin/v1/provider-accounts` — 新增，body `{channel_id, provider_id, account_type, credentials (encrypted), name}`
- `PATCH /admin/v1/provider-accounts/{id}` — 修改（enabled, cap_concurrency, cap_quota_daily）
- `POST /admin/v1/provider-accounts/{id}/clear-rate-limit` — 重置

**预期 UI 组件**:
- 表单（新增 / 编辑 modal）:
  - Select: Provider（Anthropic / OpenAI / Gemini）
  - Select: Channel（from PoolGroup）
  - Radio: Account type（OAuth / API Key / Service Account）
  - Textarea: Credentials（粘贴 token；前端不存储明文，发送给后端加密）
  - Checkbox: Enabled
  - Input: Name
  - Input: cap_concurrency（default 4）
  - Input: cap_quota_daily（optional numeric）
- 表格（列表）:
  - Columns: Name | Provider | Health State（着色: green/yellow/red） | Credential State | In Flight / Cap | Quota Status | Actions
  - 着色规则: health_state=operational → green; degraded → yellow; failed → red
  - 着色规则: quota_status=exhausted → orange badge
  - Actions: Enable/Disable toggle | Edit | Clear Rate Limit | Delete

---

#### Page 4: Pool Group & Channel Management（容量分组）
**用户故事**: 运营者创建 Pool（如"Anthropic Pro Pool"），配置路由参数，添加/移除 account。  
**主要操作**: 新建 Pool → 配置参数 → 新建 Channel → 绑定 Account 列表  
**需要的 Backend API**:
- `GET /admin/v1/pools` — 列表，query `?tenant_id=default&enabled=true`
- `POST /admin/v1/pools` — 新增，body `{name, routing_policy_version, top_k_default, sticky_wait_timeout_ms, ...}`
- `PATCH /admin/v1/pools/{id}` — 修改配置
- `GET /admin/v1/pools/{id}/channels` — 该 Pool 下的 Channel 列表
- `POST /admin/v1/pools/{id}/channels` — 新增 Channel
- `GET /admin/v1/provider-accounts?pool_group_id={id}` — 该 Pool 的所有 Account

**预期 UI 组件**:
- Pool 列表卡片:
  - Card: Pool 名称 | top_k_default | 包含的 Channel 数 | 包含的 Account 数 | Enabled toggle | Edit/Delete actions
- Pool 详情页（modal 或独立页）:
  - 配置表单: name, routing_policy_version, top_k_default（1-10 slider）, sticky_wait_timeout_ms, fallback_wait_timeout_ms
  - Channel 子表: 按 Channel 列出，每行可 expand 显示该 Channel 的 Account
- Account 绑定逻辑:
  - 在 Channel 级别拖拽/checkbox 选择 Account，保存时调用 PATCH `/admin/v1/provider-accounts/{id}` 更新 channel_id

---

#### Page 5: Usage & Billing（消费记录 + 成本）
**用户故事**: 运营者查看 end-user 的 token 消费和成本，做出定价决策。  
**主要操作**: 选时间段 → 按 user / account / model 维度聚合 → 看成本分解  
**需要的 Backend API**:
- `GET /admin/v1/usage?start_date=2026-05-01&end_date=2026-05-12&group_by=user` — 聚合查询，response `[{dimension_value, token_count, cache_hit_rate, cost}, ...]`
- `GET /admin/v1/usage?group_by=account&pool_group_id={id}` — per-account 成本分解
- `GET /admin/v1/billing/claims?status=committed&api_key_id={id}` — 该 key 的所有 claim
- `GET /admin/v1/billing/events?claim_id={id}` — claim 的结算事件

**预期 UI 组件**:
- 日期范围选择器 + group-by 单选（User / Account / Model / PoolGroup）
- 表格: 按选中维度聚合展示，Columns: Dimension | Token Count | Cache Creation | Cache Hit | Input Cost | Output Cost | Cache Cost | Total Cost
- 图表: 成本趋势（日环比）
- 钻取: 点击某行 → 下钻到 day-by-day / request-by-request 明细日志

---

### 6.2 L1 — 产品完整性（应该有）

#### Page 6: Request & Audit Logs（请求日志）
**用户故事**: 运营者调查某个请求的完整链路。  
**主要操作**: 按 request_id / user / model 搜索 → 看完整的 routing 决策、fallback 足迹、token 消费  
**需要的 Backend API**:
- `GET /admin/v1/requests?search=request_id&limit=100` — 请求列表，含 request_id, user, model, status, latency, cost
- `GET /admin/v1/requests/{request_id}` — 详情，含 routing_plan, attempt_chain, ledger snapshot

**预期 UI 组件**:
- 搜索栏 + 表格（request_id, user_email, model, provider_account_name, status, latency_ms, total_cost, created_at）
- 详情 modal: 
  - Timeline 视图: attempt #1 (Account A, failed after 3s) → attempt #2 (Account B, success in 1.2s)
  - JSON 展开: routing_plan, usage_record 完整内容，billing_claim snapshot
  - 操作: "Copy Request ID"（debug 用）

---

#### Page 7: Provider Health Map（上游健康监控）
**用户故事**: 运营者看到所有 Provider（Anthropic、OpenAI、Gemini 等）在各 Account 上的实时健康状态。  
**主要操作**: 看每个 account 的最后一次成功/失败；手动刷新某个 account  
**需要的 Backend API**:
- `GET /admin/v1/provider-accounts` — 全列表，含 health_state, last_dispatch_at, last_error
- `POST /admin/v1/provider-accounts/{id}/refresh-health` — 手动健康检查（可选）

**预期 UI 组件**:
- 网格 / 表格: Provider × Account 矩阵，单元格着色（green=healthy, yellow=degraded, red=failed，灰色=disabled）
- 单元格 hover: 显示 last_check_at, last_error_message, health_state_until（如果有 cooldown）
- 刷新按钮: per-account 的"重试健康检查"

---

#### Page 8: Quota & Rate Limiting（额度控制）
**用户故事**: 运营者为 end-user 设置消费上限，自动断流。  
**主要操作**: 新建 quota policy → 分配到 user 或 key → 监控使用进度  
**需要的 Backend API**:
- `POST /admin/v1/quota-policies` — 新增 policy（daily / weekly / total 额度）
- `GET /admin/v1/quota-policies` — 列表
- `PATCH /admin/v1/quota-policies/{id}` — 修改
- `GET /admin/v1/api-keys/{id}` — 该 key 的 quota 状态（用了多少、剩多少）

**预期 UI 组件**:
- 表单（新增 / 编辑 modal）: 
  - Input: Policy 名称
  - Numeric: Daily cap (tokens)
  - Numeric: Weekly cap (tokens)
  - Numeric: Total cap (tokens 或 $)
  - Select: Apply to (User / API Key)
  - Checkbox: Auto-enforce (超额自动 403 还是告警)
- 表格（列表）: Policy name | User/Key | Daily cap | Daily used | % | Weekly cap | Weekly used | % | Total cap | Total used | %

---

### 6.3 L2 — 生产就绪（nice-to-have）

#### Page 9: System Settings & Feature Flags
- Edition 切换（Personal ↔ SaaS；当前锁 Personal）
- Feature flag 控制面板（如：auto-disable on/off）
- KMS key rotation 管理
- Backup / DR 配置

#### Page 10: Plugin Marketplace（L0 完成前不做）
- 上传自定义 adapter / middleware
- 启用/禁用 plugin

#### Page 11: Audit & Compliance Export
- 所有 admin 操作日志
- GDPR / SOC2 报告导出

#### Page 12: Multi-Tenant Admin（SaaS Edition 专用；Phase 10+）
- Tenant 列表、创建、隔离管理
- Cross-tenant 成本统计

---

## 7. HUAKAI 前端风格定位

### 7.1 视觉气质

基于 [CLAUDE.md](../CLAUDE.md)、[AGENTS.md](../AGENTS.md)、垂直闭包测试 wedge 的现有设计：

**不是消费级 UI，是专业操作中心**：
- **色系**: Dark theme（#161b22 背景，#e6edf3 主文本，#58a6ff 链接），受 GitHub dark mode 启发；保留足够的对比度（WCAG AA）
- **排版**: 等宽字体用于 ID / token prefix / 数字；sans-serif（如 -apple-system, BlinkMacSystemFont）用于标签和操作
- **交互**: 按钮着色（success=green #238636，primary=blue #6e40c9，danger=red #da3633）；toggle / radio / checkbox 使用原生 HTML + CSS，不依赖第三方 UI 库
- **布局**: 卡片网格（垂直闭包测试用 280px min-width）；表格 sticky header；侧栏导航（当前用 next.js Link）
- **密度**: 紧凑但可读；表格行高 1.5em；表单输入 0.75rem padding；信息卡 1rem 内边距

### 7.2 设计原则

**遵循约束**（来自 HUAKAI 项目规则）：
1. **CMB-5 凭证隐私**: 永不在 UI 上显示完整凭证；API Key 仅在签发时的 modal 中一次性显示，复制后关闭 modal 就看不到了
2. **No UI source copy**: 不复制非 MIT UI 源代码；可以参考 UI 工作流（如 Sub2API 的"账号池卡片"布局）但要独立实现
3. **Audit-grade 日志**: 所有 admin 操作（新增 / 删除 / 修改）都要有时间戳 + 操作者身份 + 变更内容；前端负责展示，后端负责记录
4. **Tenant isolation visual**: Personal Edition 虽然只有 1 个 tenant，但 UI 预留 tenant_id 展示，为 SaaS Edition 迁移做准备（目前可隐藏或显示为"Default"）

### 7.3 导航结构

```
HUAKAI Admin
├─ Dashboard                 (首页，指标 + 快捷导航)
├─ Gateway Operations       (网关层)
│  ├─ API Keys              (用户凭证管理)
│  ├─ Provider Accounts     (上游凭证库)
│  └─ Pool Groups           (容量分组 + Channel)
├─ Routing & Rules          (路由层)
│  ├─ Models & Aliases      (model normalization 映射表)
│  └─ Routing Policies      (Pool 级别的路由策略)
├─ Observability           (观测层)
│  ├─ Request Logs         (请求日志 + 详情)
│  ├─ Usage & Billing      (消费 + 成本)
│  ├─ Provider Health      (上游状态)
│  └─ Quota & Rate Limits  (额度控制)
├─ Audit & Compliance      (审计层)
│  ├─ Audit Logs           (admin 操作日志)
│  └─ DLQ & Dead Letter    (failed 消息队列；Phase 4.5+)
└─ Settings                (系统层)
   ├─ Edition & Mode       (Personal / SaaS 切换)
   ├─ Feature Flags        (功能开关)
   └─ KMS & Secrets        (密钥管理)
```

### 7.4 技术栈建议（遵循 [DR-004](../process/decisions/DR-004-frontend-framework.md)）

- **Framework**: Next.js 14+ App Router（已选定；[DR-004](../process/decisions/DR-004-frontend-framework.md)）
- **Language**: TypeScript strict mode（[frontend/tsconfig.json](../frontend/tsconfig.json)）
- **Styling**: 原生 CSS 或 CSS modules（no Tailwind / Styled Components；keep it minimal）
- **HTTP Client**: fetch API（no axios / tanstack query；可选 SWR 用于轮询，但 [observability/page.tsx](../frontend/app/observability/page.tsx) 已用原生 setInterval）
- **State Management**: React hooks 局部状态（no Redux / Zustand；除非页面数据流太复杂）
- **UI Components**: 不用 shadcn / mui / antd；自己写简单组件（button / input / select / modal / table）
- **Codegen**: TypeScript types 从 `docs/openapi/openapi.yaml` 生成（openapi-generator 或 @hey-api/openapi-ts；第一版可手写 types）
- **Mock Data**: `__mocks__` 目录或 msw（如果需要）；前端可先对 stub 501 endpoint 工作

---

## 8. Phase 规划与前端交付窗口

### 8.1 HUAKAI 的 10 个 Phase

前端应理解整个产品路线图，知道自己在哪个 Phase（[16_PHASED_DELIVERY_PLAN.md](../16_PHASED_DELIVERY_PLAN.md)）：

| Phase | 里程碑 | 前端工作 |
|-------|--------|---------|
| **0** | Governance Baseline | 规则文档 + agent 定义（已完成） |
| **1** | Reference Evidence & Feature Map | 数据挖掘（已完成） |
| **2** | MVP Scope Lock & Architecture | API 合约锁定（已完成 2026-04-29） |
| **3** | Project Skeleton | Go 框架 + TS 框架选定（已完成；Next.js 14） |
| **4** | Gateway Core MVP | `/v1/chat/completions` 端到端工作（已完成 2026-05-09） |
| **5** | Provider Account Hub & Routing | Pool / Channel / Account CRUD + selector（已完成 60-70%） |
| **6** | Billing & Usage | Claim/Settle/Usage 完整链路（已完成 70%） |
| **7** | Admin UI (L0) | **← Gemini 当前位置**；API Key + Provider Account + Pool + Usage/Billing 页面 |
| **8** | Advanced Features (L1) | Request Logs / Health Map / Quota / DLQ |
| **9** | Personal Edition Release | E2E smoke test + docs + security audit |
| **10+** | SaaS Edition | Tenant onboarding / isolation / billing |

**Gemini 的交付窗口**: Phase 7（L0 Admin UI）—— 估计 4-6 周，取决于后端 stub endpoint 的完成度。

### 8.2 当前后端 stub 状态（2026-05-12）

14 个 admin endpoint 中：

- **实现 6 个** (70%)（已有真实逻辑 + DB 交互）:
  - `POST /admin/v1/api-keys`
  - `GET /admin/v1/api-keys`
  - `POST /admin/v1/api-keys/{id}/revoke`
  - `GET /admin/v1/usage` （Slice 4 obs reader）
  - `GET /admin/v1/billing/claims`
  - `GET /admin/v1/billing/events`

- **预留签名 8 个** (需实现或 mock):
  - `GET/POST/PATCH /admin/v1/provider-accounts`
  - `GET/POST/PATCH /admin/v1/pools`
  - `GET/POST /admin/v1/pools/{id}/channels`
  - `POST /admin/v1/provider-accounts/{id}/clear-rate-limit`
  - 以及 L1 的 request logs / health check / quota policy

**建议**: Gemini 前端可以**先对 stub 501 endpoint 写 UI**（用 mock data / localStorage），等后端实现时切换到真实 API。这样并行度最高。

---

## 9. 关键参考文档（Read First）

按阅读顺序（从高到低）：

| 文档 | 用途 | 行数 |
|------|------|------|
| **本文** (2026-05-12-frontend-brief-huakai-summary.md) | Gemini 快速上手 | ~600 |
| [01_PROJECT_BRIEF.md](../01_PROJECT_BRIEF.md) | 产品定位 + Owner 目标 | ~90 |
| [02_HUAKAI_FUSION_ARCHITECTURE.md](../02_HUAKAI_FUSION_ARCHITECTURE.md) | 融合架构 + 5 轴复杂度 + 进度表 | ~230 |
| [14_UI_CONTRACTS.md](../14_UI_CONTRACTS.md) | UI 需要覆盖的资源和操作 | ~42 |
| [docs/specs/api-contract.md](../specs/api-contract.md) | OpenAPI 合约 + 错误模型 | ~128 |
| [backend/sql/migrations/000*.up.sql](../backend/sql/migrations/) | Schema（通读 0001-0010） | ~1500 |
| [docs/openapi/openapi.yaml](../openapi/openapi.yaml) | 完整 OpenAPI 3.1 定义 | 1184 |
| [CLAUDE.md](../CLAUDE.md) §§1-6 | 项目规则 + clean-room policy | ~130 |
| [AGENTS.md](../AGENTS.md) | Agent 角色定义 + Gemini 约束 | ~170 |

---

## 10. 待 Owner 确认的设计决策

| # | 决策点 | 建议 | 影响范围 |
|---|--------|------|---------|
| **1** | Admin 用户认证方式 | Personal Edition MVP 用简单方案（静态 token 在 config 或 env；SaaS Edition 用 OIDC/Google Login） | 影响 `/admin/*` 所有 endpoint；需要中间件拦截 |
| **2** | API Key 显示策略 | 只在签发 POST 响应 modal 中显示完整；列表页显示 prefix+\*\*\*\*；复制后 modal 关闭（CMB-5） | frontend/app/api-keys page |
| **3** | Pool 和 Channel 的 UI 层级 | 建议：PoolGroup 作为顶级页面，Channel 作为其子资源（展开 / modal）；account binding 发生在 Channel 级别 | Pool 管理页设计 |
| **4** | 缓存成本分解展示 | 对标 new-api，分离 cache_creation_cost 和 cache_read_cost；比率可定制（建议 1.6× 差价） | Usage & Billing 页，表格添加列 |
| **5** | Model alias 映射 | 需要 UI 页面吗？还是只在 API response 中返回（如 alias_mapping in pool details）| Model 归一化逻辑 |
| **6** | Fallback chain 可视化 | Request log 详情中用 timeline / waterfall 图展示 attempt 链和耗时 | Request Logs 页，需要 backend 支持 attempt 链返回 |

---

## 附录 A：Schema 关键字段速查表

### 用户和凭证层
```
users:                      id, tenant_id, email, name, status, balance
api_keys:                   id, tenant_id, user_id, key_prefix, key_hash (bcrypt), status, expires_at
```

### 上游配置层
```
providers:                  id, tenant_id, code (anthropic/openai/gemini), upstream_protocol
pool_groups:                id, tenant_id, name, routing_policy_version, top_k_default, wait budgets
channels:                   id, tenant_id, pool_group_id, name, failover_status_codes
provider_accounts:          id, tenant_id, provider_id, channel_id, account_type, 
                            enabled, expires_at, health_state, credential_state, 
                            cap_concurrency, in_flight_count, cap_quota_*, quota_used_*,
                            model_allow_list, capability_flags, credentials (encrypted)
```

### 金钱路径层
```
billing_ledger_claims:      id, tenant_id, idempotency_key, api_key_id, user_id,
                            provider_account_id, acquisition_token, predicted_cost, actual_cost,
                            status (reserving/committed/aborted), reserved_at, settled_at
billing_ledger_archive:     用于幂等性重放检查（retired claims）
billing_events:             claim_id, event_type (claim_committed/aborted), actual_cost
```

### 观测数据层
```
usage_records:              id, tenant_id, claim_id, api_key_id, user_id, provider_account_id,
                            request_id, model, endpoint_family,
                            input_token_count, output_token_count, cache_*_tokens,
                            cost_input, cost_output, cost_cache_*, total_cost,
                            end_class (13 class enum), latency_ms, created_at
```

---

## 附录 B：API 端点快速索引（URL 汇总）

### 客户端 API
```
POST /v1/chat/completions
POST /v1/messages
POST /v1/responses         (预留)
```

### Admin API
```
POST   /admin/v1/api-keys
GET    /admin/v1/api-keys
GET    /admin/v1/api-keys/{id}
POST   /admin/v1/api-keys/{id}/revoke

GET    /admin/v1/provider-accounts
POST   /admin/v1/provider-accounts
PATCH  /admin/v1/provider-accounts/{id}
POST   /admin/v1/provider-accounts/{id}/clear-rate-limit

GET    /admin/v1/pools
POST   /admin/v1/pools
PATCH  /admin/v1/pools/{id}
GET    /admin/v1/pools/{id}/channels
POST   /admin/v1/pools/{id}/channels

GET    /admin/v1/usage
GET    /admin/v1/billing/claims
GET    /admin/v1/billing/events

(L1+)
GET    /admin/v1/requests
GET    /admin/v1/requests/{request_id}
POST   /admin/v1/provider-accounts/{id}/refresh-health
(...)  /admin/v1/quota-policies
```

### Debug API
```
GET    /debug/vars         (expvar format)
```

---

**生成时间**: 2026-05-12 11:30 UTC  
**代码状态参考**: `backend commit 5d1fbd7 (2026-05-09)`  
**Gemini 可以开始的工作**: L0 Admin UI 6 页面原型（Page 1-6）；并行等待 Backend Page 3 (Provider Account) + Page 4 (Pool/Channel) endpoint 实现。
