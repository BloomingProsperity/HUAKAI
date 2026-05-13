# HUAKAI 项目测试方案起草 — Codex Lane

你是 HUAKAI 测试策略起草人（codex）。

## 任务

读取整个 HUAKAI 项目（backend / docs / frontend 关键路径），起草一份完整的**测试方案文档**，输出到：

```
docs/plans/2026-05-12-test-strategy-codex.md
```

## 项目当前状态

- **Phase 0 / 0c / 1（in-progress）**：HCSF v0.4 协议层 + 14 capability families + INV-1..46 validator + 35 fixture
- **PASR**（Provider Account Selector with Cache-aware Routing）D1–D5
- **SSE adapters**：anthropic / openai / gemini / bedrock
- **Buffered / streaming / replay** 三态 stream plan
- **Admin endpoints** M1..M5
- **单体 backend**：`backend/cmd/huakai`
- **Next.js 14 frontend**：admin dashboard P1（已 4 轮 Gemini design）

## 你要覆盖的测试维度

1. **Unit（per file）**
   - `backend/internal/proto/capability_*.go` / `envelope_*.go` / 各 SSE adapter
   - `backend/internal/pool/*`（PASR claim/release/cache-aware ranking）
   - `backend/internal/cache*`（segment table / bitmap / metrics）
   - 当前 envelope_test.go 2094 LoC，54 个 TestINV — 分析覆盖哪些 INV，缺哪些
2. **Integration（multi-package）**
   - PASR claim/release + slot 计数 + cache metric 一致性
   - upstream adapter 全链路（client request → canonical → upstream → canonical → client response）
3. **Fixture-based contract**
   - HCSF fixtures（35 个）现状 + 缺哪些 negative case + 缺哪些 vendor variant
4. **End-to-end（mock upstream）**
   - gateway 起来 + 客户端调 `/v1/chat/completions` 或 `/v1/messages`
   - 校验 SSE → buffered → replay 三态
5. **Real-upstream smoke**
   - **限定 4 vendor 真账号**：anthropic / openai / gemini / codex（参考 memory `project_real_vendor_account_scope.md`）
   - 其余 vendor 全 mock
6. **Load / stability**
   - PASR shadow vs canary、cache hit ratio 收敛、并发 fanout
7. **Chaos / fault**
   - upstream 限流 / 5xx / timeout / connection drop / partial SSE 截断 / mid-stream fallback
8. **Security**
   - API Key 签发链 / quota 越权 / data retention enforcement / 注入测试
9. **Frontend**
   - dashboard mock 模式 + 真后端 wired 模式 / type-check / build / a11y / hydration / SSR
10. **CI/CD**
    - pre-commit codex review + go test + `-tags debug` + lint

## 每个维度输出

- **现状**：现有 test 数量 / 覆盖率粗估 / 已识别空白
- **优先级**：P0 必做 / P1 短期 / P2 中长期
- **工作量估计**：engineer-day
- **依赖**：需要 fixture 补 / 需要 mock server / 需要真账号 / 需要 CI infra
- **风险**

## 不要做

- 不实际写测试代码（只写策略文档）
- 不读非 MIT reference project 源码（sub2api / new-api / portkey / helicone / litellm / all-api-hub / envoy-ai-gateway —— **HUAKAI 内部 only**）
- 不动 production code / config / LICENSE
- 不假设 AWS 凭据（Owner 没 AWS access，参考 memory `project_no_aws_credentials.md`）

## 工程量约束

- 文档总长 ≤ 1200 行（避免 token 爆）
- 逐维度紧凑
- 末尾给一个 **"P0 启动建议"** — Owner 立马能开做的 3-5 件事

中文写。直接做。
