# Hermes phase-1 Slice 2.2 — Claude lane plan

- UTC: 2026-05-26T00:00:00Z
- Lane: Claude (与 codex lane 独立起，不互相参考)
- 前置已闭合: Slice 2.0 (785ee02 schema) / Slice 2.1 (4b02edf JWT + bootstrap)
- 切片性质: 中难度 (SSE 桥接 + atomic persist+audit, 非 auth/financial 高难度)

## §A 切片目标

让 `/v1/hermes/chat` 链路从 "stub-透传" 升级为 "真接 hermes-agent + tee-and-persist"。具体：

1. **Python runner**: `/chat` 端点真实施，import hermes-agent v0.14.0，contextvars 注入 per-request HERMES_HOME，SSE stream 输出 + 20s 心跳 + 显式 `event: done`。
2. **Gateway tee-and-persist**: 接 runner 的 SSE stream，一边透传给 client (现有 copyProxyResponse 风格)，一边解析 `event: done` 时**atomic** persist `hermes_messages` + `hermes_conversations.updated_at/last_message_at` + audit `hermes.message.send`。
3. **新增 audit action**: `hermes.message.send` 加进 `audit.go` validAction 白名单 (Slice 2.0 0058 migration 已 ALTER CHECK 加这个值, audit.go 注册即可)。
4. **不动**: schema (已落)、JWT (已落)、frozen 包 (gatewayhttp/gateway/proto)。

## §B 边界 (不做什么)

- ❌ 不做 conversations list/get/delete handler (Slice 2.3)
- ❌ 不做 messages list (Slice 2.3)
- ❌ 不做 cursor protobuf vendor (后续)
- ❌ 不动 frozen 包 `backend/internal/{gatewayhttp,gateway,proto}` — 只**读**其 SSE 工具 EmitSSEDone 等 (已存在 `internal/proto/client_adapter_common.go:204-220`)
- ❌ 不做 HMAC fallback 清理 (Slice 2.5)
- ❌ 不做 hermes-agent fork / patch — 仅 import + contextvars overlay
- ❌ 不做真实上游 LLM key 注入路径变更 — hermes-runner 走 HUAKAI gateway 现有 LLM credential，runner 不存 key (维持现有 Slice 1 模式)

## §C 难点清单 (前期写全, [[feedback_small_closed_increments]])

| # | 难点 | 风险 | 缓解 |
|---|---|---|---|
| C-1 | SSE stream tee 同时**透传** + **解析 event:done** 不能两遍读 body | 错过 done → 永不 persist；过早 close → client 截断 | bufio.Scanner 读 line-by-line, 见 `event: done` 时**异步** persist (不阻塞 stream),done 行仍写 client |
| C-2 | hermes-agent v0.14.0 API 表面 (stream method / event 类型) 未确认 | 接错 API → runner 启动崩 | 先 codex /shell exec 进 hermes-agent venv `python -c "import hermes; print(hermes.__all__)"` 探测 |
| C-3 | persist atomic — content + conversation + audit 必须同 tx | 任一失败 → 中间态 (留半条消息或 audit 缺) | 复用 Slice 1 `withTx` 模式 (audit.go recordAuditWithStore + persist 同 tx) |
| C-4 | contextvars 注 HERMES_HOME 在 FastAPI middleware 与 hermes-agent thread 是否一致 | thread 间 contextvars 失效 → 跨 tenant data 串 | hermes-agent v0.14.0 同步路径足够;若 sync→async 边界,加 contextvars.copy_context 显式传 |
| C-5 | 20s 心跳格式与 client 兼容 (`: keepalive\n\n` vs 标准 `event: ping\ndata: ...`) | 错误格式 client 断 | 用 SSE 注释行 `: keepalive\n\n` (RFC 6202 ignore by spec),不发 event 名 |
| C-6 | conversation_id 生成 / 沿用 | 无 conv_id → 每 chat 创建新 conv (Owner 是否期望?) | request body 可选 `conversation_id`;缺则新建 row,返回 id 给 client |
| C-7 | gateway 写 tx 在 stream 末尾,若 client 断连 runner 仍在 stream → 是否 persist? | 数据完整性 trade-off | done 事件来了一律 persist (即使 client 断,运营和审计仍需要);未 done 不 persist (避免落不完整) |
| C-8 | Python runner 启动 fail-closed 检查 hermes-agent 可用性 | hermes-agent 缺 → runner 起来后 /chat 才 500 | startup 探测 `import hermes`, 失败直接 exit 1 |

