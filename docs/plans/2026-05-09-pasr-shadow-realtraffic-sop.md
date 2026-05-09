---
title: PASR-lite Shadow 实战操作 SOP
date: 2026-05-09
audience: Owner (本机实操指南)
lane: writer SOP
related_commits:
  - M1: a5dd8a6 (config typed + ENV parse)
  - M2: 27eda39 (pasr_dispatch expvar metrics)
  - M3: f1d4e39 (PASRSelector slot parity)
  - M4: 3d1a05c (SelectorDispatcher 5 mode)
  - M5: f2e5221 (request-scoped AccountRing)
  - M6: 6ba2010 (main.go 装配)
  - M7: 1d46124 (集成测试)
  - M5b: 1655bd6 (SegmentTable tenant_id)
  - M5c: 5b4ead2 (segmentKey mode tag)
  - D: 42b847b (dispatcher vendor metric)
  - D2: 479d7d0 (vendor metric 到 chat handler)
owner_directives:
  - project_real_vendor_account_scope: 4 vendor (anthropic/openai/gemini/codex)
  - project_no_aws_credentials: Bedrock mock 信号不可信, 绕过 decision criteria
status: READY-FOR-EXECUTION
---

# PASR-lite Shadow 实战操作 SOP (2026-05-09)

本文是 Owner 本机实操指南，引导逐阶段验证 PASR-lite shadow 学习→canary 转折→pasr-primary 的端到端流程。不是开发计划，而是可机械执行的 runbook。

---

## 1. 元信息与前置条件

### 1.1 关联主线提交链

- **M1-M7**: PASR-lite main-wire 核心 7 atom (config/metrics/slot/dispatcher/ring/main wiring/集成测试)
- **M5b/M5c**: tenant_id 与 mode tag 补丁 (跨租户隔离)
- **D/D2**: vendor 切片 metric 接入 dispatcher + chat handler

### 1.2 前置条件 (Owner 本机)

执行本 SOP 前必须满足:

```bash
# 条件 1: 代码已拉最新主线
cd /path/to/HUAKAI
git pull origin claude/phase-1
git log --oneline | head -5
# 应看到最新 commit: 479d7d0 pool+gatewayhttp: PASR-lite D2

# 条件 2: Go 编译通过, 无编译错误
cd backend
go build ./...
# 应 clean exit, 无任何 error 或 warning

# 条件 3: 单元测试全绿
go test -v ./internal/pool/... ./internal/config/... ./cmd/gateway/... 2>&1 | tail -20
# 应看到 "ok" 与 pass count

# 条件 4: PostgreSQL 启动 + migration 全应用
psql -h localhost -U postgres -d huakai -c "SELECT COUNT(*) FROM provider_accounts;" | tail -2
# 应返回至少 4 行 (4 个 vendor account)

# 条件 5: 至少 1 个 admin api_key 已签发
psql -h localhost -U postgres -d huakai -c "SELECT api_key, account_id FROM admin_api_keys LIMIT 1;"
# 应返回 1 行, api_key 非空

# 条件 6: 4 vendor account 在 provider_accounts 表登记
psql -h localhost -U postgres -d huakai -c \
  "SELECT DISTINCT provider FROM provider_accounts;"
# 应返回: anthropic, openai, gemini, codex (或类似 4 个不重复的)
```

### 1.3 环境变量基线

启动前设置基线 ENV (Stage 0 等价验证):

```bash
export HUAKAI_DATABASE_URL="postgres://postgres:password@localhost:5432/huakai"
export HUAKAI_ADDR=":8080"
export HUAKAI_LOG_LEVEL="info"
export HUAKAI_POOL_SELECTOR_MODE="default"
# 其他模式 ENV 暂不设, 用默认值
```

---

## 2. Stage 0: Default 等价验证 (~1 小时)

**目标**: 验证零回归 — default 模式与之前表现一致

### 2.1 启动网关

```bash
cd /path/to/HUAKAI/backend
HUAKAI_DATABASE_URL="postgres://postgres:password@localhost:5432/huakai" \
HUAKAI_ADDR=":8080" \
HUAKAI_LOG_LEVEL="info" \
HUAKAI_POOL_SELECTOR_MODE="default" \
  go run ./cmd/gateway/main.go
# 应看到: "[INFO] gateway listening on :8080"
# 无任何 panic 或 error 日志
```

### 2.2 真实流量测试 (4 vendor × 10 请求)

