# F-FP-POOL Plan (Claude) — TLS 指纹池 + 代理池 一键部署化

**作者:** Claude (PM-Orchestrator)  
**日期:** 2026-05-19  
**触发:** Owner 2026-05-19 quote "代理池肯定也是需要的，就行sub2也配置了代理池" + "你来做。codexrenew吧"  
**lane:** specifier + implementer (反代敏感 spec/code, 2026-05-16 Owner directive 允许 Claude 直接写)

## 1. 背景

今日抓包验证了 HUAKAI 现有 mimicry pipeline emit ja3 跟真 Codex CLI 0.128.0 一致 (commit 8f6c7dd), 但是发现:

- HUAKAI 只 builtin 1 个 codex template, 全 account 共享 → 1000 个用户请求全同 ja3 = 反检测系统 cluster signal
- sub2api 通过 `tls_fingerprint_profiles` DB 表 (admin CRUD) + per-account 绑定 (`tls_fingerprint_profile_id` 0/-1/>0 三档) 解决这个
- sub2api 也有 `proxies` DB 表 + per-account `proxy_id` FK, HUAKAI 现在只有 inline `proxy_url` string column

Owner directive: HUAKAI parity sub2api + 一键部署带 builtin seed + drift detection (HUAKAI delta).

## 2. 范围 (3 commit, ~4 天 codex 等价, Claude 直接实施)

### Phase 1: 基础 parity sub2api (2.5 天)

**migration 0037** `tls_fingerprint_profiles`:

```sql
CREATE TABLE tls_fingerprint_profiles (
    id                    bigserial PRIMARY KEY,
    name                  text NOT NULL UNIQUE,
    description           text,
    enable_grease         boolean NOT NULL DEFAULT false,
    cipher_suites         integer[] NOT NULL DEFAULT '{}',
    curves                integer[] NOT NULL DEFAULT '{}',
    point_formats         integer[] NOT NULL DEFAULT '{}',
    signature_algorithms  integer[] NOT NULL DEFAULT '{}',
    alpn_protocols        text[]    NOT NULL DEFAULT '{}',
    supported_versions    integer[] NOT NULL DEFAULT '{}',
    key_share_groups      integer[] NOT NULL DEFAULT '{}',
    psk_modes             integer[] NOT NULL DEFAULT '{}',
    extensions            integer[] NOT NULL DEFAULT '{}',
    status                text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at            timestamptz NOT NULL DEFAULT NOW(),
    updated_at            timestamptz NOT NULL DEFAULT NOW(),
    deleted_at            timestamptz
);
CREATE INDEX idx_tls_fingerprint_profiles_status ON tls_fingerprint_profiles(status) WHERE deleted_at IS NULL;
```

**migration 0038** `proxies` + `provider_accounts` column adds:

```sql
CREATE TABLE proxies (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL UNIQUE,
    protocol    text NOT NULL CHECK (protocol IN ('http', 'https', 'socks5')),
    host        text NOT NULL,
    port        integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
    username    text,
    password    text,           -- 加密存; HUAKAI credentialstore.KeyProvider 套
    status      text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'dead')),
    created_at  timestamptz NOT NULL DEFAULT NOW(),
    updated_at  timestamptz NOT NULL DEFAULT NOW(),
    deleted_at  timestamptz
);

ALTER TABLE provider_accounts
    ADD COLUMN tls_fingerprint_profile_id bigint REFERENCES tls_fingerprint_profiles(id),
    ADD COLUMN proxy_id                   bigint REFERENCES proxies(id);

-- backfill: 把现有 proxy_url 字符串变成 proxies 表行 + 关联
-- 详细 SQL 见 migration 文件
```

**sqlc 改动** — 加 query 到 admin package:

- `sql/queries/admin_tls_fingerprint_profiles.sql` (List/Get/Create/Update/Delete)
- `sql/queries/admin_proxies.sql` (同)

