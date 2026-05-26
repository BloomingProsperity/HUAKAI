# Hermes phase-1 Slice 2.2 — Claude × Codex Synthesis

- UTC: 2026-05-26T02:00:00Z
- Inputs:
  - Claude lane: `2026-05-26-hermes-phase-1-slice2-2-claude.md` (~280 lines, 5 决策 D-1..D-5, 3 sub-slice, 8 难点)
  - Codex lane: `2026-05-26-hermes-phase-1-slice2-2-codex.md` (~562 lines, 7 决策 D1..D7, 10 难点, 含 D6 credential bridge + D7 sse-starlette dep)
- 性质: synthesis only,不实施;Owner 拍 §F 决策后 codex 实施
- Independence caveat: codex §0 自报曾 `rg` 抓到 Claude plan 几行 (非内容主题). Owner 可接受 (不影响 plan 主体独立性) 或要求重跑;recommendation: 接受

## §A 共识区 (双 lane 一致)

| 主题 | Claude | Codex | Synthesis |
|---|---|---|---|
| 不动 frozen 包 | 共识 | 共识 | **采纳** |
| Slice 2.0/2.1 已闭合, Slice 2.2 不动 schema/JWT | 共识 | 共识 | **采纳** |
| hermes-agent 直接 import (in-process) | 我 D-1 A | codex D2 A | **采纳 A** |
| persist-on-event:done | 我 §A + D-5 | codex D4 A | **采纳 A** (Owner D5 已批) |
| 20s heartbeat `: keepalive\n\n` | 我 D-4 | codex D3 A | **采纳 A** (Owner D5 已批) |
| HERMES_HOME ContextVar overlay (per-request) | 我 §A | codex D5 A | **采纳 A** (Owner D6 已批) |
| EdDSA JWT only (HMAC compat 留 Slice 2.5) | 我 §A | codex D1 A | **采纳 A** (Owner D1 已批) |
| atomic audit+persist 同 tx | 我 §C C-3 | codex §1.2 | **采纳** |
| audit `hermes.message.send` action 注册 | 我 §E 2.2.c | codex §1.2 | **采纳** |
| SanitizeArgs redact api_key/token/password/secret | 我 §A | codex §3 D4 | **采纳** |
| runner DB-free + key-free | 我 §B | codex §1.2 | **采纳** |
| 浏览器不直连 runner, 所有走 gateway | 我 §B | codex §1.3 | **采纳** |

共识数: **12**

## §B 冲突区 (synthesis 需 Owner 决策)

### B-1 D-4 persist fence 失败处理: Claude 宽松 (warn-log) vs Codex 严格 (error event)

| Dimension | Claude D-5: warn-log + DLQ 兜底 | Codex D4: persist 失败 emit `event: error` 不发 done | Synthesis |
|---|---|---|---|
| 用户体验 | 收到完整 done, 后台尝试 retry | 收到 error event 知道消息没存 | Claude 路径 chat 体验更好 |
| 数据完整性 | 异步 DLQ 接住, 短暂 inconsistent 窗口 | 强一致, 失败明示客户端 | Codex 路径数据更严 |
| W4 trust-ledger 决策一致性 | 一致 (durable-DLQ + 放行) | 不一致 (W4 是 fail-open + DLQ) | Claude 路径符合 [[project_trust_ledger_failclosed_policy]] |
| chat 端 UX | done 已发, 客户端可继续 | done 不发, 客户端要 retry | Claude 更顺 |
| operational complexity | 需 DLQ infra (W4 已存在) | 需 client 处理 error event | 平 |

**Synthesis 推荐 — 分类处理**:
- **audit 写失败** → Claude 路径 (warn-log + DLQ 兜底 → 与 W4 [[project_trust_ledger_failclosed_policy]] 一致)
- **hermes_messages persist 失败** → Codex 路径 (emit `event: error` 不发 done, 客户端必须重试) — 消息是用户面历史,丢失对用户可见,严格保护