在另一个终端，用真实账号凭据测试:

```bash
# 准备 4 个 curl payload (分别用 anthropic/openai/gemini/codex 账号)
# 各发 10 次真实 chat completion 请求

# 示例 (anthropic, 需替换实际 api_key):
for i in {1..10}; do
  curl -X POST http://localhost:8080/v1/chat/completions \
    -H "Authorization: Bearer $ANTHROPIC_API_KEY" \
    -H "Content-Type: application/json" \
    -d '{
      "model": "claude-3-5-sonnet-20241022",
      "messages": [{"role": "user", "content": "say hello"}],
      "max_tokens": 10
    }' 2>/dev/null | jq -r .choices[0].message.content
  sleep 0.5
done

# 同理对 openai/gemini/codex 各 10 次
```

### 2.3 验证指标

网关启动后，访问 metric 端口:

```bash
# 查看 pasr_dispatch 模式分布
curl -s http://localhost:8080/debug/vars 2>/dev/null | jq '.pasr_dispatch' | head -30

# 应看到:
# {
#   "mode_default_total": 40,     ← 40 个请求全在 default
#   "mode_shadow_total": 0,
#   "mode_canary_total": 0,
#   "mode_pasr_primary_total": 0,
#   "mode_pasr_strict_total": 0,
#   ...
# }

# 查看 cache_token_count_by_account (应按 vendor 有数据)
curl -s http://localhost:8080/debug/vars 2>/dev/null | jq '.cache_token_count_by_account' | head -50
# 应看到 4 个不同的 account_id 对应的 creation_total / read_total / request_count
```

### 2.4 决策条件

| 指标 | 预期 | 达成 ✅ / 失败 ❌ |
| --- | --- | --- |
| `mode_default_total` ≥ 40 | 全部请求走 default | ✅ |
| 其他 mode_<x>_total 全为 0 | 无 shadow/canary/pasr 流量 | ✅ |
| 无 panic/error 日志 | 网关稳定 | ✅ |
| 4 vendor 各至少 1 次成功响应 | 账号有效 | ✅ |

**进阶条件**: 全部✅ → 进入 Stage 1; 任何❌ → ABORT 查日志 + 联系开发

---

## 3. Stage 1: Shadow 5% (~1 天)

**目标**: 验证 shadow 采样机制 + segment table 不污染 + 比对信号质量

### 3.1 ENV 切换

```bash
export HUAKAI_POOL_SELECTOR_MODE="shadow"
export HUAKAI_POOL_SELECTOR_SHADOW_PCT="5"
# 重启网关
```

### 3.2 流量累积

跑脚本持续生成真实请求，目标 ≥ 200 个跨 4 vendor:

```bash
# 简单脚本 (伪代码, 需适配真实账号)
for vendor in anthropic openai gemini codex; do
  for i in {1..50}; do
    # 用该 vendor 账号发一次 chat completion
    # (循环脚本需自行实现, 这里仅展示步骤)
  done
done
```

可用现有压测工具 (如 Apache Bench / wrk / 自定义脚本) 并发发送。

### 3.3 关键指标检查

24 小时后或流量≥200 后，读 metric:

```bash
curl -s http://localhost:8080/debug/vars 2>/dev/null | jq '.pasr_dispatch'

# 关键行:
# {
#   "mode_default_total": 190,       ← ~95% 流量
#   "mode_shadow_total": 10,         ← ~5% 流量 (目标采样率)
#   "shadow_sampled_total": 10,      ← shadow 成功入队 10 次
#   "shadow_match_total": 8,         ← shadow 选择与 default 一致 8 次
#   "shadow_diff_total": 2,          ← shadow 选择与 default 不同 2 次
#   "shadow_drop_total": 0,          ← 队列未满 (不应 drop)
#   "shadow_panic_total": 0,         ← shadow 未崩溃
#   ...
# }

# 计算比率:
# diff_ratio = shadow_diff_total / shadow_sampled_total
#            = 2 / 10 = 20%  (在合理范围内)
```

### 3.4 segment table 状态

```bash
curl -s http://localhost:8080/debug/vars 2>/dev/null | jq '.pasr'

# 应看到 (stage 1 末期):
# {
#   "segment_count": 0,              ← shadow ReadOnly, 段表不创建
#   "segment_creates_total": 0,
#   "segment_evictions_total": 0,
#   "first_pick_total": 0,           ← 因为 segment_count==0
#   ...
# }
```

### 3.5 决策条件