## §D 决策点 — Owner surface (CLAUDE.md #15 每点 ≥2 参考 cite)

### D-1: hermes-agent 调用层 — 直接 import vs subprocess 隔离

| 选项 | 描述 | 参考项目对照 |
|---|---|---|
| **A (推荐)** 直接 `import hermes` in main.py | 同进程同 thread, contextvars 完整, 最简 | `~/refs-latest/hermes-agent-main/conversation_loop.py:80` 自身就是 import 模式;`~/refs/CLIProxyAPI-latest/sdk/auth/codex_device.go:24-33` (类比 subprocess vs in-proc trade-off, 选 in-proc 更简) |
| **B** subprocess + JSON RPC 隔离 | 崩溃隔离, 但开销大 + contextvars 失效 | LiteLLM `proxy/proxy_cli.py:200` 走 worker subprocess (但 LiteLLM 是多 worker);`one-api/middleware/distributor.go:30` 同进程 |

推荐 A — hermes-agent v0.14.0 单租户线程模型够用，contextvars 完整。如果未来扩并发再切 B。

### D-2: tee-and-persist 时机 — 同步 done 阻塞 vs 异步 goroutine

| 选项 | 描述 | 参考项目对照 |
|---|---|---|
| **A (推荐)** 异步 goroutine: done 行写 client + 启动 goroutine 做 tx persist+audit | client 不等 DB, 体验好 | LiteLLM `streaming.py:218` async persistence;invariant-gateway `routes/anthropic.py:35` post-stream callback |
| **B** 同步: done 行**之后**才写 client, persist 失败 → client 收 error event | 数据完整性强保证, 但 DB 慢时 client 卡 | one-api `middleware/distributor.go:60` 同步 audit;portkey `src/middlewares/logger.ts:42` 同步 |

推荐 A — chat 体验优先；persist 失败异步 retry/DLQ (后续切片), 当前切片 done event 已发 client, audit 错误 log warn 不阻塞主流。

### D-3: conversation_id 生成策略

| 选项 | 描述 | 参考项目对照 |
|---|---|---|
| **A (推荐)** Client 可选传 `conversation_id`, 缺则 gateway 在 first message 时 INSERT 并在 SSE 第一行 emit `event: conversation\ndata: {"id":N}` | 客户端可控制连续会话;首条响应即得 id | hermes-agent `conversation_loop.py:120` 自管 conv_id;LiteLLM `proxy/spend_tracking.py:80` server-side conversation_id |
| **B** 强制 client 先创建 conversation 再 chat (两步走) | API 清晰, 但多一次 round-trip | sub2api channel `monitor.go` (类比 step-wise) |

推荐 A — 一步 chat + 隐式 conv 创建对客户端最自然，conv_id 仍可被前端用于后续操作。

### D-4: SSE 心跳节奏 — 20s 固定 vs 自适应

| 选项 | 描述 | 参考项目对照 |
|---|---|---|
| **A (推荐)** 20s 固定 `: keepalive\n\n` 注释行 | 已 Owner D5 batch 批 | LiteLLM `streaming.py:218`;invariant `streaming.py:50` 30s 间隔 |
| **B** 自适应 (低流量时短间隔) | 复杂, 收益不大 | (无对应 ref 项目, B 暂不推) |

推荐 A — Owner 已批的简单方案。

### D-5: persist 失败处理 — fail-stream vs warn-log

