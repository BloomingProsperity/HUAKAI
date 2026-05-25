# Hermes Phase-1 Slice 1 — Claude × Codex Synthesis

- UTC: 2026-05-25T11:45:00Z
- 输入:
  - Claude lane: [`2026-05-25-hermes-phase-1-slice1-claude.md`](2026-05-25-hermes-phase-1-slice1-claude.md) (~250 lines, 推 D1=B 4 张表/D4=A shared-secret-static)
  - Codex lane: [`2026-05-25-hermes-phase-1-slice1-codex.md`](2026-05-25-hermes-phase-1-slice1-codex.md) (239 lines, 推 D1=A 3 张表/D4=B-then-C 演进)
- 性质: synthesis only,不实施;Owner 批 §F 决策后 codex 实施

## §0 Lane 揭示的关键事实

| # | 事实 | Claude lane | Codex lane | Synthesis 影响 |
|---|---|---|---|---|
| F-1 | Hermes runner (`hermes-agent v0.14.0`) 自带 `conversation_loop` + `context_engine`,自己持 chat state | 未明确 | 明确 (runner 自持 conversations) | conversations 表不是 Slice 1 硬需求 |
| F-2 | main plan §First Implementation Slice 5 件中 #4 "Dedicated API key" 没明确 storage 位置 | 推 A 复用 `auth/api_key_resolver` | 推 A 复用 `api_keys` 表 + `purpose='hermes_runner'` 列 | 共识 — 复用 + 加 purpose 列 |
| F-3 | hermes-agent v0.14.0 license 必须先验 (per CLAUDE.md permitted-license-vendoring) | 未明确 (假定 MIT) | 显式 risk 列出 | Synthesis 必须 verify license 才进 §C 实施 |
| F-4 | composite tenant FK 模式既有 (migration 0041 `tenant_composite_foreign_keys`) | 未引用 | 明确引用 | Slice 1 必须沿用 |
| F-5 | `external_api` mode 是否 Slice 1 支持 | 推 3 模式都支持 | 明确 out of scope, 只支持 managed + dedicated_group | synthesis 采纳 Codex (external_api 涉及加密 secrets 需独立 schema gate) |
| F-6 | SSE backpressure load test 风险 | 未列 | 列入风险 §8 | synthesis 纳入 risk |
| F-7 | D4 演进路径 (Slice 1 shared-secret → Slice 2 JWT → mTLS 留 production) | 推静态 A shared-secret | 推 B-then-C path | synthesis 采纳 Codex (更清晰演进路径) |

关键事实数: **7**

## §A 共识区

