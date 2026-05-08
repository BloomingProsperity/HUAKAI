# 2026-05-08 纵向闭环 synthesis（claude × codex 双 lane + Owner 前端 directive）

| 字段 | 值 |
| ---- | ---- |
| Owner directives | (1) "横向扩展完成后立即进行纵向闭环" (2) "前端页面也要开始写起来了。你纵向一闭环就得接入前端进行测试" |
| Lane A | Claude planner — [docs/plans/2026-05-08-vertical-closure-claude.md](2026-05-08-vertical-closure-claude.md) |
| Lane B | Codex planner — [docs/plans/2026-05-08-vertical-closure-codex.md](2026-05-08-vertical-closure-codex.md) |
| Status | Synthesis 完成，Owner 一句话即可 launch |

## 1. 双 lane agree（一致选择）

| 决策项 | claude | codex | 一致结论 |
| ----- | ------ | ----- | -------- |
| 闭环候选 | A: Bedrock-on-Anthropic | A: Bedrock-on-Anthropic | **A** |
| 执行路径 | Z: 双轨 (Y mock 先 + X 真 AWS 后补) | Z: 双轨 | **Z** |
| in-scope | Bedrock 单链路 + Track B/C/D/P metrics | 同 | 一致 |
| out-of-scope | 不扩 OpenAI/Gemini，不动 schema/billing | 同 | 一致 |
| clean-room | 本 vertical 不读外部参考源码 | 同 | 一致 |
| 估时 | ~3 hours | ~1 工程日 + 0.5-1h 真 AWS 补 | **取 codex 估时（更保守且含 fix-buffer）** |

## 2. 双 lane gaps（互补，不冲突）

| Gap | claude 提到 | codex 提到 | synthesis 处理 |
| --- | --- | --- | --- |
| 验证矩阵粒度 | 10 项（routing/translate/cache/sigv4/eventstream/cachemetrics/sticky） | 18 项（含 client contract / tenant context / endpoint URL / canonical event / error mapping / secret redaction） | **取 codex 18 项**（更细，含安全 redaction） |
| Pre-execution checklist | 无 | 8 项 | **新增 codex checklist** |
| Decision points for Owner | 4 项 | 4 项 | **合并去重** |
| Failure path | 简提 | 详 (auth/permission/rate-limit/server failure) | **取 codex 详** |

## 3. Owner 新 directive（2026-05-08 second pass）补加项

Owner 强 directive："前端页面也要开始写起来了。你纵向一闭环就得接入前端进行测试。"

→ 双 lane 原计划仅在 backend 跑 mock-server / curl 风格 E2E。**纵向闭环现在必须穿透到前端 UI 层**，否则不算闭环。

### 前端 wedge 最小集（不写仓库内 Gemini 域 marketing UI，仅 vertical 工程必需）

| 前端组件 | 用途 | 复杂度 |
| ------- | ---- | ----- |
| ChatPage | 单页 Anthropic Messages 形 chat UI（输入 system+user prompt → 调 HUAKAI `/v1/messages` → 渲染 SSE 流 → 显示 cache 字段） | 小（200-300 LoC TSX） |
| ObservabilityPage | 读 `/debug/vars` cache_token_count 渲染 creation/read/request_count 全局 + per-account | 极小（80-150 LoC） |
| Layout 框架 | 极简 router 跳两 page | 50 LoC |

**stack**: React + Vite + TypeScript（per [frontend/README.md](../../frontend/README.md) 已锁定）。
不引 UI library（不需要 marketing 美感，operations dense view OK）。

### 完整闭环链路（含前端）

```
浏览器
  │ ChatPage 输入长 system prompt + user msg + 选 stream
  ▼ POST /v1/messages (Anthropic Messages API form)
HUAKAI gateway
  │ 路由 → bedrock_invoke → Track B sticky → Track C 注入 cache_control
  ▼ AWS SigV4 sign → POST bedrock-runtime/.../invoke-with-response-stream
mock-server (Y 轨) / 真 AWS Bedrock (X 轨)
  │ binary EventStream 响应
HUAKAI gateway
  │ EventStream decode → canonical event → SSE forwarder
  ▼ SSE stream
浏览器
  │ ChatPage 流式渲染 + 显示 cache_creation/cache_read 字段
  │ 切到 ObservabilityPage 看 cache_token_count 增量
  ▼ Owner 看 UI 即闭环成立
```