| 指标 | 预期 | 判定 |
| --- | --- | --- |
| `shadow_diff_total` / `shadow_sampled_total` > 80% | PASR 选错严重 | **ABORT** |
| `shadow_drop_total` > 0 | 队列满, 有丢弃 | **ABORT** |
| `shadow_panic_total` > 0 | shadow 崩溃 | **ABORT** |
| `shadow_sampled_total` ≥ 10 | 采样足量 | ✅ 继续 |
| 无新的 error 日志 | 稳定 | ✅ 继续 |

**ABORT 操作**: 见 §4 Rollback

---

## 4. Stage 2: Shadow 25% (~3 天)

**目标**: 验证段表学习启动 + latency 无显著上升

### 4.1 ENV 切换

```bash
export HUAKAI_POOL_SELECTOR_MODE="shadow"
export HUAKAI_POOL_SELECTOR_SHADOW_PCT="25"
# 重启网关
```

### 4.2 流量累积

目标 ≥ 1000 个请求 (可用 3 天自然流量或压测加速)

### 4.3 指标检查

```bash
curl -s http://localhost:8080/debug/vars 2>/dev/null | jq '.pasr_dispatch' | grep -E "(mode_|shadow_)"

# 关键行:
# "mode_default_total": 750,
# "mode_shadow_total": 250,        ← ~25% 采样
# "shadow_sampled_total": 250,
# "shadow_diff_total": 30-50,      ← ratio 应 < 30%
# "shadow_match_total": 200-220,   ← 大部分一致
```

### 4.4 Latency 校验

```bash
# 需在网关日志或 APM 工具中观察:
# P95 latency (shadow mode 25%) vs P95 latency (stage 0 default)
# 应 ≤ +10% (即若 default P95=100ms, shadow P95 应 ≤ 110ms)

# 如果没有 APM, 可用 curl 手动测几个请求并计时
time curl -X POST http://localhost:8080/v1/chat/completions ... > /dev/null
# 观察 real 时间
```

### 4.5 决策条件

同 Stage 1, 但 diff_ratio 阈值略松 (可容纳 ≤ 30%):

| 条件 | 判定 |
| --- | --- |
| diff_ratio > 80% | **ABORT** |
| `shadow_drop_total` > 0 | **ABORT** |
| `shadow_panic_total` > 0 | **ABORT** |
| P95 latency 上升 > 10% | **ABORT** (查 shadow worker 是否过载) |
| 其他正常 | ✅ 继续 Stage 3 |

---

## 5. Stage 3: Shadow 100% (~7 天)

**目标**: 验证全量采样 + segment table 学习效果 + cache locality

### 5.1 ENV 切换

```bash
export HUAKAI_POOL_SELECTOR_MODE="shadow"
export HUAKAI_POOL_SELECTOR_SHADOW_PCT="100"
# 重启网关
```

### 5.2 流量累积

目标 ≥ 5000 个请求 (7 天自然流量)

### 5.3 关键指标

```bash
curl -s http://localhost:8080/debug/vars 2>/dev/null | jq '.pasr_dispatch'

# 期望:
# "mode_default_total": 0,
# "mode_shadow_total": 5000+,      ← 100% 采样
# "shadow_sampled_total": 5000+,
# "shadow_match_total": ≥3500,     ← 70%+ 一致性
# "shadow_diff_total": ≤1500,
```

### 5.4 Segment table 学习验证

```bash
curl -s http://localhost:8080/debug/vars 2>/dev/null | jq '.pasr'

# 期望 (因为 stage 3 shadow 仍是 ReadOnly, 不应写):
# "segment_count": 0,              ← shadow ReadOnly 不污染
# "segment_creates_total": 0,

# 但如果段表 *确实被污染* (比如误配置), 会看到:
# "segment_count": 100-500,
# "segment_creates_total": 100+,
# → 这表示 shadow 参数有问题, ABORT 查代码
```

### 5.5 决策条件

| 指标 | 预期 | 条件 |
| --- | --- | --- |
| `shadow_match_total` / `shadow_sampled_total` | ≥ 70% | ✅ 进 Stage 4 |
| | < 70% | ❌ ABORT (PASR 选择有问题) |
| `shadow_drop_total` | 0 | ✅ |
| | > 0 | ❌ ABORT |
| segment 未被污染 | `segment_count == 0` | ✅ |
| | > 0 | ❌ ABORT (ReadOnly 失效) |

