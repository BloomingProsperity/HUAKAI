# Hermes Phase-1 Slice 1 Implementation Plan — Claude Lane

- Lane: Claude PM-orchestrator (parallel-draft per CLAUDE.md #10)
- UTC: 2026-05-25T11:30:00Z
- Branch: `claude/hermes-phase-1` (基 main `e7d6a9d`)
- 输入: `docs/plans/2026-05-24-hermes-native-integration.md` (dff1326) §First Implementation Slice
- 性质: planning only,不实施;Owner 批 §D 决策后 codex 实施
- Counter-part: `docs/process/plans/2026-05-25-hermes-phase-1-slice1-codex.md` (codex independently drafted,本 lane 未读)

## §0 Scope (in/out)

**In (本切片必交付)**:

1. PG 新表 4 张 (核心):
   - `hermes_settings` (per-user enable/disable + 配置 JSON)
   - `hermes_api_profiles` (3 模式: managed_huakai_api / dedicated_group / external_api)
   - `hermes_conversations` (chat session 元信息)
   - `hermes_audit_events` (enable/disable/profile changes/chat starts)
2. Migration 0057 上下脚本 + sqlc generated bindings
3. 5 个 gateway endpoints (HTTP layer):
   - `POST /v1/hermes/settings/enable`
   - `POST /v1/hermes/settings/disable`
   - `GET  /v1/hermes/settings`
   - `POST /v1/hermes/chat` (streaming proxy)
   - `GET  /v1/hermes/conversations`
4. `docker-compose.dev.yml` 加 `hermes-runner` 服务 (pip install hermes-agent==0.14.0)
5. Audit events 落库 (复用 `oauth_refresh_audit_events` 字段语义,actor/tenant/action/sanitized_args/result/correlation_id)

**Out (本切片不做,后续 Slice)**:
- 7 张完整表 (memory_items / tool_calls / runtime_events / messages 拆分到 Slice 2)
- HUAKAI skill pack 8 个 huakai.* tools (Slice 3)
- MCP server bidirectional (本切片 Hermes runner 拿到的是 OpenAI-compat HUAKAI key,通过 HTTP 调 HUAKAI;Slice 2+ 才加 MCP stdio)
- per-tenant Docker volume 隔离 (本切片用 单 volume + path 隔离,够 MVP)
- Hermes runtime upgrade 路径 (Slice 3 加 docker tag versioning)
- 真实 chat streaming SSE proxy 完整测试 (本切片只 echo skeleton)

## §A Success Criteria (可机检)

- `make backend-test` 通过,新表 migration up/down round-trip OK (本地 PG huakai_roundtrip 验证)
- `curl -X POST .../v1/hermes/settings/enable -H "X-API-Key: $K"` 落 audit row,enable 后 GET settings 返 `enabled: true`
- `docker compose -f backend/docker-compose.dev.yml up -d hermes-runner` 起来,健康 endpoint 返 200
- 新文件全部不属 `backend/internal/{gatewayhttp,gateway,proto}` (frozen package,grep 验证)
- 每个新测试满足 CLAUDE.md #14 mutation 自检 (在 PR body 列出"删除哪行测试就 red")

## §B Time Estimate

- 3 day 1 人 / 2 day 2 人并行 (codex 实施 + Claude review)
- Sub-day milestones:
  - 0.5 day: migration 0057 + sqlc + 本地 round-trip 验证
  - 1 day: 5 endpoints handler + service layer + audit hook
  - 0.5 day: docker-compose hermes-runner + healthcheck + 静态启动验证
  - 0.5 day: discriminating tests (mutation 自检覆盖)
  - 0.5 day: per-commit codex review + 修 S0/S1

## §C Blast Radius

- **Schema**: 新加 4 表 (非破坏性);不动既有表 → S2 schema gate
- **API**: 新 5 endpoints (新 prefix `/v1/hermes/`),不影响既有 OpenAI / Anthropic / Gemini API → S3
- **部署**: docker-compose.dev.yml 新增可选 service,production compose 不动 (本切片) → S3
- **Security**: api_profiles 表存 dedicated key/external key (加密) → encryption-at-rest 决策点 D2/D6
- **Production impact**: 0 (本切片仅 dev compose + 新 endpoints 默认 disable)

## §D Owner 决策点

(每决策含 ABCD 选项 + 推荐 + ≥2 参考项目对照 per CLAUDE.md #15)

### D1: 表分批

| 选项 | 描述 | 参考项目 |
|---|---|---|
| A: 一次性建 7-8 张表 | hermes_settings + api_profiles + conversations + messages + memory_items + tool_calls + audit_events + runtime_events | LiteLLM 单 migration 建多表 (BerriAI/litellm@79b457:litellm/proxy/schema.prisma:1045-1180) |
| **B: 本切片 4 张核心 (推荐)** | settings + api_profiles + conversations + audit_events;memory/tool_calls/runtime_events/messages 拆 Slice 2 | sub2api 渐进式建表 (Wei-Shaw/sub2api@91da81:backend/internal/repository/channel_repo.go:120) |
| C: 仅 2 张 (settings + audit) | 极简起步,后续每张表独立 slice | one-api 早期 commit 单表演进 |

**推荐: B**。理由: messages/memory_items 在 chat 真接通后才需要;tool_calls/runtime_events 需要 MCP server 落地后才有意义。本切片聚焦"开关 + 第一条 chat + audit"3 件套。

### D2: dedicated API key 存储

| 选项 | 描述 | 参考项目 |
|---|---|---|
| **A: 复用 backend/internal/auth/ api_key_resolver (推荐)** | Hermes dedicated key 进现有 admin/operator key 表 (新加 `purpose='hermes_runner'` 列或 metadata.purpose JSON),key 加密复用现有机制 | new-api channel API key 复用主 token 表 (QuantumNous/new-api@20d3e7:model/token.go:55) |
| B: 新表 `hermes_api_keys` | 独立表,独立 RBAC,独立 rotate 路径 | LiteLLM 独立 `LiteLLM_VerificationToken` 表 (BerriAI/litellm@79b457:proxy/schema.prisma:355) |
| C: 不存,Hermes 启动时从 settings 实时派生 | 无持久 key,每次 Hermes runner 启动 HUAKAI 派一次性 service token | sub2api per-session token pattern |

**推荐: A**。理由: 复用减重复;`auth.AdminOperatorKey` 已有 encryption + audit + RBAC;新加 purpose 字段不破坏既有。

### D3: runner 启动方式

| 选项 | 描述 | 参考项目 |
|---|---|---|
| **A: always-on docker (推荐)** | hermes-runner 一直跑,但 idle (无 user enable 时不调外部 API);UI 切换只改 PG 状态,runner 内部 dispatcher 按状态拒收 | CLIProxyAPI 单 process always-on (zhanglunet/CLIProxyAPI@21fad9db:cmd/CLIProxyAPI/main.go:80) |
| B: lazy on enable | 用户首次 enable 才 `docker compose up hermes-runner`;无 user 时 stop | 无强参考,LiteLLM/new-api 都是 always-on |
| C: K8s autoscale | 0→N replica 按 user count 缩 | LiteLLM helm chart (BerriAI/litellm@79b457:deploy/charts/litellm-helm/values.yaml:8) — 太复杂,留 Slice 3+ |

**推荐: A**。理由: pip install hermes-agent 启动 5-10s,lazy 模式首次 enable 延迟差;always-on idle 内存占 < 100MB 可接受。

### D4: runner→gateway auth

| 选项 | 描述 | 参考项目 |
|---|---|---|
| **A: shared secret + HMAC (推荐)** | HUAKAI 给 runner 一个 shared secret (启动 env),runner 每次 HTTP 调 HUAKAI 加 HMAC header;HUAKAI 验 HMAC + IP 限 docker network | CLIProxyAPI shared secret (zhanglunet/CLIProxyAPI@21fad9db:internal/auth/auth.go:42) |
| B: mTLS | runner 持 client cert,HUAKAI 持 root CA;启动 cert 派发 + rotate 复杂 | LiteLLM proxy mTLS option (BerriAI/litellm@79b457:proxy/utils.py:1820) |
| C: signed JWT (HUAKAI 派) | 每次启动 HUAKAI 派短期 JWT;runner refresh 时再派 | OpenAI/Anthropic SDK 同模式 |

**推荐: A**。理由: dev compose 内 docker network,IP 限 + HMAC 已足;mTLS 复杂留 production 切片;JWT 增加 refresh 链路复杂度。

### D5: schema gate 时机

| 选项 | 描述 |
|---|---|
| **A: 本切片同时建 4 表 (推荐)** | migration 0057 一次建 settings + api_profiles + conversations + audit_events,本切片闭合 |
| B: schema 独立 slice 0,handler 独立 slice 1 | 拆 2 commit,先 schema gate Owner 批,再实施 |
| C: 7 张表全建,Slice 2+ 不动 schema | 见 D1-A,被 D1-B 否决 |

**推荐: A**。理由: 4 表都是同一事务范畴 (Hermes core state),拆开成本高于收益;本地 PG round-trip 验证 + per-commit codex review 足够 schema gate ceremony。

### D6: hermes-agent version lock

| 选项 | 描述 | 参考项目 |
|---|---|---|
| **A: pin commit SHA (推荐)** | docker-compose 写 `pip install git+https://github.com/NousResearch/hermes-agent@v0.14.0` (tag) + 同时 record SHA in NOTICE.md | clean-room policy CLAUDE.md #12 first-cite recency check |
| B: 仅 pin version range | `hermes-agent==0.14.*` 让 patch 自动升 | 风险: 上游 0.14.1 改 behavior 我们未审 |
| C: vendor 源码到 backend/vendor/hermes-agent/ | 完全离线,版本 absolute 锁;但增加维护成本 | LiteLLM permitted-license vendor (CLAUDE.md #12 fusion-upgrade vendoring policy) |

**推荐: A**。理由: hermes-agent v0.14.0 是 NousResearch MIT 项目,pip 装 + tag pin + SHA 记录是 cleanest;C 留到上游不稳定再升级。

### D7: MCP server (HUAKAI exposes huakai.* tools)

| 选项 | 描述 |
|---|---|
| A: 本切片建 MCP server skeleton | 新包 `backend/internal/hermesmcp/` + stdio JSON-RPC handler,0 tools (后续 slice 加 8 个) |
| **B: 本切片不建 MCP,留 Slice 3 (推荐)** | 本切片 Hermes runner 通过 OpenAI-compat HTTP 调 HUAKAI;MCP bidirectional留 Slice 3 加 8 tools 时一起建 |
| C: vendor 现成 MCP server | 用 NousResearch 自带的 mcp_serve.py (~/refs-latest/hermes-agent-main/mcp_serve.py),HUAKAI 实现 client 端 | 反向: MCP server 已是 hermes-agent 自带,HUAKAI 是 MCP **client** 角度看 |

**推荐: B**。理由: MCP server 是后续 huakai.* tools 的载体,本切片没 tools 不需要 server;切片范围聚焦"开关 + 第一条 chat";C 表述有误,hermes-agent 自带 MCP server,HUAKAI 在 Slice 3 实现 client 接通。

### D8: tenant 隔离粒度

| 选项 | 描述 |
|---|---|
| **A: 单 volume + path 隔离 (推荐)** | `/var/lib/huakai/hermes/tenants/{tenant_id}/users/{user_id}/` 路径分目录,one docker volume;runner 内部按 path 走 | main plan §Data Isolation 默认 |
| B: per-tenant docker volume | 每 tenant 一个 named volume,docker volume create 动态 | 复杂度高,本切片 N=1 tenant MVP 不需要 |
| C: 本切片不做隔离,全 user 共享 path | 违反 plan §Non-Goals,否决 |

**推荐: A**。理由: 文件系统 path 隔离对 MVP 够;per-tenant volume 在 SaaS Edition 多租户切片再升;C 否决。

## §E Step-by-step 实施

按依赖序:

1. **Schema gate (codex commit 1)**:
   - migration `0057_hermes_core_tables.up.sql` (settings + api_profiles + conversations + audit_events)
   - down 脚本回滚干净
   - sqlc `backend/sql/queries/hermes.sql` 7-10 个 query
   - sqlc generate → `backend/internal/db/hermes/`
   - 本地 PG round-trip 验证 (huakai_roundtrip db)

2. **Service layer (codex commit 2)** 新包 `backend/internal/hermes/`:
   - `service.go`: EnableForUser / DisableForUser / GetSettings / RecordAudit
   - `audit.go`: 落 hermes_audit_events,字段对齐 main plan §HUAKAI Skill Pack
   - `keys.go`: dedicated API key 派发 (D2-A 复用 backend/internal/auth/)
   - 不动 frozen 包

3. **HTTP handler (codex commit 3)** 新包 `backend/internal/hermeshttp/` (避开 frozen gatewayhttp):
   - `settings_handler.go`: enable / disable / get
   - `chat_handler.go`: streaming proxy (skeleton,Slice 2 完整)
   - `conversations_handler.go`: list
   - wire 进 `cmd/gateway/routes.go` (cmd/ 不属 frozen,只 wire)

4. **docker-compose (codex commit 4)**:
   - `backend/docker-compose.dev.yml` 加 `hermes-runner` service
   - Dockerfile.hermes 在 `deploy/hermes/Dockerfile` 新建 (python:3.11-slim + `pip install hermes-agent==0.14.0` + entrypoint)
   - healthcheck endpoint + IP 限 docker network

5. **Discriminating tests (codex commit 5)**:
   - `hermes/service_test.go`: enable 后 disable,audit row 数 = 2,outcome 字段含 enabled/disabled
   - `hermeshttp/settings_handler_test.go`: enable 无 auth → 401;有 auth → 200 + audit row
   - `audit_test.go`: actor/tenant/action 字段缺一即 fail (mutation 自检覆盖)
   - 每个测试 PR body 写"删除 assert 行 → 测试变绿,证明 discriminating"

6. **Per-commit codex review** (CLAUDE.md #8): 每 commit 跑 `codex exec review --uncommitted --full-auto --sandbox read-only`, ≤2 轮,S0/S1 必修,S2/S3 票

## §F 新文件包归属 (frozen 验证)

| 新文件 | 目标包 | frozen? |
|---|---|---|
| `backend/internal/hermes/{service,audit,keys}.go` | `backend/internal/hermes/` | **No** (新包) |
| `backend/internal/hermeshttp/{settings,chat,conversations}_handler.go` | `backend/internal/hermeshttp/` | **No** (新包,避开 gatewayhttp) |
| `backend/sql/migrations/0057_hermes_core_tables.{up,down}.sql` | SQL,不属 Go 包 | n/a |
| `backend/sql/queries/hermes.sql` + sqlc gen `backend/internal/db/hermes/` | `backend/internal/db/hermes/` | **No** (db/ 不属 frozen) |
| `deploy/hermes/Dockerfile` | infra,非 Go | n/a |
| `backend/docker-compose.dev.yml` (edit) | 既有文件,非 Go | n/a |
| `cmd/gateway/routes.go` (wire only) | `backend/cmd/gateway/` | **wire-only edit**,not adding new file |

**冻结包 (CLAUDE.md #13) `gatewayhttp/gateway/proto` 不加任何新文件**;cmd/gateway/routes.go wire 是允许的"既有文件 bug-fix edit"。

## §G 参考项目对照 (clean-room source-must-read per CLAUDE.md #12)

已读 (≤30 day recency):
- ~/refs/CLIProxyAPI/ (zhanglunet/CLIProxyAPI@21fad9db) Go MIT,pip-free Go 单 process 反差
- ~/refs-latest/hermes-agent-main/ (NousResearch/hermes-agent@v0.14.0) Python MIT,mcp_serve.py + agent/ + skills/
- 本 plan 引用的其他 ref (LiteLLM/sub2api/new-api/Portkey) 仅 cite,不做 deep dive (本切片不需要)

**Fusion-upgrade 三维 (CLAUDE.md #12)**:
- 架构升级: HUAKAI Gateway + Hermes runner 双进程 vs sub2api/new-api 单 process gateway → Hermes 自带 multi-channel + skills 生态嫁接 HUAKAI 信任链
- 算法升级: 复用 hermes-agent 自带 conversation_loop + context_engine + credential_pool,HUAKAI 加 RBAC + audit + dedicated key 层
- 生态升级: HUAKAI admin UI + skill pack + audit trail + dedicated billing 让 ops 拿到 Hermes 的运营观感

## §H Lane provenance

- Source files read: `docs/plans/2026-05-24-hermes-native-integration.md`,`~/refs-latest/hermes-agent-main/pyproject.toml`,`~/refs-latest/hermes-agent-main/mcp_serve.py`,`backend/sql/migrations/0056_*.sql`,`backend/internal/auth/api_key_resolver.go`,`backend/cmd/gateway/routes.go`,`CLAUDE.md`,`AGENTS.md`
- Lane: specifier (Claude PM-orchestrator)
- UTC: 2026-05-25T11:30:00Z

## Owner 中文摘要

Hermes phase-1 Slice 1 我建议这样切:**4 张表 + 5 个 endpoint + 1 个 docker service + 基础 audit**,3-7 天完工。8 个关键决策点我都给了 ABCD 选项 + 推荐 + 参考项目对照,Owner 拍板后 codex 实施。最关键 3 个:**D1 表分批**(推荐 B 4 张 vs main plan 5 件全建)、**D2 dedicated key 存哪**(推荐 A 复用现有 admin/operator key 表)、**D4 runner 鉴权**(推荐 A shared secret + HMAC)。冻结包 (gatewayhttp/gateway/proto) 完全不动,新代码全部进新包 hermes/ + hermeshttp/。Codex lane 在后台同时独立 draft 同名 plan,我读到 codex 版本后做 synthesis surface Owner。
