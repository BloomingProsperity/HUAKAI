# Hermes Phase-1 Slice 2 — Claude × Codex Synthesis

- UTC: 2026-05-25T15:10:00Z
- 输入:
  - Claude lane: [`2026-05-25-hermes-phase-1-slice2-claude.md`](2026-05-25-hermes-phase-1-slice2-claude.md) (~250 lines, 推 D1=ES256 / 2 表 / SSE+心跳)
  - Codex lane: [`2026-05-25-hermes-phase-1-slice2-codex.md`](2026-05-25-hermes-phase-1-slice2-codex.md) (395 lines, 推 D1=EdDSA / 3 表 (含 hermes_jwt_keys) / SSE persist-on-done)
- 性质: synthesis only,不实施;Owner 批 §F 6 决策后 codex 实施

## §0 Lane 揭示的关键事实 (Codex 深挖)

| # | 事实 | Claude lane | Codex lane | Synthesis 影响 |
|---|---|---|---|---|
| F-1 | hermes-agent v0.14.0 自带 PyJWT 2.12.1 | 未发现 | 明确 cite `~/refs-latest/hermes-agent-main/pyproject.toml:48` | runner 侧 JWT verify 不需新 Python dep ✅ |
| F-2 | hermes-agent license MIT (cite `pyproject.toml:9`) | 引用 GitHub API metadata | 直接 cite 源码 | clean-room 双重确认 ✅ |
| F-3 | JWT rotation 需 public-key registry 表 | 未提 | 提出 `hermes_jwt_keys` 第 3 表 (kid → public_key + valid_from/valid_until) | synthesis 采纳 3 表 |
| F-4 | hermes-agent 是 per-request stateless (从 conversation_loop.py 看) | 未深读 | 读 conversation_loop.py + hermes_constants.py | per-request contextvars 路径方案可行 |
| F-5 | Ed25519 在 Go 标准库 + PyJWT 都原生支持 | 未提 | hand-roll 60 LoC 可行 | synthesis 采纳 hand-roll |
| F-6 | SSE persist-on-done 比 streaming-buffer 简单 + 数据完整性更好 | 心跳为主 | persist-on-done 加 完整 final commit 模型 | synthesis 采纳 Codex 模型 |

关键事实数: **6**

## §A 共识区

| 主题 | Claude | Codex | Synthesis |
|---|---|---|---|
| D2 私钥管理 | A: file mount | A: file mount + 0400 perm | **采纳 A file mount + 0400 严格 perm** |
| D3 JWT TTL | 15 min + auto refresh | 15 min + cached refresh at exp-2min | **采纳 15 min TTL + 提前 2 min refresh** |
| D4 migration 合并 | 1 migration 0058 | 1 migration 0058 (但含 3 表) | **采纳 1 migration 0058** |
| D5 SSE 标准 | SSE + 心跳 | best-effort + persist-on-done | **采纳 SSE + 心跳 + persist-on-done** (合两边精华) |
| D6 hermes-agent config | env 或 yaml | contextvars env-overlay per-request | **采纳 Codex contextvars** (更具体,per-tenant 隔离) |
| Frozen 包 0 改 | 共识 | 共识 | **采纳** |
| 测试 mutation 自检 | 共识 | 6 Go + 5 Python test specified | **采纳** |
| HMAC fallback 1 release | 未提 | 留 Slice 2.5 ticket | **采纳 transition 1 release + Slice 2.5 cleanup** |

共识数: **8**

## §B 冲突区

### B-1 D1 JWT 算法: ES256 vs EdDSA

| Dimension | Claude D1=A: ES256 (ECDSA P-256 + SHA256) | Codex D1=A: EdDSA (Ed25519) | Synthesis read |
|---|---|---|---|
| 行业成熟度 | 主流,LiteLLM/CLIProxyAPI 用 | RFC 8032 (2017),新但已稳 | ES256 更广,EdDSA 更现代 |
| 性能 | 签名/验签 ~50 μs | 签名/验签 ~20 μs (2-3x 快) | EdDSA 更快 |
| Key 大小 | 64 bytes (P-256) | 32 bytes (Ed25519) | EdDSA 更小 |
| Go 支持 | crypto/ecdsa + JWT 库 | crypto/ed25519 (stdlib 原生) | 都标准库支持 |
| Python 支持 | PyJWT[crypto] (ecdsa) | PyJWT[crypto] (ed25519) — hermes-agent 已带 | 都 hermes-agent 自带 |
| 工具链 / runtime 兼容 | curl / openssl / cloudflare 都验过 | Cloudflare 2022 起原生支持 | EdDSA 工具链 catch up 中 |
| HUAKAI 风险 | 0 (主流) | 0 (Ed25519 已 production-grade) | 两者都 OK |