**关键决策**: 这是 shadow 的终点。通过 stage 3 即说明 PASR 逻辑基本可靠，可进 **canary 真写** 阶段。

---

## 6. Stage 4: Canary 5% (~24 小时)

**⚠️ 关键转折**: 从 **shadow 异步只读** → **canary 真写** (slot + claim)

### 6.1 ENV 切换

```bash
export HUAKAI_POOL_SELECTOR_MODE="canary"
export HUAKAI_POOL_SELECTOR_CANARY_PCT="5"
# 重启网关
# 此时 PASR 实例开始写 slot acquisitions + billing claims
```

### 6.2 监控新指标

```bash
curl -s http://localhost:8080/debug/vars 2>/dev/null | jq '.pasr_dispatch'

# 新增关键行:
# "canary_pasr_used_total": 5%,                  ← PASR 真写的请求数
# "canary_default_used_total": 95%,
# "canary_pre_mutation_fail_fallback_total": <5,  ← pre-mutation 失败 fallback default
# "canary_post_mutation_fail_release_total": 0,   ← post-mutation 失败数 (必须为 0!)
```

### 6.3 数据库验证

```bash
# 检查 slot acquisitions 表是否有新数据
psql -h localhost -U postgres -d huakai -c \
  "SELECT COUNT(*) FROM pool_slot_acquisitions;"
# stage 4 启动后应快速增长 (每秒几十行)

# 检查 claims 表
psql -h localhost -U postgres -d huakai -c \
  "SELECT COUNT(*) FROM billing_claims WHERE claim_source = 'pasr';"
# 应有数据增长 (billing_claims 会记录 PASR claim)
```

### 6.4 决策条件 (最严格)

| 指标 | 预期 | 判定 |
| --- | --- | --- |
| `canary_post_mutation_fail_release_total` | **必须为 0** | ✅ / ❌ ABORT |
| `canary_pre_mutation_fail_fallback_total` | < 5% canary 请求 | ✅ |
| | > 5% canary 请求 | ❌ ABORT |
| slot_acquisitions 正常增长 | 无 DB error | ✅ |
| 无新 panic 日志 | 24h 运行 | ✅ |

**post_mutation 失败 = BLOCKING bug**: 立即 ABORT, 不进 stage 5。

---

## 7. Stage 5: Canary Ramp (24h × 3 阶段)

**目标**: 逐步提升 PASR 实际流量比例，观察生产稳定性

### 7.1 阶段序列

```
Canary 5%   (24h) → Canary 25%  (24h) → pasr-primary (24h)
↓                   ↓                     ↓
fnv hash, 5%桶    fnv hash, 25%桶      全量 PASR
fallback OK       fallback OK           pre-fail fallback OK
```

### 7.2 Canary 5% → 25% (24h 后)

```bash
export HUAKAI_POOL_SELECTOR_CANARY_PCT="25"
# kill -SIGTERM 旧网关进程
sleep 5  # 让 stop handler 优雅关闭
# 重启网关
```

监控相同指标，确保无 post_mutation_fail。

### 7.3 Canary 25% → pasr-primary (24h 后)

```bash
export HUAKAI_POOL_SELECTOR_MODE="pasr-primary"
# canary_pct 此时被忽略 (mode 改为 pasr-primary 即全量走 PASR)
# 但 pre-mutation fail 仍可 fallback default
```

### 7.4 新指标: first_pick_total

```bash
curl -s http://localhost:8080/debug/vars 2>/dev/null | jq '.pasr'

# 应看到 (与 stage 3 shadow 不同):
# "segment_count": 100-500,        ← 实际在学习 cache locality
# "first_pick_total": 1000+,       ← PASR segment 直接命中
# "failover_total": 100-300,       ← segment miss → HRW fallback
# "cache_hit_observations": 2000+, ← segment 记录的 cache hits
```

### 7.5 决策条件

- `canary_post_mutation_fail_release_total` 继续为 0
- `first_pick_total` / (first_pick + failover) ≥ 70% (segment 学习效果)
- 无新 alert / error 日志

---

## 8. Stage 6: pasr-strict (验收终态)

**目标**: PASR 完全替代 default，任何错误 fail closed

```bash
export HUAKAI_POOL_SELECTOR_MODE="pasr-strict"
# 重启网关
# 现在 PASR 失败 (无论 pre/post) 全部 fail closed, 不 fallback
```

### 8.1 预期行为