**service layer** — 新 file `backend/internal/admin/fingerprint_resolver.go`:

```go
type FingerprintResolver struct {
    q *admindb.Queries
}
func (r *FingerprintResolver) ResolveTLSProfile(ctx, account) (*tlsfingerprint.Profile, error)
func (r *FingerprintResolver) ResolveProxy(ctx, account) (*url.URL, error)
```

三档逻辑照搬 sub2api (`id>0` 固定 / `id==-1` random / `0 OR NULL` builtin fallback).

**transport.Factory 改造** — `For()` 签名加 account 参数:

```go
// 旧
func (f *Factory) For(provider ProviderCode, mode TransportMode) (http.RoundTripper, error)
// 新
func (f *Factory) ForAccount(provider ProviderCode, mode TransportMode, account *AccountResolution) (http.RoundTripper, error)
```

老 `For()` 保留 backwards-compat shim (走 builtin default), 调用方逐步迁移到 `ForAccount`.

**admin endpoints** — 新文件 `backend/internal/adminhttp/tls_profile_handler.go` + `proxy_handler.go`:

- `GET/POST /admin/v1/tls-profiles`
- `GET/PUT/DELETE /admin/v1/tls-profiles/{id}`
- `GET/POST /admin/v1/proxies`
- `GET/PUT/DELETE /admin/v1/proxies/{id}`

均要求 admin token (复用现 `AdminResolver`).

### Phase 2: HUAKAI delta — seed bootstrap (1 天)

新文件 `backend/internal/admin/fingerprint_seed.go` + 数据 `backend/internal/admin/fingerprint_builtin_data.go` (代码内嵌 JSON):

启动时检测 `tls_fingerprint_profiles` 行数 == 0, 则 INSERT 3-5 个 builtin profile:

- `codex-cli-0.128-linux` (从今天抓的 27718d56... — commit 1440a8d)
- `codex-cli-0.128-macos` (后续抓样, 初版可 skip)
- `claude-code-node24` (转译现 `anthropic-claude-code.json`)
- `gemini-cli` (转译现 `gemini-advanced.json`)
- `kiro-cli` (转译现 `kiro-cli.json`)

启动 idempotent (已 seed 过不重复 INSERT). 老 builtin file `tools/fingerprint-collector/templates/` 保留作为 capture artifact, 但 runtime 优先从 DB 读。

### Phase 3: HUAKAI delta — drift detection worker (0.5 天)

新文件 `backend/internal/admin/fingerprint_drift_worker.go`:

- Ticker 默认 24h 跑一次 (env `HUAKAI_FP_DRIFT_INTERVAL` 可调, dev 缩短测)
- 对每 active TLS profile 跑一次 mimicry wire-emit smoke (复用 `_smoke-codex-tls` 逻辑提抽成 internal 函数)
- 比 wire ja3 vs profile metadata 的 ja3
- 不一致 → INSERT `obs_outbox_events` (alert priority=high), DB 标 profile.status='drift_detected' (新 status 值, 加 CHECK)
- admin UI 看 status 标红 (frontend 后续)

## 3. Risk / 缓解

| 风险 | 缓解 |
|---|---|
| transport.Factory 签名改, 全 callsite 编译破 | 老 `For()` 保留 shim, 走 default. 全仓搜 callsite 后逐步迁 |
| migration 0038 backfill 复杂 | 提取 proxy_url 解析为 (protocol/host/port/user/pass), INSERT proxies, JOIN 回 provider_accounts.proxy_id; 在 tx 内做; 保留老 proxy_url 列直到下个 release 完成迁移 |
| sqlc 在 admin package 加新 query 跟现 admin_provider_account_mutations.sql 同 package, 注意 name 冲突 | sqlc 同 package query 函数名要 unique, 用 `TLSFingerprintProfile*` 和 `Proxy*` 前缀 |
| seed 数据用今天抓的 27718d56 但 fingerprint 漂移会让它再 stale | drift detection (Phase 3) 自动 alert. 主流程不阻塞。 |
| drift worker 跑真上游消额度 | smoke 每 24h 跑 1 次 × 5 profile × ~16 tokens = ~80 tokens/天, 微忽略 |
| Frontend admin 页未实现 | API 接口 ready, admin 可用 curl + bootstrap token 操作; frontend 留作 follow-on PR |