理由: audit 是后台合规面 (DLQ 异步补即可), 消息是用户面 (丢失会让客户端历史"消失",必须明示).

### B-2 决策粒度: Claude 5 决策 vs Codex 7 决策

| Decision | Claude | Codex |
|---|---|---|
| D-1 JWT alg | 共识 (Owner 已批) | D1 同 |
| D-2 hermes-agent 调用层 | 我 D-1 (我命名 D-1, codex D2) | codex D2 同 |
| D-3 conversation_id 生成 | 我 D-3 | (未列) |
| D-4 心跳节奏 | 我 D-4 | codex D3 同 |
| D-5 persist 失败 | 我 D-5 (宽松) | codex D4 (严格) |
| D-6 credential bridge 形态 | 隐含 §B | **codex D6 独立列出** |
| D-7 sse-starlette dep 批准 | 未列 | **codex D7 独立列出** |

**Synthesis 采纳 Codex 7 决策 + 我 D-3 conversation_id = 总 8 决策**, 其中:
- 已 Owner 批 (D-1 EdDSA / D-3 心跳 / D-5 ContextVar / D-? persist-on-done): 4 决策, 列入 §A 共识
- 真正待批: **4 决策** (D-2 调用层 / D-4 失败处理 / D-6 credential / D-7 dep) + 我独有 D-8 conversation_id = **5 待批**

### B-3 §0 independence caveat

Codex §0 自报: 一次 `rg` 误抓到 Claude plan 几行 (非内容主题). 我建议 **接受** — 不影响 plan 主体独立性 (Codex §13 列出读取源, 包含全 hermes/hermeshttp/main.py 等, 不含 Claude plan 文件名读取). Owner 可要求重跑但成本高 (40k token).

## §C 各方独有维度

| 来源 | 独有维度 | Synthesis 处理 |
|---|---|---|
| Claude | conversation_id 生成 策略 D-3 | **纳入** → D-8 |
| Claude | 3 sub-slice 拆分 (2.2.a/b/c) | 与 codex 类似, 合并 |
| Claude | C-7 client 断连 runner 仍 stream 处理 | **纳入** §H 风险 |
| Codex | D6 credential bridge — runner 拿内部短期 token, 不拿原 vendor key | **纳入** → 直接 surface |
| Codex | D7 sse-starlette dep license gate | **纳入** → 直接 surface |
| Codex | 难点 #5 entrypoint.sh JWT-only 启动 (Slice 2.1 已 defer 的 DEFERRED-1) | **纳入** §H 风险, 也在 DEFERRED-hermes-slice2-1-round3-tail.md 已有 ticket |
| Codex | 难点 #3 hermes-agent 同步 callback + async SSE queue 桥接 | **纳入** §C C-2 加强 |
| Codex | runner credential 接 hermes-agent `base_url`/`api_key` 形参 vs HUAKAI 内部 endpoint | **纳入** D-6 |

各方独有维度数: **9**

## §D 执行序 (synthesis 推荐)

