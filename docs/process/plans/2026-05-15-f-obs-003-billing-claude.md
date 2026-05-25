# 2026-05-15 F-OBS-003 4-state failed-stream billing (Claude 独立 plan)

| Lane | SPECIFIER (Claude); 平行于 codex 同主题 plan (CLAUDE.md #10) |
| Source | docs/03_FEATURE_PARITY_MATRIX.md F-OBS-003 row + memory project_core_trust_chain_differentiator |
| Method | Specifier 读 HUAKAI backend/, NOT 上游 reference 源码; 仅描述实施 — 不写代码 |
| Agent | Claude Opus 4.7 (1M context) |
| UTC | 2026-05-15T13:45:00Z |

## scope

流式请求 mid-stream 失败时需要 4 态分类 + Tx2 ledger 每态独立记账:
- **Acquired** — Route plan 选了账号但还没发请求 → 不计费,记 lease lifecycle
- **In-Flight** — 上游已建连/已收到部分 token 但未达 done → 待裁决
- **Partial-Delivered** — 上游 RST/timeout 中断,但 SSE 已发部分 token 给客户端 → **按已发 token 计费**
- **Failed** — 完全未发任何 token → 不计费 + 账号 health probe -1

Tx2 entry 必须能区分 4 态,并支持后续 reconciliation。

## file-by-file impact

需先 grep 确认实际路径:
- `backend/internal/billing/ledger.go` — Tx2 entry struct 加 `StreamState enum` 字段
- `backend/internal/proto/anthropic_sse.go:26x hot path` / `openai_sse.go:26x hot path` / `gemini_sse.go:15x hot path` — 流转换器在 chunks 计数 + 失败转 state
- `backend/internal/attempt/state.go` — Attempt struct 加 stream_state + delivered_token_count
- `backend/internal/db/migrations/` — 新迁移加 stream_state 列 + delivered_token_count 列
- `backend/internal/observability/metrics.go` — Prometheus 加 4 个 counter: `huakai_stream_state_{acquired,inflight,partial,failed}_total{vendor,model}`

## data model

```sql
ALTER TABLE billing_ledger
  ADD COLUMN stream_state SMALLINT NOT NULL DEFAULT 0,  -- 0=Acquired/1=InFlight/2=Partial/3=Failed
  ADD COLUMN delivered_token_count BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN stream_terminated_reason VARCHAR(64);  -- 上游 reset / 超时 / 客户端 cancel
```

migration 需 Owner 确认 (DB schema change = HIGH 决策点)。

## 4-state diagram (ASCII)

```
[Route] → Acquired ─┬─ 上游连接失败 ─→ Failed (not charged)
                    │
                    └─ 上游 200 OK ─→ InFlight
                                       │
                          ┌────────────┼────────────┐
                          ↓            ↓            ↓
                       done event   RST/timeout   client cancel
                          ↓            ↓            ↓
                       (full)      Partial      Partial
                          ↓     (charge sent)  (charge sent)
                       charge
```

## test plan

- unit: `state_test.go` 4 状态转换矩阵 (Acquired → InFlight → Partial / Failed / done)
- unit: 流转换器 `proto/anthropic_sse_test.go` + `openai_sse_test.go` 各加 1 case "RST mid-stream produces Partial state with delivered_token_count > 0"
- integration: `integration/billing_state_test.go` 用 mock upstream 在不同点切断流,断言 ledger entry stream_state 正确
- E2E: 通过 reverse proxy 跑真 anthropic mock,客户端连 SSE 然后 abort,验证 Partial state + token count

## time estimate

5-7 天 codex 实施 + 2 天 Claude review + 1 天迁移演练 = 8-10 天

## blast radius

数据层 + 计费层 — **HIGH**。migration 要在生产前两遍 dry-run; 4-state enum 一旦上线不能再退 enum 值。

## decision points (Owner sign-off triggers)

(D1) migration 时机 — 是否 R-E mainline 切换前;还是与 Phase 4.5 同 wave  
(D2) Partial state 是否要给客户端可见的 hint(F-TRUST 信任链卖点需要)  
(D3) Client cancel 算 Partial 还是单独第 5 态  
(D4) stream_terminated_reason 字符串是否枚举化  
(D5) ledger entry 上线后能否回填历史数据 (无法回填 → 接受历史 NULL)

## clean-room

灵感(声誉级):
- sub2api 据闻有 partial usage 概念但实现细节不读
- new-api 据闻不区分 partial
- LiteLLM 据闻按 token count 计费但无 4-state enum

HUAKAI 升级点 (生态升级 + 算法升级):
- 4-state enum 显式落 schema → ops 可见 → 区别 sub2api 的隐式状态
- delivered_token_count + terminated_reason 双字段 → 反掺水卖点(F-TRUST family)

## sources read

- docs/03_FEATURE_PARITY_MATRIX.md F-OBS-003 row
- memory project_core_trust_chain_differentiator
- memory project_sub2api_scaling_bottleneck
- backend/internal/proto/{anthropic,openai,gemini}_sse.go hot path 标记
- (未读) sub2api/new-api/portkey/helicone/litellm 源码 — 仅声誉级引用