| 选项 | 描述 | 参考项目对照 |
|---|---|---|
| **A (推荐)** persist 失败 warn-log + 不写 client (client 已收完 done) + 落 hermes_audit failure row | trade-off: chat 体验 vs 历史完整;选体验 | sub2api `monitor/latency.go:30` warn-only;LiteLLM `slack_alerting.py` warn |
| **B** persist 失败回滚 + emit `event: error\ndata: {"persist_failed":true}` 给 client | 数据强保证, 但客户端没法回滚已渲染内容 | invariant `routes/anthropic.py:80` strict mode |

推荐 A — 数据丢失 risk 由 [[project_trust_ledger_failclosed_policy]] 已决策的 durable-DLQ 接住 (W4 已开,Slice 2.2 不重复造);当前 commit 落 warn-log 即可。

## §E 实施步骤 (sub-slice 拆分, 每子切片 no-S0/S1 闭合)

### Sub-slice 2.2.a: Python runner /chat 真实施 (0.5 day)

- 文件: `backend/deploy/hermes-runner/main.py` + 新 `backend/deploy/hermes-runner/hermes_chat.py` (调用 hermes-agent + SSE) + `backend/deploy/hermes-runner/test_hermes_chat.py`
- 完成: runner 启动后 `POST /chat` 返 SSE stream `event: token`/`event: done`,带 20s 心跳;contextvars 注 HERMES_HOME=`tenants/{tid}/users/{uid}/...`
- 测试: 1) /chat 不带 auth → 401 2) 带 JWT → 200 + SSE chunk 3) contextvars 不同 tenant/user 路径不同 4) 心跳 20s emit `: keepalive`
- 验证: `python3 -m unittest discover backend/deploy/hermes-runner`
- Commit: 单 commit `hermes-runner /chat 接 hermes-agent + SSE 心跳`

### Sub-slice 2.2.b: Gateway tee-and-persist (1 day)

- 文件: `backend/internal/hermeshttp/chat_handler.go` 改 (现有 startChat 升级为 tee mode), 新 `backend/internal/hermeshttp/chat_persist.go` (parser + persist tx), 加 sqlc query `InsertMessage` / `TouchConversation` 到 `backend/sql/queries/hermes.sql`
- 完成: gateway 接 runner SSE → 透传 client + 解析 `event: done` → async goroutine persist `hermes_messages` + touch `hermes_conversations.last_message_at` + audit `hermes.message.send` (atomic tx)
- 测试: 1) done event triggers persist 2) persist 失败 warn-log 不阻塞 client 3) audit row written with sanitized args 4) cross-tenant 不串 5) audit + persist 同 tx (mutation: 注 erra 让 audit 失败,persist 应回滚)
- 验证: `go test ./internal/hermeshttp/... ./internal/hermes/... -race -count=1`
- Commit: 单 commit `hermes-chat tee-and-persist + hermes.message.send audit`

### Sub-slice 2.2.c: audit action 注册 + conversation_id endpoint (0.25 day)

- 文件: `backend/internal/hermes/audit.go` 加 `ActionMessageSend = "hermes.message.send"` + validAction 白名单;`chat_handler.go` 加 conversation_id 处理 (request body 可选, 缺则新建)
- 测试: 1) ActionMessageSend 有效 (mutation: 删 case 测试应红) 2) conversation_id 缺 → 新 INSERT 3) conversation_id 传 + tenant 不匹配 → 404
- Commit: 与 2.2.b 同 commit (audit action 是 wire 衔接)

## §F 测试策略

每 sub-slice 测试遵循 CLAUDE.md #14 mutation 自检：

- **2.2.a**: hermes_chat.py 删除 `: keepalive` emit → 心跳测试红;contextvars middleware 注 HERMES_HOME 字符串错 → 路径测试红
- **2.2.b**: chat_persist 解析跳过 `event: done` → persist 测试红;persist tx 不裹 audit → audit 测试红;cross-tenant 用错 tenant_id → isolation 测试红
- **2.2.c**: audit.go validAction 不加 message.send → audit invalid action 测试红

## §G 验证清单