## 4. 最终执行顺序（synthesis）

1. **Y0 backend harness** （codex 步 1-2）: httptest mock Bedrock + 测试租户/account fixture
2. **Y1 backend happy path** (codex 步 3): Anthropic Messages 形 + 长 system prompt → mock 响应 → canonical SSE
3. **Y2 sticky hit + 短 prompt control + failure path** (codex 步 4-6)
4. **Y3 metrics + audit 验证** (codex 步 7)
5. **F0 前端 scaffold**: package.json + vite + tsconfig + 极简 router
6. **F1 ChatPage**: textarea(system) + textarea(user) + send button → fetch `/v1/messages` 流式 + 渲染
7. **F2 ObservabilityPage**: poll `/debug/vars` 显示 cache_token_count + per-account
8. **F3 前端→backend 联通**: 浏览器跑通 Y1-Y4 的请求形态，UI 渲染流 + cache fields 可见
9. **X 真 AWS smoke** (Owner 本机): UI 输入相同 prompt 形 + 真 AWS 凭据 → 看 ObservabilityPage cache_read_total > 0

## 5. 闭环验证矩阵（取 codex 18 项 + 前端 4 项）

codex 矩阵（client contract / tenant / routing / credentials / sticky upsert+hit / body translation / cache pos+neg / endpoint / signing / EventStream decode / canonical / error / global metrics / per-account metrics / audit / redaction）— 18 项。

新增前端 4 项:

| 层 | 必须验证项 | 通过标准 |
| --- | --- | --- |
| 前端发起 | ChatPage 调 `/v1/messages` | network tab 看 SSE 流，response 200 |
| SSE 渲染 | message_delta + 终止事件正确显示 | UI 看到 token 流 + finish reason + cache fields |
| Observability | ObservabilityPage 拉 `/debug/vars` | 表格显示 creation_total / read_total / request_count |
| 双请求闭环 | 同 system prompt 第二次发 | UI 显示 cache_read_input_tokens > 0 |

## 6. Scope / Non-Scope (final)

**In scope**:
- Bedrock-on-Anthropic 全栈 E2E（一条模型 + 一组 prompt 形态 + 一类 failure）
- 前端 ChatPage + ObservabilityPage + 极简 router (3 文件 + tsconfig + vite config + package.json)
- mock-server harness (httptest)
- Track B/C/D/P 指标确实可观测

**Out of scope**:
- 不扩 OpenAI / Gemini / Azure E2E
- 不写 admin UI（pools / provider-accounts / usage / billing / audit pages — 那是 Phase 7 Gemini 域）
- 不引前端 UI 库 (shadcn / mui / antd 等)
- 不动 schema / auth / billing / quota / deployment
- 不读外部 reference 源码（CPA / sub2api / one-api / new-api / portkey / ...）

## 7. 风险（claude + codex 合并去重）

| 风险 | 缓解 |
|-----|-----|
| Mock EventStream 与真 Bedrock 偏差 | Y 轨只覆盖 deterministic gate；X 轨独立通过条件 |
| 真 AWS 凭据/模型权限不可用 | Y 轨给 defect signal；X 标记 blocker |
| SigV4 测试过度耦合 | 只断言 AWS-required envelope，不断言 internal helper 字符串 |
| Metrics 异步 flaky | bounded polling，不固定 sleep |
| Sticky binding fixture 污染 | 每测试独立 tenant/session/model 或 transaction cleanup |
| Failure path 误打真 provider | failure 默认走 mock；X 只跑 happy path |
| Secret 泄露到 test log | fake credential 给 CI；真凭据从 env；日志扫 Authorization 字样 |
| Scope 膨胀到 provider compat suite | 本 vertical 选一条 model / 一组 prompt / 一类 failure |
| **前端 scope 膨胀到 admin UI** | 本 vertical 仅 ChatPage + ObservabilityPage 两页 |
| **CORS/CSRF 阻塞前后端联通** | 前端 dev server 走 vite proxy → backend，避免 CORS；prod 同源部署 |