- 所有请求全走 PASR (不走 default)
- PASR 错误直接返回给客户端 (不是 fallback)
- segment hit rate 应稳定 ≥ 80%

### 8.2 验收指标

```bash
# 最终确认:
curl -s http://localhost:8080/debug/vars 2>/dev/null | jq '.pasr_dispatch | {mode_pasr_strict_total, mode_default_total}'

# 应看到:
# "mode_pasr_strict_total": 100%,
# "mode_default_total": 0,
```

---

## 9. Rollback 操作 (紧急)

任何阶段触发 ABORT 条件，立即执行:

### 9.1 快速回滚

```bash
# Step 1: 更改 ENV 回到 default
export HUAKAI_POOL_SELECTOR_MODE="default"

# Step 2: 发送 SIGTERM 给网关进程
ps aux | grep "gateway.*main"
# 找到 PID, 然后:
kill -SIGTERM <PID>
# 网关会做 graceful shutdown (defer Close + 等待 dispatcher.Stop)

# Step 3: 等待进程退出 (通常 < 10s)
sleep 3
ps aux | grep "gateway.*main"  # 确认已退出

# Step 4: 启动新进程 (default 模式)
HUAKAI_POOL_SELECTOR_MODE="default" \
HUAKAI_DATABASE_URL="..." \
  go run ./cmd/gateway/main.go &
```

### 9.2 关键特性

- **无 DB 迁移**: 段表在内存, 进程 stop 时自动清空, PG 不污染
- **无 cache 清空**: 下次启动 default 不需要任何 init 步骤
- **总耗时**: 5 pod ≈ 3min, 50 pod ≈ 5-8min (k8s 滚动重启)

---

## 10. 监控 Dashboard 必备面板

实操中需要在 Prometheus / Grafana 或类似工具中建立以下 dashboard panels:

### 10.1 模式分布

```prometheus
# 各 mode 总请求数
sum by (mode) (rate(pasr_dispatch_mode_total[5m]))

# 各 vendor × mode 分布
sum by (vendor, mode) (rate(pasr_dispatch_by_vendor_mode_total[5m]))
```

### 10.2 Shadow 对比质量

```prometheus
# shadow 匹配率
pasr_dispatch_shadow_match_total / pasr_dispatch_shadow_sampled_total

# shadow diff 比率
pasr_dispatch_shadow_diff_total / pasr_dispatch_shadow_sampled_total

# shadow drop (应为 0)
rate(pasr_dispatch_shadow_drop_total[5m])
```

### 10.3 Canary 安全性

```prometheus
# post-mutation fail (CRITICAL, 应为 0)
rate(pasr_dispatch_canary_post_mutation_fail_release_total[5m])

# pre-mutation fail & fallback
rate(pasr_dispatch_canary_pre_mutation_fail_fallback_total[5m])
```

### 10.4 Segment table 学习

```prometheus
# 段表大小
pasr_segment_count

# 段命中率
pasr_first_pick_total / (pasr_first_pick_total + pasr_failover_total)

# cache hit 观察数
rate(pasr_cache_hit_observations[5m])
```

---

## 11. 已知限制 (Owner 须知)

### 11.1 Bedrock 不含有效信号

Owner 无 AWS 凭据，Bedrock 路径的 shadow diff 数据是 mock，**不能**用于 stage 1-3 decision criteria。建议:

- 若 diff_ratio 在 "anthropic/openai/gemini/codex" 3-4 vendor 中健康 (< 30%)，忽略 bedrock 数据
- 若其他 vendor 都异常，bedrock 数据可作参考但不决策

### 11.2 8 vendor 反转 + 5 account 直通暂停

per CLAUDE.md directives，vendor account 反转与 account 直通 (redirect 不含 provider 直通键) 暂停，不在本 SOP 范围。本阶段仅验证 4 个 "正向" vendor。

### 11.3 Segment table 跨租户隔离已完成

M5b commit 已将 tenant_id 加入 segment key，per-(tenant, pool_group) 隔离生效。无需额外配置。

### 11.4 Messages handler 与 chat handler 共享 dispatcher

D2 已将 vendor slice metric 接入 chat handler。Messages handler 使用同一 dispatcher 实例，metric 自动生效。不需单独验证。

---

## 12. 决策点 (Owner 拍板)

本 SOP 执行中可能遇到的 decision points (需 Owner 确认):

### 12.1 Shadow 启动期预期 (O-1)

Stage 1-2 启动后，shadow 期间可能有大量 `ErrNoEligibleAccount` (segment 冷启)，此时 shadow worker 走 full ring fallback。