```
cd backend && export GOCACHE=/tmp/huakai-gocache
go build ./...
go vet ./...
go test ./internal/hermeshttp/... ./internal/hermes/... -count=1 -race
cd deploy/hermes-runner && python3 -m unittest discover -p 'test_*.py'
psql -h localhost -U huakai -d huakai_dev -c "SELECT * FROM hermes_conversations LIMIT 0" # schema sanity
git diff --check
```

## §H 风险与回滚

| Risk | Severity | Mitigation | Rollback |
|---|---|---|---|
| hermes-agent v0.14.0 API 变化 | S1 | pin version 0.14.0 in requirements.txt | revert main.py /chat 回 501 |
| contextvars 在 hermes-agent thread 失效 | S1 | hermes-agent 同步路径不跨 thread; 若发现跨 thread 用 contextvars.copy_context 显式传 | 加 lock + 全局 dict 降级 (1 release) |
| persist tx 死锁 (并发 chat 同 conv) | S2 | conv 行加 SELECT FOR UPDATE 序列化 OR 用乐观锁 (last_message_at 比较) | 加重试 backoff |
| SSE 心跳被 client/proxy 砍 | S2 | 心跳间隔可 env 调整 (HUAKAI_HERMES_KEEPALIVE_SECONDS, default 20) | 临时调短 |
| audit row 写失败导致 chat 全失败 | S0 | Owner W4 已决策 durable-DLQ fallback (project_trust_ledger_failclosed_policy);Slice 2.2 不重做, warn-log 即可 | warn-log 已是兜底 |

## §I clean-room 检查

- **读源**: `~/refs-latest/hermes-agent-main/` (MIT, 已确认) — 仅用于 D-1 决策 + C-2 难点缓解的 API 探测；引用形式: `NousResearch/hermes-agent@<sha>:<file>:<line>` (Slice 2.0 plan 已确认 hermes-agent 是 MIT)
- **不读**: 任何 non-MIT 上游 (sub2api / new-api / portkey / litellm 等) — Slice 2.2 不需要它们的 chat handler 源
- **lane 声明**: Claude lane = specifier (读 hermes-agent + 自写 plan), codex lane 独立起 plan, synthesis 由 Owner 决策 §D 后合并

## §J 估时 + blast radius

- 总估时: 1.75 day (2.2.a 0.5 + 2.2.b 1.0 + 2.2.c 0.25)
- Blast radius:
  - +1 新文件 `backend/deploy/hermes-runner/hermes_chat.py`
  - +1 新文件 `backend/internal/hermeshttp/chat_persist.go`
  - 修改 `backend/deploy/hermes-runner/main.py` (Slice 2.1 后)
  - 修改 `backend/internal/hermeshttp/chat_handler.go` (Slice 1 后)
  - 修改 `backend/internal/hermes/audit.go` (Slice 1 后, 加 1 action)
  - 修改 `backend/sql/queries/hermes.sql` (加 InsertMessage + TouchConversation 2 query)
  - sqlc 重生成 `backend/internal/db/hermes/hermes.sql.go`
- 包预算: hermes 现 18 files / ~3000 LoC, 加 2 files 后 ~3300 LoC, 仍远低于 5000 LoC 包预算 (AGENTS.md § Package Discipline)
- frozen 包改动: **0** (validated)

## §K Owner 决策清单 (surface)

| ID | Decision | Recommendation | Required now? |
|---|---|---|---|
| D-1 | hermes-agent 调用层 | A: 直接 import (in-proc + contextvars 完整) | **Yes** |
| D-2 | persist 时机 | A: 异步 goroutine (client 不等 DB) | **Yes** |
| D-3 | conversation_id 生成 | A: 可选传 + first SSE event emit 新 id | **Yes** |
| D-4 | 心跳节奏 | A: 20s 固定 (Owner 已批) | confirm |
| D-5 | persist 失败处理 | A: warn-log + durable-DLQ 兜底 | **Yes** |

Owner 拍 5 决策后，按 §E 三 sub-slice 顺序实施。每 sub-slice 按 CLAUDE.md #8 ≤2 round + S0/S1 gate 处理 review。