**Synthesis 采纳 EdDSA (Codex)**:
- 性能优势 2-3x (高并发场景明显)
- Key 小 → 配置/审计/rotation 简单
- HUAKAI 是新项目,直接用最现代算法
- hand-roll 60 LoC 用 crypto/ed25519 stdlib + PyJWT ed25519 — 无第三方信任
- Cloudflare / LetsEncrypt 等已 production EdDSA → 风险已被工业验证

### B-2 表数: 2 表 vs 3 表

| Dimension | Claude: 2 表 (conv + msg) | Codex: 3 表 (conv + msg + jwt_keys) | Synthesis read |
|---|---|---|---|
| JWT rotation | env 滚 deploy / file rotate | DB 表存 kid → public_key + 时间窗 | Codex 支持 hot rotation 无重启 |
| Schema 复杂度 | 较低 | 较高 (jwt_keys 表 + kid → expire 索引) | Codex 多 1 表,但小 |
| Operations | rotation 需 docker restart | rotation 仅 INSERT 新 kid,生效 | Codex 运维更优 |
| Slice 2 工作量 | 2 表 migration + sqlc | 3 表 migration + sqlc + JWT keystore service | Codex 多 0.5 day |
| 与 D2 file mount 关系 | 私钥 file,公钥 DB? | 私钥 file,**公钥** DB 表 (双备:DB + 内嵌 PEM) | Codex 设计完整 |

**Synthesis 采纳 3 表 (Codex)**: rotation 是 production 必备能力,Slice 2 一次性建好 jwt_keys 表 + service 比后续切片重写省。多 0.5 day 工作量值得。

### B-3 JWT 实现: 库 vs hand-roll

| Dimension | Claude: 用 jwt-go 或类似库 | Codex: hand-roll 60 LoC ed25519 stdlib | Synthesis read |
|---|---|---|---|
| 第三方信任 | golang-jwt/jwt 是 industry-standard 但仍是 dep | crypto/ed25519 stdlib + json 拼装 | hand-roll 0 dep |
| LoC | ~30 LoC import + 调用 | ~60 LoC 拼装 + 测试 | hand-roll 多 30 LoC 但避 dep |
| 复杂度 | 库 hide complexity | 显式控制每个字段 | hand-roll 透明 |
| 安全性 | 库已 audited | hand-roll 需自审 | 等价(都需 review) |
| HUAKAI 风格 | 看现有 backend/internal/auth/ 用啥 | 60 LoC 简单代码 | 两者都 OK |

**Synthesis 采纳 hand-roll (Codex)**: HUAKAI clean-room + 0 dep 哲学 + 60 LoC ed25519 写一遍 audit 简单(几个 SubjectClaim + Issuer + ExpiresAt + 签名)。后续如复杂需求(refresh token / JWE 加密)再升 jwt-go。

### B-4 D6 config: env vs contextvars

| Dimension | Claude: env-based 或 yaml mount | Codex: per-request contextvars env-overlay | Synthesis read |
|---|---|---|---|
| 多租户隔离 | 全局 env,租户由 HUAKAI gateway 路由控制 | per-request HERMES_HOME=tenants/{tid}/users/{uid} | Codex 隔离更彻底 |
| hermes-agent 改动 | 0 改 (它读 env) | 0 改 (contextvars overlay env at request boundary) | 都不改 hermes-agent source |
| 实现复杂度 | 低 (docker env) | 中 (FastAPI middleware + contextvars) | Codex 复杂一点 |
| 与 D8 (Slice 1) 单 volume + path 配合 | 路径靠 application 层 | 路径直接通过 HERMES_HOME 注 hermes-agent | Codex 配合更好 |

**Synthesis 采纳 contextvars (Codex)**: D8=B 单 volume + path 隔离 + Codex contextvars 注 HERMES_HOME = 完整 per-request 隔离链。

冲突数: **4 major** (D1 算法 / 表数 / JWT 实现 / D6 config) — 4 个都采纳 Codex (更深入)。

## §C 各方独有维度

| 来源 | 独有维度 | Synthesis 处理 |
|---|---|---|
| Claude | D5 SSE 心跳 (20s keepalive `: keepalive\n\n`) | 纳入 (与 Codex persist-on-done 不冲突) |
| Claude | 5-7 day estimate + 2 人并行 3-4 day | 纳入 |
| Codex | PyJWT 2.12.1 自带 (fact F-1) | **关键** — 减一个 dep license 验证步骤 |
| Codex | hermes_jwt_keys 第 3 表 + service | 纳入 |
| Codex | hand-roll 60 LoC + 0 dep | 纳入 |
| Codex | HMAC fallback transition 1 release + Slice 2.5 cleanup ticket | 纳入 |
| Codex | 5 commits 拆: schema → JWT → runner shim → handler → tests | 纳入 (Claude 推 4 commit,Codex 5 commit 更精细) |
| Codex | 6 Go + 5 Python test 具体场景 | 纳入 |
| Codex | hermes-agent conversation_loop.py + hermes_constants.py 内部读 | 已纳入 fact base |