## 8. Pre-execution Checklist（取 codex 8 项 + 新增 2 项）

- [ ] 确认 synthesized vertical closure plan 已由 Owner 批准
- [ ] 不读 Claude 独立 plan 直到进入 compare/reconcile（已发生）
- [ ] 测试只用 fake credentials；真 AWS 仅从 env 读取
- [ ] `/debug/vars` 在测试环境可访问
- [ ] sticky binding test store/DB cleanup 方案存在
- [ ] audit/log sink 可在测试中查询，secret redaction 可断言
- [ ] failure path 默认指向 mock server
- [ ] 任何 high-risk 文件变更停下请 Owner 确认
- [ ] **前端 dev server vite proxy 配置 → backend HUAKAI gateway 端口**
- [ ] **前端 SSE 解析仓库内自实现，不引 sse 第三方包**

## 9. Decision Points for Owner（合并去重）

1. **真 AWS smoke 谁跑**：Owner 本机手动 vs 临时给 agent 凭据
2. **真 AWS region + Anthropic Bedrock model id**
3. Y 轨发现 implementation defect 是否允许 Codex 直接小修
4. schema/quota/billing/auth 相关修改另行确认
5. **前端是否走 vite proxy 跳过 CORS（推荐）vs 给 backend 加 CORS header**
6. **前端 stack 是否锁定 React+Vite+TS（per frontend/README）or 切换到 Next.js**
7. **现有 GEMINI.md 域定义"frontend = Gemini 域"是否在本 vertical 暂时由 Claude 接手 minimum wedge**

## 10. 估时（合并）

| 阶段 | 估时 |
|-----|------|
| Y0-Y3 backend harness + 4 paths + metrics 验证 | 6-10 hours |
| F0-F3 前端 scaffold + 2 page + 联通 | 4-6 hours |
| X Owner 真 AWS smoke + sanitized trace | 0.5-1h Owner-side |
| Defect fix buffer | 2-6 hours |
| **合计** | **~2 工程日内完整 Y+F；X 取决 Owner** |

## 11. Clean-room boundary

本 vertical 不读外部参考源码（CPA / sub2api / one-api / new-api / portkey / litellm 等）。理由：HUAKAI 已实现链路自给自足，验证目标是"我们已写的 actually work"，不是从外参考行为。

允许读：HUAKAI internal docs/specs/code、AWS Bedrock 官方协议文档、Anthropic Messages API 官方文档、React/Vite 官方文档。

禁读：Claude 同名 plan（已生效，本 synthesis 阶段是 reconcile，不是 compare 阶段）、外部 reference 源码、外部 reference README 中的 code blocks。

如执行中确实需对照外参考行为（unlikely），按 [docs/05_CLEAN_ROOM_POLICY.md](../05_CLEAN_ROOM_POLICY.md) 与 Owner 2026-05-08 强化 (必读源码 + 不抄) 另开 specifier/reviewer 双 lane。

## 12. Final output shape

执行完成后输出：
- Y 轨 mock E2E：PASS/FAIL + 失败层级 + 复现命令
- F 轨前端 E2E：PASS/FAIL + screenshot + network log
- X 轨真 AWS smoke：PASS/FAIL/SKIPPED_BY_OWNER_CREDENTIAL + sanitized request_id
- 22 项验证矩阵逐项状态
- 发现的实现缺陷 + 建议 owner
- 是否阻塞下一 slice

---

**待 Owner 1-2 句话拍板**:
1. 同意 A+Z+前端 wedge → 立即开 Y0 + F0 (mock harness + 前端 scaffold) **并行**
2. 真 AWS region/model：us-east-1 + anthropic.claude-3-5-sonnet-20241022-v2:0 OK 还是别的
3. 前端 stack 锁 React+Vite+TS 还是改

无 Owner 反对则我按本 synthesis 顺序立即开做。