## 4. Verification

每 phase commit 后:

1. `cd backend && GOCACHE=/tmp/go-cache go build ./...` PASS
2. `cd backend && GOCACHE=/tmp/go-cache go test ./internal/admin/... ./internal/transport/... ./internal/provider/... -race -count=1 -timeout 180s` PASS
3. integration_pg test (sandbox 已证可跑): 应用新 migration 后 admin CRUD + ResolveTLSProfile/ResolveProxy 全 PASS
4. codex review per-commit: `codex exec review --uncommitted --full-auto` 后台跑, HIGH 必修, MED 选修
5. commit 走 v2 命名 (gateway/auth/billing/audit/pool/provider/proto/openapi/rust-vendor/rust-mimicry/tests/docs/structure 选一; 本 family 主要 `rust-mimicry` 或 `provider`); 一 commit 一 module

## 5. Out of scope (后续 release)

- **proxy_group / proxy pool random**: account 现 1:1 绑单 proxy. 多 proxy 池随机/health check 后续看流量信号再加
- **Persona 抽象 (6 维打包)**: 不做; admin 多屏配置可接受, 抽象只为防错配
- **Account-stable HRW pick**: 不做; 固定绑定本来就 stable
- **Persona 字段加密**: oai_device_id 不算密码级 secret; Phase 1 把 proxy.password 用 KeyProvider 加密就够了
- **Frontend admin pages**: API 完工后 follow-on PR

## 6. 决策点 (Owner approval needed before phase commit)

- ✅ **scope = Phase 1+2+3** (Owner 2026-05-19 确认 sub2api parity + 必加代理池)
- ✅ **Claude 直写实施** (Owner 2026-05-19 "你来做")
- ✅ **codex review 每 commit** (Owner 2026-05-19 "codexrenew 吧")
- ⏳ **Phase 1.2 backfill 策略**: 把现有 `proxy_url` 字符串 backfill 进 `proxies` 表; 这个是 destructive-ish (改 schema 关联). 启动前 surface Owner

## 7. Source files read

- `~/refs/sub2api/backend/internal/service/tls_fingerprint_profile_service.go` (sub2api ResolveTLSProfile 三档逻辑)
- `~/refs/sub2api/backend/internal/model/tls_fingerprint_profile.go` (model 字段 shape)
- `~/refs/sub2api/backend/ent/schema/proxy.go` (proxies table shape)
- `~/refs/sub2api/backend/ent/schema/account.go` (account → proxy edge)
- `~/refs/sub2api/backend/internal/repository/http_upstream.go` (DoWithTLS callsite + per-account TLS client pool)
- `backend/sql/migrations/0001_pool_routing.up.sql` (现 provider_accounts schema)
- `backend/sql/migrations/0012_provider_accounts_proxy_url.up.sql` (现 inline proxy_url 起源)
- `backend/internal/provider/postgres_proxy_resolver.go` (现 ProxyResolver 骨架)
- `backend/internal/transport/factory.go` (现 Factory.For 接口)
- `backend/internal/transport/mimicry/registry.go` (现 builtin LoadFromDirectory)
- `backend/cmd/_smoke-codex-tls/main.go` (今天写的 wire-emit smoke; drift worker 复用)

## 8. Lane / Agent / UTC

- **Lane:** implementer (Claude 直写代码, 反代敏感 spec 2026-05-16 Owner directive 允许)
- **Agent:** Claude Opus 4.7 (1M context)
- **UTC:** 2026-05-19T06:55Z