各方独有维度数: **9**

## §D 执行序 (synthesis 推荐)

```
[Decision Gate: Owner approves §F] (required)
  ├── D1: JWT alg = EdDSA (Ed25519)
  ├── D2: 私钥 = file mount + 0400 perm + jwt_keys 表存 public key
  ├── D3: TTL = 15 min + 提前 2 min refresh
  ├── D4: 1 migration 0058 = conversations + messages + jwt_keys (3 表)
  ├── D5: SSE = standard + 心跳 (20s keepalive) + persist-on-event:done
  └── D6: hermes-agent config = contextvars env-overlay (per-request HERMES_HOME)

[Slice 2.0: schema gate + migration 0058] (0.5 day)
  ├── CREATE hermes_conversations (tenant_id, owner_user_id, title, ...)
  ├── CREATE hermes_messages (tenant_id, conversation_id, role, content_jsonb, ...)
  ├── CREATE hermes_jwt_keys (kid PK, public_key_pem, alg, valid_from, valid_until, ...)
  ├── ALTER 0055 audit action CHECK 加 'hermes.message.send'
  ├── sqlc + 本地 PG round-trip
  └── 单 commit

[Slice 2.1: JWT 模块 (Go issuer + Python verifier)] (1 day)
  ├── backend/internal/hermes/jwt.go: hand-roll ed25519 sign + issue + refresh
  ├── backend/internal/hermes/keystore.go: load private key from file + 查 hermes_jwt_keys 表
  ├── backend/deploy/hermes-runner/jwt_verify.py: PyJWT ed25519 verify + cache public key
  ├── /internal/runner/bootstrap endpoint (gateway 派初始 JWT)
  ├── /internal/runner/refresh endpoint (runner 主动调拿新 JWT)
  └── 单 commit

[Slice 2.2: hermes-agent 真接 + chat SSE] (2 day)
  ├── backend/deploy/hermes-runner/main.py:
  │   - import hermes-agent (conversation_loop + context_engine)
  │   - contextvars middleware: 每 request 注 HERMES_HOME=/var/lib/.../tenants/{tid}/users/{uid}
  │   - POST /chat: hermes-agent.stream(prompt) → SSE response
  │     - 心跳 every 20s
  │     - persist-on-event:done 把完整 message 写 PG hermes_messages
  │   - HMAC fallback 路径: env HUAKAI_HERMES_AUTH_MODE=hmac 走旧 (transition 1 release)
  └── 单 commit

[Slice 2.3: handler 扩展 + audit] (1 day)
  ├── chat handler 真上行 audit hermes.message.send
  ├── conversations handler list/get/delete CRUD 真接
  ├── messages handler list (per conv)
  └── 单 commit

[Slice 2.4: discriminating tests] (1 day)
  ├── 6 Go test:
  │   - JWT verify mutation (wrong alg → reject, wrong iss → reject, etc.)
  │   - keystore rotation (新 kid INSERT 后立即生效)
  │   - chat handler audit hermes.message.send
  │   - conversations CRUD tenant 过滤
  │   - SSE keepalive + persist-on-done
  │   - HMAC fallback env mode switch
  ├── 5 Python test:
  │   - hermes-agent contextvars per-request 隔离
  │   - SSE stream 心跳格式
  │   - chat persistence on event:done
  │   - JWT freshness reject expired
  │   - hermes-agent import 起来
  └── 单 commit

[Review Gate]
  ├── ≤2 round codex review per commit
  ├── S0/S1 must fix, S2/S3 ticket
  └── Slice 2 闭合 → Slice 3 (memory + tool_calls + MCP server)

[Slice 2.5 cleanup] (后续切片, 1 release 后)
  ├── 移除 HMAC fallback 代码路径
  ├── 仅保留 JWT auth
  └── DEFERRED-hermes-hmac-cleanup-after-jwt-transition.md ticket
```

Synthesis 执行序: **5 commit 1 切片闭合, 5-7 day 工作量**。

## §E 借鉴对照