| 主题 | Claude | Codex | Synthesis |
|---|---|---|---|
| 新包命名 | `hermes/` + `hermeshttp/` | 同 | **采纳** |
| 冻结包不碰 | 共识 | 共识 | **采纳** (gatewayhttp/gateway/proto 0 新文件) |
| Migration 序号 | 0057 | 0057 | **采纳** |
| D2 dedicated key | A 复用 api_keys + purpose 列 | A 同 | **采纳 A** |
| D3 runner 启动 | A always-on | A 同 | **采纳 A** |
| D6 hermes-agent lock | A pin tag + 记 SHA | A pin sha + `--require-hashes` | **采纳 A (Codex 更严)** |
| D7 MCP server | B 不在本切片建 | C 完全 defer | **采纳 defer** (语义相同) |
| D8 tenant 隔离 | A 单 volume + path | B 同 | **采纳 B (synthesis 序号对齐 Codex)** |
| audit events 字段 | actor/tenant/action/sanitized_args/result/correlation_id | actor_user_id/action/sanitized_args/result/correlation_id/request_id | **采纳 Codex (多 request_id 字段更完整)** |
| 测试 mutation 纪律 | 共识 (CLAUDE.md #14) | 同 | **采纳** |

共识数: **10**

## §B 冲突区

### B-1 Core Conflict: D1 表数 (3 张 vs 4 张)

| Dimension | Claude D1=B: 4 张含 conversations | Codex D1=A: 3 张不含 conversations | Synthesis read |
|---|---|---|---|
| Schema 颗粒度 | settings + api_profiles + **conversations** + audit | settings + api_profiles + audit | Codex 更小,小切片闭合更易 |
| chat 验证路径 | conversations 表本切片就能查 | runner 自持,Slice 1 chat 只 proxy 不落 PG | Codex 更纯,runner 是 chat state 真相源 |
| admin UI 可查性 | 本切片 admin 即可查 conversations | Slice 2 admin 才能查 (admin 用 audit row + correlation_id 跨查) | 本切片 admin 用 audit_events 查 chat.start 就够,conversations CRUD 推后 |
| 实施 step 数 | +1 表 + sqlc + handler GET conversations 完整 | -1 表 + GET conversations 仅 proxy runner | Codex 减一步实施 |
| Slice 2 cost | Slice 2 加 messages + memory + tool_calls + runtime_events | Slice 2 加 conversations + messages,Slice 3 加 memory + tool_calls + runtime_events | 都 OK,Codex 拆得更均匀 |

**Synthesis 采纳 D1=A (Codex, 3 张表)**:
- Hermes runner 是 chat state 真相源 (~/refs-latest/hermes-agent-main/agent/conversation_loop.py),Slice 1 不与 runner 抢真相源
- 更小切片符合 [[feedback_small_closed_increments]] (Owner 2026-05-22 小切片闭合纪律)
- audit_events 表已有 correlation_id 字段,admin 用 audit row 查 chat 行为足够 MVP
- conversations 表 Slice 2 时和 messages 一起 lift,设计更一致

### B-2 Conflict: D4 演进路径

| Dimension | Claude D4=A: shared-secret + HMAC 静态 | Codex D4: B (slice-1) → C (slice-2 JWT) | Synthesis read |
|---|---|---|---|
| Slice 1 落地速度 | A 静态,几小时 | B 同样几小时 | 等效 |
| 长期 audit | 无 per-process identity | C JWT 提供 per-process audit identity | Codex 更稳 |
| 维护负担 | rotate 时滚 compose deploy | rotate 时 token TTL 自然循环 | Codex 长期更稳 |
| mTLS 时机 | 留远期 | A (mTLS) "only if Owner requires for compliance" | 等效 (都 defer mTLS) |

**Synthesis 采纳 Codex 演进路径**: Slice 1 = shared-secret + HMAC,Slice 2 = signed JWT,mTLS 留 production-cert-rotation 切片或 Owner 显式要求。

### B-3 Conflict: D5 后续表分批

| Synthesis 决策 | Slice 2 加 conversations + messages,Slice 3 加 memory_items + tool_calls + runtime_events |
|---|---|
| 理由 | Codex 拆得均匀 — conversations + messages 同事务范畴,memory/tool_calls/runtime_events 需 MCP server (D7 Slice 3) 才有意义 |

冲突数: **3 major**

## §C 各方独有维度

| 来源 | 独有维度 | Synthesis 处理 |
|---|---|---|
| Claude | Fusion-upgrade 三维明确 (架构/算法/生态) | 纳入 (CLAUDE.md #12 强制) |
| Claude | 2 人并行 time estimate 2 day | 纳入 (Codex + Claude review 并行) |
| Codex | Migration 名 `0057_hermes_phase1_slice1_core` 更明确 | 纳入 |
| Codex | composite tenant FK 沿用 0041 模式 | **关键** — schema gate 必须沿用 |
| Codex | `external_api` mode out of scope (Slice 1 仅 managed + dedicated_group) | 纳入 (external 涉及加密 secrets 需独立 schema) |
| Codex | SSE backpressure load test 风险 | 纳入 risk |
| Codex | hermes-agent license verification gate | **必须** — §C 实施前 verify |
| Codex | 5 个具体 mutation test 场景 | 纳入 |
| Codex | endpoint 加 `GET /v1/hermes/conversations/{id}/messages` (6 个 endpoint) | 纳入 — chat proxy 后能看 messages 是合理 MVP |
| Codex | `api_keys` 表加 `purpose TEXT NOT NULL DEFAULT 'user'` 列 + partial index | 纳入 — clean migration |

各方独有维度数: **10**

## §D 执行序 (synthesis 推荐)

```
[Decision Gate: Owner approves §F] (required)
  ├── D1: confirm 3 tables (settings + api_profiles + audit_events)
  ├── D2: confirm reuse api_keys + purpose='hermes_runner' column
  ├── D3: confirm always-on runner
  ├── D4: confirm shared-secret Slice 1 → JWT Slice 2 path
  ├── D5: confirm conversations+messages → Slice 2, memory+tool_calls+runtime → Slice 3
  ├── D6: confirm sha-pin + --require-hashes
  ├── D7: confirm MCP defer 完全到 Slice 3
  └── D8: confirm single volume + path-prefix

[License Gate]
  └── verify hermes-agent v0.14.0 license 是 MIT (per CLAUDE.md permitted-vendor-policy)
      - 已知: NousResearch/hermes-agent 是 MIT (project_huakai_better_than_sub2api 时已 cite)
      - 但 Slice 1 实施前必须 fresh-fetch pyproject.toml 验证 license 行 + record SHA

[Slice 1.0: schema gate] (0.5 day)
  ├── migration 0057_hermes_phase1_slice1_core.{up,down}.sql
  │   - ALTER api_keys ADD COLUMN purpose TEXT NOT NULL DEFAULT 'user'
  │   - CREATE TABLE hermes_settings (composite tenant FK 沿 0041)
  │   - CREATE TABLE hermes_api_profiles
  │   - CREATE TABLE hermes_audit_events
  ├── sqlc queries hermes_*.sql
  ├── 本地 PG huakai_roundtrip up → down → up roundtrip verify
  └── 单独 commit "hermes schema gate Slice 1 migration 0057"

[Slice 1.1: service + handler] (1.5 day)
  ├── backend/internal/hermes/{settings,profiles,audit,runner_client,types}.go
  ├── backend/internal/hermeshttp/{settings,chat,conversations,profiles,router}_handler.go
  ├── 6 endpoints (含 GET /v1/hermes/conversations/{id}/messages 仅 proxy runner)
  ├── audit emit 在 enable/disable/profile.create/profile.rotate/chat.start 5 事件
  ├── wire 进 cmd/gateway/main.go (only frozen-package-adjacent edit)
  └── 单独 commit "hermes service + handler Slice 1.1"

[Slice 1.2: runner + compose] (0.5 day)
  ├── backend/deploy/hermes-runner/Dockerfile (python:3.12-slim + pip --require-hashes)
  ├── backend/deploy/hermes-runner/requirements.txt (hermes-agent==0.14.0 + sha256)
  ├── backend/docker-compose.dev.yml 加 hermes-runner stanza
  ├── healthcheck + shared-secret env
  └── 单独 commit "hermes-runner docker compose service"

[Slice 1.3: discriminating tests] (0.5 day)
  ├── settings_test.go (mutation: 删 WHERE tenant_id → tenant 串)
  ├── audit_test.go (mutation: 删 sanitize → secret leak)
  ├── chat_proxy_test.go (mutation: 删 shared-secret middleware → 401 bypass)
  ├── profile_crypto_test.go (mutation: stub encrypt to identity → leak)
  ├── tenant_isolation_test.go (mutation: 删 tenant filter → 跨租户 enable)
  └── 单独 commit "hermes discriminating test suite"

[Review Gate]
  ├── 每 commit 跑 codex exec review --uncommitted --full-auto --sandbox read-only
  ├── ≤2 round 每 commit, S0/S1 must fix, S2/S3 ticket
  ├── 本地 go test ./... 全绿
  └── PR body 列 mutation 自检证据 (per CLAUDE.md #14)
```

Synthesis 实施序: **schema → service+handler → runner+compose → tests**,4 commit 1 切片闭合,3 天工作量。

## §E 借鉴对照 (synthesis 收敛 — 两 lane cite 合并)

| Reference | Lane | 关键 cite | Synthesis read |
|---|---|---|---|
| one-api | both lanes | `model/token.go:23-37` 单表多 purpose 模式 | 支持 D2=A (复用 api_keys + purpose 列) |
| LiteLLM | both lanes | `proxy/management_helpers/audit_logs.py:182-220` 通用 object_id audit row | 支持 D1=A (audit 表早落,history 表后落) + D2=A |
| LiteLLM | both lanes | `proxy/memory/memory_endpoints.py:278-541` memory CRUD 分离 | 支持 D5 (conversations Slice 2,memory Slice 3) |
| LiteLLM | both lanes | `proxy/_experimental/mcp_server/byok_oauth_endpoints.py:50-92` JWT auth | 支持 D4 演进 (Slice 2 升 JWT) |
| LiteLLM | both lanes | `proxy/_experimental/mcp_server/mcp_server_manager.py` MCP 独立包 | 支持 D7 defer 到独立包 |
| CLIProxyAPI | both lanes | `internal/api/handlers/management/api_key_usage.go:11-30` lazy usage telemetry | 支持 D1=A (history 表 v1 可选) |
| CLIProxyAPI | both lanes | `internal/api/handlers/management/oauth_callback.go` runtime config 无重启 | 支持 D3=A (always-on + config 改不重启) |
| invariant-gateway | codex lane | `gateway/serve.py` 单 FastAPI always-on sidecar | 支持 D3=A + D8=B |
| Portkey gateway | codex lane | `src/handlers/services/logsService.ts` header-based auth | 支持 D4 Slice 1 shared-secret |
| sub2api | both lanes | (LGPL — paraphrase only) channel monitor 渐进式建表 | 支持 D1=A 渐进 |
| new-api | both lanes | (AGPL — paraphrase only) `model/token.go` 单表多 tag | 支持 D2=A |
| Hermes-agent | both lanes | `mcp_serve.py` 自带 MCP server + `agent/conversation_loop.py` 自带 chat state | 关键 — Hermes runner 是 chat state 真相源,Slice 1 不抢 |

参考项目对照 (CLAUDE.md #15): **8 projects** (one-api / LiteLLM / CLIProxyAPI / invariant-gateway / Portkey / sub2api / new-api / hermes-agent),每决策 ≥2 cite。

## §F Owner 决策清单 (synthesis surface)

| ID | Owner decision | Synthesis recommendation | 关键 cite | Required now? |
|---|---|---|---|---|
| D1 | 本切片建多少张表? | **A: 3 张 (settings + api_profiles + audit_events)**;conversations+messages Slice 2;memory+tool_calls+runtime Slice 3 | one-api `model/token.go:23-37`;LiteLLM `audit_logs.py:182-220` | **Yes** |
| D2 | dedicated Hermes API key 存哪? | **A: 复用 api_keys 表加 purpose='hermes_runner' 列 + partial index** | one-api `model/token.go:23-37`;new-api 单表多 tag (paraphrased) | **Yes** |
| D3 | hermes-runner 启动模式? | **A: always-on docker (idle 内存 < 200MB 可接受)** | invariant-gateway `serve.py`;CLIProxyAPI `oauth_callback.go` | **Yes** |
| D4 | runner→gateway auth 演进? | **B Slice 1 → C Slice 2**: shared-secret + HMAC 起步,Slice 2 升 signed JWT,mTLS 留 production cert rotation 切片 | LiteLLM `byok_oauth_endpoints.py:50-92`;Portkey `logsService.ts` | **Yes** |
| D5 | 后续 4 表分批序? | **B: Slice 2 加 conversations + messages,Slice 3 加 memory_items + tool_calls + runtime_events** | LiteLLM `memory_endpoints.py:278-541`;`audit_logs.py:160-220` | **Yes** |
| D6 | hermes-agent v0.14.0 lock? | **A: pin `hermes-agent==0.14.0` + `--require-hashes` sha256;Slice 3 升级 sigstore attest** | LiteLLM `pyproject.toml` 精确 pin;invariant-gateway `uv.lock` | **Yes** |
| D7 | MCP server (`huakai.*` tools) 建? | **C: 完全 defer 到 Slice 3 (含 8 个 huakai.* tools 时一起建独立包)** | LiteLLM `mcp_server_manager.py` 独立包模式 | **Yes** |
| D8 | tenant 隔离粒度? | **B: 单 volume + path-prefix per tenant (`/var/lib/huakai/hermes/tenants/{tid}/`)** | invariant-gateway `__init__.py`;LiteLLM `memory_endpoints.py:399-541` | **Yes** |

§F Owner 决策数: **8**

## §G Risk + Mitigation

| Risk | Severity | Mitigation |
|---|---|---|
| hermes-agent v0.14.0 license 未 fresh-verify | S1 | Slice 1 实施前 curl `https://api.github.com/repos/NousResearch/hermes-agent` + `pyproject.toml`,验证 MIT;若非 MIT/Apache-2.0,改 vendor 路径 (per CLAUDE.md permitted-vendor) |
| SSE backpressure 高并发挂死 | S2 | Slice 1 仅 dev compose,production 切片做 load test;Slice 2 评估 WebSocket fallback |
| shared-secret rotate 需滚 compose | S3 | 文档化 rotate runbook;Slice 2 升 JWT 后 token TTL 自然循环 |
| 6 endpoints 测试覆盖不足 | S2 | mutation 自检 5 个场景必须红绿验证,PR body 列证据 |

## §H Lane provenance

- Source files read: 
  - `docs/process/plans/2026-05-25-hermes-phase-1-slice1-claude.md`
  - `docs/process/plans/2026-05-25-hermes-phase-1-slice1-codex.md`
  - `docs/plans/2026-05-24-hermes-native-integration.md`
  - `CLAUDE.md`,`AGENTS.md`
- 未重读 reference 源码 (specifier lane 已 cite,本 reviewer lane 不 re-read per CLAUDE.md #11 lane discipline)
- Lane: reviewer (Claude synthesis)
- UTC: 2026-05-25T11:45:00Z

## Owner 中文摘要

Codex 独立 draft 后我做 synthesis,**核心冲突 3 个**:**D1 表数** (Claude 推 4 张,Codex 推 3 张) — synthesis 采纳 **Codex 3 张** (Hermes runner 自己持 chat 状态,Slice 1 不抢真相源);**D4 auth 演进** (Claude 静态,Codex 阶段 B→C) — synthesis 采纳 **Codex 演进** (Slice 1 shared-secret,Slice 2 升 JWT);**D5 后续分批** (Codex 拆得更均匀) — synthesis 采纳 Codex。剩 5 个决策 (D2/D3/D6/D7/D8) 两 lane 共识。**8 个决策 Owner 必拍**,我都给了 synthesis 推荐 + 至少 2 个参考项目 cite (per CLAUDE.md #15)。**新增 1 个 License Gate**: hermes-agent v0.14.0 license 实施前必须 fresh-verify MIT。实施序:schema gate → service+handler → runner+compose → tests,4 commit 1 切片闭合,3 天工作。我现在 surface 8 个决策给你拍板,推荐都是默认选项,直接 Enter 走 default 也行。