**问题**: 是否在 dashboard alert 中设置 60s warmup 静默?

**选项 A**: 静默 60s, 之后告警阈值 ≥ 10% cold-miss
**选项 B**: 不静默, 直接看 5min 滑动窗口

**建议**: 选 A (合理 ops noise floor)

### 12.2 Shadow 100% 观察时长 (O-2)

Stage 3 shadow 100% 到 stage 4 canary 5% 之间的观察时长。

**选项 A**: 7 天 (claude lane)
**选项 B**: 24h shadow 100% (codex lane)

**建议**: B 的变体 — shadow 5% × 24h → 25% × 24h → 100% × 48h (总 4 天, 覆盖工作日+周末)

### 12.3 Segment 参数是否暴露为 ENV (O-3)

当前段表 cap/老化/load 参数烧死在代码里，是否暴露为 ENV?

**选项 A**: 暴露 3 个 ENV: `HUAKAI_PASR_SEGMENT_CAP` / `_SEGMENT_MAX_AGE` / `_LOAD_CAP`
**选项 B**: 暂不暴露，等 SOP 完后按需添加

**建议**: 选 A (ops 调参不改代码，低成本高价值)

---

## 13. 执行清单

本 SOP 开始前请打印或复制:

- [ ] 前置条件 §1.2 全部通过
- [ ] Stage 0 default 验证完成 (§2)
- [ ] Stage 1 shadow 5% 通过决策条件 (§3)
- [ ] Stage 2 shadow 25% 通过决策条件 (§4)
- [ ] Stage 3 shadow 100% 通过决策条件 (§5)
- [ ] O-1 / O-2 / O-3 Owner 拍板
- [ ] Stage 4 canary 5% 完成，post_mutation_fail = 0
- [ ] Stage 5 canary ramp (5% → 25% → primary) 完成
- [ ] Stage 6 pasr-strict 进入验收
- [ ] 最终 metric 快照已保存 (用于 release notes)
- [ ] runbook 已存档 (用于未来升级参考)

---

## 14. 问题排查

常见问题与快速排查:

| 现象 | 可能原因 | 排查命令 |
| --- | --- | --- |
| segment_count 不为 0 (shadow 期) | shadow ReadOnly 失效或未生效 | `grep "ReadOnlySegments: true" selector_wiring.go` |
| shadow_diff_ratio > 80% | PASR 选择逻辑有 bug | 对比 default 与 shadow 选的 account_id |
| canary_post_mutation_fail > 0 | slot acquire 或 claim write 失败 | 检查 PG 连接、quota 状态、disk 空间 |
| P95 latency 上升 > 10% | shadow worker 过载或 DB 响应慢 | 看 shadow_drop_total, 如 >0 说明队列满 |
| mode_default_total 不为 0 (canary/primary/strict) | dispatcher routing bug | 检查 Session 构造与 fnv hash 一致性 |

---

## 附录: Metric 完整列表

实操中可能用到的所有 `/debug/vars` 字段 (仅列关键):

**pasr_dispatch** namespace:
- `mode_<x>_total` — 各 mode 请求总数
- `shadow_sampled_total` — shadow 入队成功数
- `shadow_match_total` — shadow 与 default 选择一致
- `shadow_diff_total` — shadow 与 default 选择不同
- `shadow_drop_total` — shadow 队列满丢弃数
- `shadow_panic_total` — shadow 崩溃计数
- `canary_pasr_used_total` — canary 命中 PASR 的请求
- `canary_default_used_total` — canary miss PASR 的请求
- `canary_pre_mutation_fail_fallback_total` — pre-mutation 失败 fallback
- `canary_post_mutation_fail_release_total` — post-mutation 失败数

**pasr** namespace:
- `segment_count` — 当前活跃段数
- `segment_creates_total` — 段创建总数
- `segment_evictions_total` — 段老化清理总数
- `first_pick_total` — 段直接命中次数
- `failover_total` — segment miss → HRW fallback 次数
- `cache_hit_observations` — segment 记录的 cache hits 总数

**cache_token_count_by_account.<account_id>**:
- `creation_total` — 该账号 cache 创建次数
- `read_total` — 该账号 cache 读取次数
- `request_count` — 该账号请求总数

---

**最后更新**: 2026-05-09  
**下一步**: Owner 确认前置条件 + 拍板 O-1/O-2/O-3，启动 Stage 0