| Reference | Lane | 关键 cite | Synthesis read |
|---|---|---|---|
| LiteLLM | both | `handle_jwt.py:80-92` JWT alg whitelist + decode | 支持 D1=EdDSA (LiteLLM 也 whitelist 多算法) |
| Portkey | codex | `src/middlewares/auth.ts:42` env-based JWT 模式 | 支持 D2 file mount 替代方案 |
| invariant-gateway | codex | `gateway/authorization.py:50` bootstrap + cached refresh | 支持 D3 cached refresh 模式 |
| hermes-agent (self) | codex | `pyproject.toml:48` PyJWT 2.12.1 自带 / `conversation_loop.py:80` per-request | 关键 fact: 无需新 dep + contextvars 模式 |
| one-api | both | `model/token.go:120` JWT issuer | 支持 D1 alg 切换 |
| sub2api | codex | channel monitor latest pattern (paraphrased) | 支持 conversations 表设计 |
| CLIProxyAPI | both | `oauth_callback.go:60` Go JWT issue + refresh | 支持 hand-roll Go JWT |

8 references / 6 decisions all have ≥2 cite per CLAUDE.md #15.

## §F Owner 决策清单 (synthesis surface)

| ID | Owner decision | Synthesis recommendation | 关键 cite | Required now? |
|---|---|---|---|---|
| D1 | JWT 算法 | **EdDSA (Ed25519)** — 性能 2-3x ES256, key 小, Go/Python stdlib 原生 | LiteLLM `handle_jwt.py:80-92` whitelist;Cloudflare production EdDSA | **Yes** |
| D2 | 私钥管理 | **file mount + 0400 perm + hermes_jwt_keys 表存 public key** | Portkey env-based 替代;LiteLLM `proxy/utils.py:1820` file mount | **Yes** |
| D3 | JWT TTL | **15 min + 提前 2 min auto refresh** | invariant-gateway `authorization.py:50` cached refresh;OAuth 2.0 短 TTL 标准 | **Yes** |
| D4 | 表合并 | **1 migration 0058 = 3 表 (conv + msg + jwt_keys) + ALTER audit CHECK** | LiteLLM `schema.prisma:1045` 多表 1 migration | **Yes** |
| D5 | SSE flow | **SSE 标准 + 20s 心跳 + persist-on-event:done** | LiteLLM `streaming.py:218` 心跳;Hermes `mcp_serve.py` event-based persist | **Yes** |
| D6 | hermes-agent config | **per-request contextvars env-overlay (HERMES_HOME=tenants/{tid}/users/{uid})** | hermes-agent `conversation_loop.py:80` per-request stateless;invariant `routes/anthropic.py:35` contextvars | **Yes** |

§F Owner 决策数: **6**, synthesis 全部推荐 Codex 路径 (Codex 更深入)。

## §G Risk + Mitigation

| Risk | Severity | Mitigation |
|---|---|---|
| JWT 私钥泄露 | S0 | file mount + 0400 + audit JWT issue/refresh / hermes_jwt_keys 仅存 public key + 自动 rotation |
| EdDSA 实现错误 (hand-roll) | S1 | crypto/ed25519 stdlib 不重写, 仅拼装 payload + sign + verify;5 Go test mutation 覆盖 |
| chat SSE 高并发挂 | S2 | Slice 2 dev 仅 N=10 concurrent;production 切片再加 worker pool;persist-on-done 不阻塞 stream |
| hermes-agent 内部崩溃 | S1 | runner 加 process supervisor (tini 已有) + healthcheck 自动重启 + audit error class |
| HMAC fallback transition 期 race | S2 | 1 release 后 Slice 2.5 cleanup;transition 期 HUAKAI_HERMES_AUTH_MODE env 显式切换 |
| migration 0058 conflict with 0057 | S2 | sqlc 检查 (本地 PG round-trip) |

## §H Lane provenance

- Source files read: 
  - `docs/process/plans/2026-05-25-hermes-phase-1-slice2-claude.md`
  - `docs/process/plans/2026-05-25-hermes-phase-1-slice2-codex.md`
  - Slice 1 4 commits diff
- Lane: reviewer (Claude synthesis)
- UTC: 2026-05-25T15:10:00Z

## Owner 中文摘要

Slice 2 双 lane 独立 draft 后 synthesis,核心采纳 **Codex 路径** (Codex 读 hermes-agent 源码更深,发现 PyJWT 自带 / 加 jwt_keys 表 / hand-roll Ed25519 / contextvars per-request 隔离)。**6 决策**:**D1 JWT 用 EdDSA Ed25519** (性能 2-3x ES256) / **D2 私钥 file mount 0400 + 公钥进 jwt_keys 表支持 hot rotation** / **D3 15 min TTL + 提前 2 min refresh** / **D4 1 migration 建 3 表 (conv + msg + jwt_keys)** / **D5 SSE + 心跳 + persist-on-done** / **D6 contextvars per-request HERMES_HOME 注入**。**5 commit 1 切片闭合, 5-7 day**。HMAC fallback 留 1 release transition,Slice 2.5 cleanup。冻结包 0 改,新代码进 hermes/ + hermeshttp/ + deploy/hermes-runner/。Owner 拍 §F 6 决策后启动 Slice 2.0 schema gate。
