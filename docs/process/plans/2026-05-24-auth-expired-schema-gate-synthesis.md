# Auth Expired Outcome Schema Gate — Synthesis (Claude × Codex)

- UTC: 2026-05-24T11:35Z
- 输入:
  - Claude: [2026-05-24-auth-expired-schema-gate-claude.md](2026-05-24-auth-expired-schema-gate-claude.md) (12K,5 D 决策)
  - Codex: [2026-05-24-auth-expired-schema-gate-codex.md](2026-05-24-auth-expired-schema-gate-codex.md) (51K,6 D 决策)

## §0 Codex 揭示 Claude 漏的事实

| # | 事实 | Claude plan 错误 | 影响 |
|---|---|---|---|
| F-1 | **DB 现状是 `outcome text` 列 + CHECK constraint**,不是 audit_outcome enum type | Claude 假设 outcome 字段已是 text 无 enum,推荐"加 PG enum type" | D-A 改向:codex 推荐 text + CHECK,enum 是 alternative |
| F-2 | **`provider_accounts.health_state` 已存在 (text 列)** + `health_state_until` 也在 + `oauth_endpoint_health` 也在 | Claude plan §4 推荐 "migration 0056 加 health_state 列" | 不需要新加列,只需 outcome 接入 + cooldown 映射 |
| F-3 | DB 已有 `channel_health_state` type / `channel_health_audit_events` table / `channel_health_admin_alerts` table | Claude 完全没引用现有 channel-health 框架 | D-C 多了 channel-health 复用选项 |

**结论**:Claude plan §4 migration 0056 不需要;0055 单 outcome enum/CHECK 一个 migration 即可;TR-D1 health 字段早已就位。

## §A 共识区(直接落地)

| 主题 | Claude | Codex | 一致 |
|---|---|---|---|
| outcome 集合 | D-AE-1 (A) 7 个 | D-B 推荐 (B) Owner 拍 4 类 + 物理拼写 | **以 codex 4 类为基线**(auth_expired/rate_limit/risk_control/account_disabled,后续可扩) |
| cooldown 默认 | D-AE-3 沿 PH-D3/TR-D4 配置可调 3+30min | D-E (A) 同 | **共识 A** |
| dispatcher 接入 health_state | D-AE-4 (B) 独立切片压轴 | (codex 没单列,假设拆) | **共识独立切片** |
| frozen package 不动 | 同 | 同 | 共识 |
| 不引新依赖 | 同 | 同 | 共识 |
| migration 0055 单做 outcome | (Claude 推 0055 outcome + 0056 health) | D-D (B) 推 0055 只做 outcome,health cooldown 拆 0056 | **共识 0055 仅 outcome**;**但 F-2 显示 health_state 列已就位,0056 不需要新列,只需 wire dispatcher** |

## §B 冲突区(必 Owner 拍板)

### B-1 outcome 物理 schema (codex D-A)

- **Codex 推荐 A**:**text + CHECK constraint**(保持现状最小改);sqlc 不动
- **Claude 推荐**:**PG enum type**(强 type safety)
- **冲突点**:type safety vs migration complexity
- **Codex 论点**:Sub2API/LiteLLM 都用 text/string 没 PG enum;新 enum 引入 lock + sqlc churn + down migration 复杂
- **Owner 拍板维度**:operator 用 enum 还是 CHECK 检查 outcome 一致性?

### B-2 cooldown 存储 (codex D-C)

| 选项 | 大白话 | ref 对照 |
|---|---|---|
| (A) **复用 health_state_until** | 列已在 PG,语义类似 cooldown deadline | Sub2API channel_monitor:LGPL paraphrase 自带 maintenance/deadline |
| (B) 加 cooldown_until 新列 | 跟 PH-D3/TR-D4 文档名字一致 | (HUAKAI 自研) |
| (C) channel_health_state 表已有 cooldown_until,dispatcher join | 复用现有 F-CH-002 框架 | LiteLLM shared health manager (Apache-2.0) |

**Codex 推荐 A**:reuse `health_state_until`,不加新列。
**Claude D-AE 没单列此决策**。
**Owner 拍板维度**:命名一致性 vs schema 干净。

### B-3 last_refresh_outcome 镜像 (codex D-F,Claude 未列)

| 选项 | 大白话 |
|---|---|
| (A) `provider_accounts.last_refresh_outcome` 镜像新 outcome(admin UI 直接读)| 易查 |
| (B) `last_refresh_outcome` 维持粗 outcome,detail 走 audit table | schema 干净 |