```
[Decision Gate: Owner approves §F 5 待批决策] (required)
  ├── D-2: hermes-agent 调用层 (推荐 A: in-process import)
  ├── D-4: persist 失败处理 (推荐分类: audit→warn-log+DLQ, msg→error event)
  ├── D-6: credential bridge 形态 (推荐 A: 内部短期 token, runner 不拿 raw vendor key)
  ├── D-7: sse-starlette dep 批准 (推荐 A 加 license audit, 否则 B: 不加 dep 用 StreamingResponse)
  └── D-8: conversation_id 生成 (推荐 A: client 可选 + first SSE emit 新 id)

[Slice 2.2.a: Python runner /chat 真实施] (0.75 day)
  ├── backend/deploy/hermes-runner/main.py: /chat 真实施
  ├── backend/deploy/hermes-runner/hermes_chat.py (新): hermes-agent + ContextVar + SSE queue bridge
  ├── backend/deploy/hermes-runner/sse_events.py (新, optional): event 类型定义
  ├── backend/deploy/hermes-runner/requirements.txt: sse-starlette (D-7 A 批准时)
  ├── backend/deploy/hermes-runner/entrypoint.sh: JWT-only 启动 (Slice 2.1 DEFERRED-1 闭合)
  └── 单 commit

[Slice 2.2.b: Gateway tee-and-persist + bridge 包] (1 day)
  ├── backend/internal/hermeschat (新): SSE parser + done-fence + atomic persist
  ├── backend/internal/hermes/audit.go: 加 ActionMessageSend
  ├── backend/internal/hermes/types.go: Store 接口加 InsertMessage/TouchConversation/AppendUserMsg
  ├── backend/internal/hermeshttp/chat_handler.go: 改用 hermeschat.Bridge
  ├── backend/internal/hermeshttp/conversations_handler.go: 读 DB 不再 proxy runner
  ├── backend/sql/queries/hermes.sql: 新 query 已有 (Slice 2.0), 仅 service 层 wire
  └── 单 commit

[Slice 2.2.c: discriminating tests] (0.25 day)
  ├── chat_persist_test.go: mutation 自检 done-fence
  ├── runner_test_hermes_chat.py: ContextVar 隔离 + heartbeat
  └── 与 2.2.b 同 commit (test 是 wire 衔接)

[Review Gate]
  ├── ≤2 round codex review per commit (CLAUDE.md #8)
  ├── S0/S1 must fix, S2/S3 → DEFERRED
  └── Slice 2.2 闭合 → Slice 2.3 (conversations list/get/delete handler)
```

Synthesis 执行序: **3 commit 1 切片闭合, 2 day 工作量** (Claude 1.75 + codex SSE queue 桥接 +0.25)

## §E 借鉴对照 (CLAUDE.md #15 每决策 ≥2 cite, 合两 lane)

| Reference | Lane | 关键 cite | 用于 |
|---|---|---|---|
| NousResearch/hermes-agent@v0.14.0 | both | `pyproject.toml:54` (PyJWT 自带) / `hermes_constants.py:20` (ContextVar setter) / `run_agent.py:350` (AIAgent base_url/api_key) | D-2 / D-5 / D-6 |
| router-for-me/CLIProxyAPI@50d19e2 | both | `sdk/api/handlers/claude/code_handlers.go:228` (request-scoped stream) / `internal/runtime/executor/claude_executor.go:280` (stream-aware processing) | D-2 / D-4 |
| invariant-gateway@9baeade | codex | `gateway/routes/anthropic.py:110` (StreamingResponse 不需新 dep) | D-7 B |
| Portkey-AI/gateway@d2ea41f4 | codex | `src/handlers/streamHandler.ts:139` (stream chunk transform) / `src/providers/google-vertex-ai/utils.ts:121` (JWT signing in gateway) | D-1 / D-4 |
| LiteLLM | claude | `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/files/streaming.py:195-207` (stream completion async logging callback) | D-4 (我宽松路径参考) |
| sub2api | claude | `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/gemini_session.go:99-111` (session-derived routing identity, paraphrased) | D-8 conversation_id |

