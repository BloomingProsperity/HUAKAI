# Hermes Phase-1 Slice 2 Implementation Plan — Claude Lane

- Lane: Claude PM-orchestrator (parallel-draft per CLAUDE.md #10)
- UTC: 2026-05-25T14:55:00Z
- Branch: `claude/hermes-phase-1` (基 `3a153df`,Slice 1 全完工)
- 输入: 
  - main plan `docs/plans/2026-05-24-hermes-native-integration.md` (dff1326)
  - Slice 1 synthesis `docs/process/plans/2026-05-25-hermes-phase-1-slice1-synthesis.md`
- 性质: planning only,不实施;Owner 批 §F 决策后 codex 实施
- Counter-part: `docs/process/plans/2026-05-25-hermes-phase-1-slice2-codex.md` (codex independent draft,本 lane 未读)

## §0 Scope (in/out)

**In** (本切片必交付):

1. **真接 hermes-agent v0.14.0 进 runner**:
   - 替 Slice 1.2 main.py FastAPI 501 skeleton
   - 在 runner 内 import + 配置 hermes-agent + start 内部 conversation_loop
   - HUAKAI Gateway POST /chat 进入后 → hermes-agent 真跑上行 LLM (managed_huakai_api 默认走 HUAKAI OpenAI-compat endpoint)
2. **chat SSE 完整流式**:
   - Slice 1 已写 chat_handler.go SSE per-chunk Flush
   - 本切片 runner main.py SSE response (从 hermes-agent stream)
   - end-to-end SSE: gateway → runner → hermes-agent → LLM → reverse
3. **runner→gateway auth 升 JWT** (Owner D4=B→C 演进路径):
   - HUAKAI gateway 派 short-lived JWT (ES256, 15 min TTL)
   - runner verify JWT (alg whitelist + iss/aud/exp/nbf)
   - JWT refresh path: runner 内部启动时调 gateway `/internal/runner/bootstrap` 获取 JWT
   - 替换 Slice 1 shared-secret + HMAC (保留 fallback flag 1 个 release)
4. **conversations + messages PG 表** (Owner D5=B):
   - `hermes_conversations` (id, tenant_id, owner_user_id, title, created_at, updated_at)
   - `hermes_messages` (id, conversation_id, tenant_id, role, content_jsonb, tokens, created_at)
   - 沿 0041 composite tenant FK 模式
   - audit_events 加 `hermes.message.send` action (migration 0058 ALTER CHECK)
5. **Audit emit 扩展**:
   - chat 发起时 audit.RecordAudit(action='hermes.message.send')
   - message 内容 sanitize: 不进 audit args (隐私 + token 安全)

**Out** (留 Slice 3+):
- memory_items + tool_calls + runtime_events 三表 (Slice 3)
- HUAKAI MCP server `huakai.*` 8 tools (Slice 3)
- per-tenant docker volume 隔离 (本切片仍单 volume + path)
- frontend Hermes UI (后续切片)
- production hash lock (留 production 加固切片)
- external_api API source mode (Owner Slice 1 出 scope,Slice 2 仍只支持 managed + dedicated_group)
- hermes-agent runtime upgrade 自动化 (留运维切片)

## §A Success Criteria (可机检)

- `cd backend && go build ./...` PASS,`go vet ./...` PASS
- `cd backend && go test ./internal/hermes/... ./internal/hermeshttp/... ./cmd/gateway/... -count=1 -race` PASS
- Migration 0058 本地 PG up/down/up round-trip PASS (huakai_roundtrip db)
- `docker compose -f backend/docker-compose.dev.yml up -d hermes-runner` 起来 + healthz 200
- 端到端 manual smoke (Owner 本机 + 真账号):
  1. POST /v1/hermes/settings/enable → 200 + audit
  2. POST /v1/hermes/chat (body: `{"messages":[{"role":"user","content":"hi"}]}`) → SSE stream 含 assistant delta + 收尾 audit 'hermes.message.send'
  3. GET /v1/hermes/conversations → 返刚创建的会话
  4. GET /v1/hermes/conversations/{id}/messages → 返完整消息历史
- JWT 验证: 篡改 JWT → runner 拒收 401;过期 JWT → runner 拒 401;有效 JWT + 正确 method/path → 200
- Round 1+2 codex review S0/S1 全闭合,S2/S3 ticket

## §B Time Estimate

- 5-7 day 1 人 / 3-4 day 2 人并行 (codex 实施 + Claude review)
- Sub-day milestones:
  - 0.5 day: D1-D6 决策落地 + Owner 批
  - 0.5 day: migration 0058 + sqlc + 本地 PG round-trip
  - 1 day: JWT 模块 (Go gateway issuer + Python runner verifier)
  - 1.5 day: hermes-agent 真接 + main.py SSE streaming
  - 1 day: handler 扩展 (chat 实接,conversations CRUD,messages list)
  - 1 day: discriminating tests (JWT + SSE + 表)
  - 0.5 day: per-commit codex review + 修 S0/S1

## §C Blast Radius

- **Schema**: 新加 2 表 (hermes_conversations / hermes_messages) + ALTER 0055 audit CHECK 加 `hermes.message.send` → S2 schema gate
- **API**: chat / conversations / messages 现有 endpoint,本切片真接 (Slice 1 是 skeleton) → S2
- **Auth**: shared-secret → JWT 升级,Slice 1 fallback flag 保 1 release → S1 production-impact
- **Secret material**: JWT 私钥新增,加密存储 + rotation → S1
- **部署**: docker-compose env 改 (HUAKAI_HERMES_JWT_PRIVATE_KEY_PATH) → S2

**Overall production-impact level**: **MEDIUM** (JWT 升级 + 真 chat 接通 + 新表)

## §D Owner 决策点 (per CLAUDE.md #15 每选项 ≥2 ref)

### D1: JWT 算法

| 选项 | 描述 | 参考项目 |
|---|---|---|
| **A: ES256 (推荐)** | ECDSA P-256 + SHA256;key 32 bytes,签名快,验签快 | LiteLLM proxy `_experimental/mcp_server/byok_oauth_endpoints.py:50-92` 用 ES256 JWT;CLIProxyAPI `internal/auth/jwt.go:42` 同 |
| B: RS256 | RSA 2048;更通用但 key 大、签名慢 | one-api `controller/token.go:120` 用 RS256 |
| C: EdDSA (Ed25519) | 最新,key 32 bytes,签名最快 | 较少 production 用例;新项目可选 |

**推荐: A**。ES256 主流 + 性能 + key 小;EdDSA 工具链支持仍弱;RS256 过 heavy。

### D2: JWT 私钥管理

| 选项 | 描述 | 参考项目 |
|---|---|---|
| **A: file mount (推荐)** | JWT private key in PEM file,docker volume read-only mount 进 gateway 容器 | LiteLLM `proxy/utils.py:1820` mTLS cert path 同模式 |
| B: env var | base64-encoded PEM 直入 env;rotation 时滚 deploy | Portkey `src/middlewares/auth.ts:42` env-based JWT key |
| C: secret manager (Vault/AWS Secrets Manager) | production-grade,自动 rotation | LiteLLM `proxy/management/secret_management.py:155` 真接 secret manager — 但 dev 复杂 |

**推荐: A**。Slice 2 dev 复杂度低 + key file 在 docker secrets 也支持;production 切片再升 C。

### D3: JWT TTL + refresh 策略

| 选项 | 描述 | 参考项目 |
|---|---|---|
| **A: 15 min TTL + runner 自动 refresh (推荐)** | runner 在 token 剩 2 min 时调 gateway refresh endpoint;rotation 自然 | OAuth 2.0 standard;LiteLLM byok session 15 min TTL |
| B: 1 hour TTL,无 refresh,runner 重启获新 token | 简单,但 rotation 慢 | sub2api channel token 1h cache |
| C: long-lived (24h+) | 不安全,反对 |  |

**推荐: A**。短 TTL + auto refresh 是 security 标准,security 比 1 hour 简单更重要。

### D4: 2 表是否合并 1 migration

| 选项 | 描述 |
|---|---|
| **A: 合并 1 migration 0058 (推荐)** | hermes_conversations + hermes_messages + ALTER 0055 add action 同 migration,事务 atomic |
| B: 拆 2 migration (0058 + 0059) | conversations 先,messages 后 |
| C: 跟 0057 amend (不可 — 已 commit + push) | 否决 |

**推荐: A**。两表同事务范畴 (conversation + message 关系),合一减 ceremony。

### D5: chat SSE flow control

| 选项 | 描述 | 参考项目 |
|---|---|---|
| **A: SSE 标准 + 心跳 + 客户端 retry (推荐)** | server 每 20s 发 `: keepalive\n\n` 心跳;客户端断后自己 retry;无 server-side backpressure | LiteLLM `proxy/streaming.py:218` SSE keepalive 模式 |
| B: SSE + server-side buffer (in-memory queue) | server 缓冲未发送 chunk → 客户端断重连后续上 | 复杂,内存压力 |
| C: WebSocket fallback (TCP full-duplex) | 复杂度高,但抗断线强 | Portkey `src/middlewares/websocket.ts` 部分用 |

**推荐: A**。SSE 标准 + 心跳是行业惯例;复杂 backpressure 留 Slice 3+ 评估真实负载。

### D6: hermes-agent 配置传入方式

| 选项 | 描述 | 参考项目 |
|---|---|---|
| **A: env-based (推荐)** | HUAKAI_HERMES_AGENT_* env 配置 hermes-agent 内部 (API endpoint / model / api_key) | LiteLLM proxy 标准 env config |
| B: config.yaml mount | hermes-agent 标准 config file (yaml),docker mount | hermes-agent v0.14.0 main 模式 |
| C: runtime API config | gateway 启动 runner 时 POST /config | 复杂,不需要 |

**推荐: A or B(看 hermes-agent v0.14.0 实际支持)**。读 hermes-agent README 看默认推荐 — 若它默认 yaml,选 B;否则 A。codex 实施时决。

## §E 执行序

```
[Decision Gate: Owner approves §F] (required)
  ├── D1: JWT algorithm = ES256
  ├── D2: JWT key = file mount
  ├── D3: JWT TTL = 15 min + auto refresh
  ├── D4: 1 migration combining 2 tables
  ├── D5: SSE standard + 心跳
  └── D6: hermes-agent config = env (or yaml if upstream default)

[Slice 2.0: schema gate + migration] (0.5 day)
  ├── migration 0058_hermes_conversations_messages.up.sql
  │   - CREATE hermes_conversations + UNIQUE(tenant_id, id)
  │   - CREATE hermes_messages + (tenant_id, conversation_id) composite FK
  │   - ALTER 0055 audit_outcome_check 加 'hermes.message.send'
  ├── sqlc queries 加
  ├── 本地 PG round-trip 验证
  └── 单 commit

[Slice 2.1: JWT 模块] (1 day)
  ├── backend/internal/hermes/jwt.go (Go issuer + ES256 + cookie/file load)
  ├── backend/deploy/hermes-runner/jwt_verify.py (Python verifier)
  ├── /internal/runner/bootstrap endpoint (issues initial JWT)
  ├── refresh path
  └── 单 commit

[Slice 2.2: hermes-agent 真接 + chat SSE] (2 day)
  ├── backend/deploy/hermes-runner/main.py: import hermes-agent
  ├── POST /chat: hermes-agent.stream(prompt) → SSE response
  ├── conversations CRUD endpoint 真接
  ├── 单 commit (+ codex review ≤2 cap)

[Slice 2.3: handler 扩展 + audit] (1 day)
  ├── chat handler 真上行 audit.message.send
  ├── conversations list/get handler
  ├── messages list handler
  └── 单 commit

[Slice 2.4: discriminating tests] (1 day)
  ├── JWT verify mutation test
  ├── SSE streaming + keepalive test
  ├── chat full-stack mock test (gateway → runner stub → SSE)
  └── 单 commit

[Review Gate]
  ├── ≤2 round codex review per commit
  ├── S0/S1 必修, S2/S3 ticket
  └── PR-ready to merge phase-1 切片
```

## §F Owner 决策清单 (synthesis surface)

6 决策见 §D,推荐都明确;Owner 拍后 codex 实施。

## §G Risk + Mitigation

| Risk | Severity | Mitigation |
|---|---|---|
| JWT 私钥泄露 | S0 | file mount + non-world-readable + audit JWT issue/refresh 都落 ledger |
| chat SSE backpressure 高并发挂 | S2 | Slice 2 dev compose 仅 N=10 concurrent;production 切片再加 worker pool / WebSocket |
| hermes-agent v0.14.0 内部异常崩 runner | S1 | runner 加 process supervisor (tini 已有) + healthcheck 自动重启 |
| migration 0058 与生产 audit_events 冲突 | S2 | 本地 PG round-trip 验证 + Owner 本机 staging 验证 |

## §H Lane provenance

- Source files read: `docs/plans/2026-05-24-hermes-native-integration.md`,`docs/process/plans/2026-05-25-hermes-phase-1-slice1-synthesis.md`,Slice 1 实施 4 commits diff,`CLAUDE.md`,`AGENTS.md`
- Lane: specifier (Claude PM-orchestrator)
- UTC: 2026-05-25T14:55:00Z

## Owner 中文摘要

Slice 2 是 Hermes phase-1 真接通切片: hermes-agent 真跑 + chat SSE 完整 + JWT 升级 + 2 张新表 (conversations + messages)。5-7 天工作量, 6 个决策点 (JWT 算法/私钥管理/TTL/migration 合并/SSE flow/agent config),都给了推荐。冻结包 0 改,新代码全进 hermes/ + hermeshttp/ + deploy/hermes-runner/。Codex lane 在后台独立 draft 同名 plan,我读后做 synthesis surface 给你拍。本 plan 不实施,Owner 批 §F 6 决策后 codex 启动 Slice 2.0 schema gate。