**Codex 推荐 A 如果 admin UI 需要**;Claude 没决。**Owner 拍板维度**:运营 UI 是否直接显示 detailed cause。

## §C Claude 多出维度(纳入)

- **Claude D-AE-2 outcome enum vs text + check constraint** → 跟 codex D-A 重合,以 codex 立场为准 (text + CHECK)
- **Claude S2-S5 切片**(outcome.go Classify / R-A3 升级 / migration 0055 / dispatcher 接入)结构跟 codex §5 类似,合并

## §D 锁定后的执行序

```
[切片 S1: migration 0055 + outcome CHECK + outcome.go Classify] (1-2 天)
  ├── migration 0055:ALTER TABLE oauth_refresh_audit_events
  │     ADD CHECK (outcome IN (...4 类...))
  ├── credentialworker/outcome.go:RefreshOutcome string type + Classify(err, vendor, statusCode)
  ├── audit.go signature 改:outcome RefreshOutcome
  └── mutation tests:401→AuthExpired / 429→RateLimit / 403 risk→RiskControl

[切片 S2: copilot+anthropic refresher outcome 升级] (0.5 天)
  ├── copilot_refresher.go 401 → outcome=AuthExpired (R-A3 升级真 integration test)
  └── anthropic refresher.go 401/429/403 同

[切片 S3: health_state 接入 dispatcher] (1 天,可拆压轴)
  ├── provider_accounts.health_state 已存在 (F-2),只需 dispatcher 选号时过滤
  └── cooldown 用 health_state_until (B-2 A 立场)
```

## §E 借鉴对照(CLAUDE.md #15)

| 维度 | Wei-Shaw/sub2api@63b0631a5827 (LGPL paraphrase) | BerriAI/litellm@414866767176 (Apache-2.0) |
|---|---|---|
| outcome 表示 | string,无 enum | dict/struct status string |
| cooldown 字段 | service.go:269 监控时间戳;无独立 cooldown_until | shared_health_check_manager TTL/lock |
| 4 类细分 | 完整 health history + rollup,但 health 不直接对应 OAuth refresh outcome | healthy/unhealthy/unknown 三态,简化 |
| 适合 HUAKAI | health flow 模式参考;outcome 用 string + CHECK 一致 | 配置可调 cooldown 模式参考 |

HUAKAI 升级 delta:per-vendor outcome 分类 + 同事务 audit (P0-4) + 长周期 health_state(已就位)。

## §F Owner 决策清单(Surface)

| ID | 决策 | 选项 | 推荐 | 必要性 |
|---|---|---|---|---|
| AE-D1 (B-1) | outcome 物理 schema | (A) text + CHECK / (B) PG enum / (C) domain | **(A)** codex 立场,小改动 | **必决**,影响 0055 形态 |
| AE-D2 (B-2) | cooldown 存储 | (A) 复用 health_state_until / (B) 加 cooldown_until / (C) channel_health_state join | **(A)** codex 立场,不加新列 | **必决**,影响 dispatcher 实施 |
| AE-D3 (B-3) | last_refresh_outcome 镜像 | (A) 镜像 / (B) 不镜像 | **Owner 选** — admin UI 是否需要直接显示 | **必决**,影响 schema 改动 |
| AE-D4 (codex D-B) | outcome 集合 | (A) 只加 auth_expired / (B) 4 类 / (C) 7 类 | **(B) 4 类**(auth_expired/rate_limit_exceeded/risk_control_triggered/account_disabled) | **必决**,影响 enum/CHECK |
| AE-D5 (codex D-D) | health-state schema 拆 0055 还是 0056 | (A) 0055 含 / (B) 拆 0056 | **(B)** 0055 仅 outcome | 已共识 |
| AE-D6 (codex D-E) | cooldown 默认 | (A) 配置可调 3+30min | 已共识 A | 已共识 |

## §G Lane + UTC

- Synthesis: Claude (claude-opus-4-7)
- UTC: 2026-05-24T11:35Z
- Inputs: Claude 5 D + Codex 6 D
- 关键 cross-discuss 收获:codex 抓住 Claude 完全漏的 **F-1 DB 已是 text+CHECK 不是 enum** + **F-2 health_state 列已就位** + **F-3 channel_health_state 框架已在**,Claude plan §4 migration 0056 错,synthesis 修正
- 下一步:Owner 拍 AE-D1..D4 → 实施 S1+S2 → S3 压轴