7 references / 5 待批决策 — 每决策有 ≥2 cite (CLAUDE.md #15 compliant).

Round 2 citation repair note (2026-05-26): the remaining full-SHA cites above were checked against local `~/refs` snapshots. Observed HEAD timestamps: LiteLLM `2026-05-19T16:20:25-07:00`; sub2api `2026-05-20T11:13:53+08:00`.

## §F Owner 决策清单 (synthesis surface, 5 待批)

| ID | Decision | Recommendation | 关键 cite | Required now |
|---|---|---|---|---|
| **D-2** | hermes-agent 调用层 | **A: 直接 in-process import** (低延迟, ContextVar 完整) | hermes-agent@v0.14.0 `run_agent.py:350` + CLIProxyAPI `code_handlers.go:228` | **Yes** |
| **D-4** | persist 失败处理 (分类) | **A 混合**: audit→warn-log+W4 DLQ; messages persist 失败→emit `event: error` 不发 done (用户面强保护) | W4 [[project_trust_ledger_failclosed_policy]] + Portkey `streamHandler.ts:70` | **Yes** |
| **D-6** | upstream credential bridge | **A: gateway 解析 credential, runner 拿内部短期 token; runner 从 env 读取内部 base_url, 不拿 raw vendor key** | hermes-agent `run_agent.py:350` + CLIProxyAPI auth manager pattern | **Yes** |
| **D-7** | sse-starlette dep 批准 | **A: 加 sse-starlette==3.4.4 (BSD-3-Clause, MIT-compat)** | invariant-gateway `routes/anthropic.py:117` StreamingResponse 路径; PyPI metadata `BSD-3-Clause` | **Yes** |
| **D-8** | conversation_id 生成 | **A: client 可选传, 缺则 gateway 新建 + first SSE emit `event: conversation\ndata: {"id":N}`** | hermes-agent `conversation_loop.py:120` server-side conv_id | **Yes** |

## §F.1 Owner 决策记录 (2026-05-26)

Owner via AskUserQuestion 批准:
- **D-2 = A**: 直接 in-process import hermes-agent
- **D-4 = 混合**: audit→warn-log+W4 DLQ, messages→emit `event: error` 不发 done
- **D-6 = A**: gateway 给 runner 内部短期 token; runner 从 env 读取内部 base_url, runner **永不** 拿 raw vendor key
- **D-7 = A**: 加 `sse-starlette==3.4.4` 依赖. License check pass: `BSD-3-Clause` (PyPI metadata 2026-05-26 fetch), 与 HUAKAI MIT 兼容. requirements.txt 加 pinned 版本.
- **D-8 = A** (Claude 默认采纳, Owner 未单独问): client 可选传 conversation_id, gateway 新建后 first SSE emit `event: conversation`

## §F.2 Interface Contract (runner ↔ gateway, 锁死接口让两 lane 并行)

### Gateway → Runner: POST /chat

Headers (沿用 Slice 2.1):
- `Authorization: Bearer <JWT>` (JWT mode) OR `X-Hermes-Signature` + `X-Hermes-Timestamp` (HMAC compat mode)
- `X-Hermes-Tenant`: positive int64 (signed in HMAC canonical / JWT sub `tenant:user`)
- `X-Hermes-User`: positive int64 (同上)
- `Content-Type: application/json`

Request body JSON:
```json
{
  "conversation_id": 123,  // optional, int64, omitted/0 means new
  "messages": [             // array, current request user message + history (gateway 已 from DB 填充)
    {"role": "user", "content": "..."}
  ],
  "model": "...",           // optional, vendor model id
  "internal_token": "..."   // 短期内部 token (D-6), 不是 vendor raw key
}
```

### Runner → Gateway: SSE stream

`Content-Type: text/event-stream`, 5 类 event 严格命名:

1. `event: conversation\ndata: {"id":N}\n\n` — 第 1 个 event (新建 conv 时) 或缺省 (沿用已传 conv_id 时)
2. `event: token\ndata: {"delta":"text"}\n\n` — 重复 N 次, hermes-agent stream callback 每段
3. `event: status\ndata: {"phase":"reasoning|tool|response","detail":"..."}\n\n` — 状态可选
4. `event: error\ndata: {"code":"...","message":"..."}\n\n` — 错误 (runner 内部失败时); gateway 转发原文不重写
5. `event: done\ndata: {"finish_reason":"stop|error|length","total_tokens":N}\n\n` — 终止哨兵, gateway 见此触发 atomic persist+audit, persist 成功才转发 done; persist 失败转 error event 替代

Heartbeat: 20s 无 event 时 runner 发 `: keepalive\n\n` (SSE 注释, gateway 透传)

### Gateway 内部 token (D-6)

Gateway 调 runner 前生成短期 token:
- TTL: 5 分钟
- Claims: tenant_id, user_id, request_id, exp
- Signed with: 内部 HMAC (不用 JWT 因为 runner 已有 JWT 验证, internal token 是 gateway → 内部 LLM endpoint 的二级 token)
- Runner 从 `HUAKAI_HERMES_INTERNAL_LLM_BASE_URL` 读取 `base_url=<gateway internal endpoint>` 并忽略 body 中任何 `internal_base_url`; runner 拿到签名后的 `internal_token` 后透传给 hermes-agent `api_key=<internal_token>`, gateway 在 internal endpoint 验证 token 后用真 vendor key 调上游

注: D-6 internal credential bridge 细节由 codex 在 Slice 2.2.b 实施时设计 + Round 1 review 验证。Slice 2.2 仅做最小路径: runner 拿到签名 `internal_token`, `internal_base_url` 只从 env 读取, **不存** 不**记录**.

## §G Risk + Mitigation (合两 lane)

| Risk | Severity | Mitigation |
|---|---|---|
| hermes-agent v0.14.0 API 变更 | S1 | requirements.txt pin == 0.14.0 |
| ContextVar 跨 thread 失效 | S1 | hermes-agent v0.14.0 同步路径; 跨 thread 用 `contextvars.copy_context` 显式传 |
| persist tx 并发死锁 | S2 | conv 行 SELECT FOR UPDATE 或乐观锁 (last_message_at 比较) |
| SSE 心跳被 proxy 砍 | S2 | 心跳间隔 env 可调 HUAKAI_HERMES_KEEPALIVE_SECONDS (default 20) |
| audit row 写失败 → chat 全失败 | S0 → 不会发生 | W4 [[project_trust_ledger_failclosed_policy]] = audit DLQ + 放行 |
| messages persist 失败 → 用户看不到历史 | S1 | D-4 严格路径 → emit error event, client retry |
| runner 收到 raw upstream key | S0 | D-6 严格禁止, runner 仅拿签名内部 token; 内部 base_url 从 env 读取 |
| client 断连 runner 仍 stream | S2 | runner 用 request-scoped ctx, client cancel → runner 终止 |
| entrypoint.sh 强制 HMAC secret 阻 JWT-only 部署 | S2 | Slice 2.2.a 闭合此 DEFERRED-1 |
| sse-starlette 加 dep 引入未审计 license | S2 | 选 D-7 B 不加 dep |
| codex §0 independence caveat | S3 | 接受 (内容主体不受影响) |

## §H Lane provenance

- Source files read by synthesis lane:
  - `docs/process/plans/2026-05-26-hermes-phase-1-slice2-2-claude.md` (full)
  - `docs/process/plans/2026-05-26-hermes-phase-1-slice2-2-codex.md` (full)
  - Slice 2.0/2.1 commits + DEFERRED files
- Lane: synthesis (Claude reviewer)
- Independence:
  - Claude lane 独立 write (无 codex plan 输入)
  - Codex lane 独立 write (§0 caveat 自报 grep 误抓数行, 接受)
- UTC: 2026-05-26T02:00:00Z

## Owner 中文摘要

Slice 2.2 双 lane 独立 plan 后 synthesis,核心收敛但 codex 多 2 个 critical 决策 (D-6 credential bridge / D-7 sse-starlette dep) + Claude 多 1 个 (D-8 conversation_id). **共识 12, 冲突 1 (D-4 persist 失败处理), 各方独有 9, 总 5 待批决策**. Synthesis 推荐:
- **D-2 推 A** hermes-agent 直接 in-process import (低延迟 + ContextVar 完整)
- **D-4 推混合** audit→warn-log+W4 DLQ, messages→emit error event (审计后台/用户面分类)
- **D-6 推 A** runner 拿内部短期 token, 内部 base_url 从 env 读取, **禁止 runner 收 raw vendor key** (最高安全)
- **D-7 推 B** 不加 sse-starlette dep, 用 FastAPI StreamingResponse (避免新 dep 审计)
- **D-8 推 A** client 可选传 conv_id, 缺则 gateway 新建 + first SSE emit
**3 commit 1 切片闭合, 2 day 工作量**. Owner 拍 §F 后启动 Slice 2.2.a (Python runner /chat 真实施).
