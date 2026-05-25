# Token Refresh Worker 闭环 — Synthesis (Claude × Codex)

- UTC: 2026-05-24T07:48Z
- 输入:
  - Claude: [2026-05-24-token-refresh-worker-closure-claude.md](2026-05-24-token-refresh-worker-closure-claude.md) (27K,6 个 D 决策)
  - Codex: [2026-05-24-token-refresh-worker-closure-codex.md](2026-05-24-token-refresh-worker-closure-codex.md) (99K,601 行,5+3 个 D 决策)
- Ref anchor: [../2026-05-24-ref-anchor.md](../2026-05-24-ref-anchor.md)

## §A 共识区(直接落地)

| 主题 | Claude | Codex | 一致 |
|---|---|---|---|
| 4 lane 拆法 | L-A bootstrap / L-B endpoint / L-C mimicry / L-D 健康 | D-A/B/C/D-001 同 4 lane | **同结构** |
| Vendor exchanger 注册 | D-1 注册表 (`vendor_exchangers.go`) | (Codex 没单列,假设 map 派发) | **map/注册表 一致** |
| Endpoint catalog 持久化(初期) | D-2 PG 表 (后续做) | D-B-001 选 A:encrypted payload 先,schema 后续 plan | **共识 A 先做轻方案**,Claude PG 列在后续切片 |
| Mimicry 默认 disabled | (Claude D-3 偏文件配置) | D-C-001 A:audit-only resolver,默认 disabled | **共识默认 disabled** |
| Health 字段先用现有 | (Claude D-4 加 health_state 列 = 算 schema 改) | D-D-001 A:写现有 credential/account state + audit only | **冲突** → §B-1 |
| Scheduler coupling | D-6 dispatcher 接入压轴切片 | D-D-002 A:health runner 通过 Scheduler/Refresher (Recommended) | **共识走 Scheduler** |

## §B 冲突区(必 Owner 拍板)

### B-1 Health 持久化:加列 vs JSON metadata?

- **Claude D-4 立场**:加 migration 0008 在 `provider_account` 表加 `health_state` enum + `health_changed_at` timestamp + `cooldown_until` timestamp
- **Codex D-D-001 立场**:不动 schema,health 写现有 `credential/account state` + audit-only,**任何新 schema 都触发 D-SCHEMA-001(Owner 高风险确认)**
- **冲突点**:health 状态 query 频率 / dashboard 是否要直接 SQL 查 / cooldown_until 是否需要索引
- **借鉴对照**:
  - `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:269` — sub2api **加了 history 表 + 日 rollup**(schema 路径)
  - `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:33` — sub2api **monitor 长周期跑,有 retention/aggregation**
  - `BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/oauth2_token_cache.py:80` — litellm 用 lock + double-check 做 cache 锁,health 信息在内存
- **Owner 拍板维度**:HUAKAI 要不要做 sub2api 那种 health history / 日 rollup(就需要 schema);否则 JSON metadata 够。

### B-2 Mimicry 接入边界:audit-only 还是真 transport?

- **Claude D-3 立场**:profile 文件配置 + interface 注入点,**RoundTripper 实现等 [[project_l1_tls_boringssl]] 决策**;不做 stdlib 假 mimicry
- **Codex D-C-001 立场**:audit-only profile resolver,**默认 disabled**;若要真 transport 走单独 plan
- **Codex D-LEGAL-001 加 meta-rule**:任何非默认 mimicry policy 启用需 Owner/legal review
- **冲突点**:基本一致(都 audit-only / 不真 transport),但 codex 加了 **legal review 闸门**
- **Claude 缺**:没考虑 legal 维度(模仿真客户端 fingerprint 是否合规)
- **Owner 拍板维度**:legal gate 加不加(类似 F-CRED 信任链有合规闸门)

### B-3 Vendor enablement 顺序(Codex D-A-001 新增维度)

- **Claude 未列**(默认全 6 vendor 同时做)
- **Codex D-A-001 立场**:
  - A: 只 enable fake/test mode adapter
  - B: per-vendor 真 OAuth 加 feature flag (Owner 逐个开)
  - C: long-lived / session-like 材料保持 manual-first
  - Codex 推 **A first merge,B 按 vendor approval,C 持续 manual**
- **冲突点**:Claude 默认 4 vendor 真 OAuth 同时上(anthropic/gemini/antigravity/copilot),Codex 推 A 先全 fake
- **Owner 拍板维度**:风险偏好 — 一次性 4 vendor 真 OAuth vs 逐步推

## §C 单边维度(可独立拍)

### C-1 PKCE CSRF 测试要求 (共识但 Claude 更详细)

Claude L-A R-LA1 给了具体 mutation 测试(删 state 校验 → test 必须红);Codex L-A 也有但描述更简。Owner 不必拍。

### C-2 Cooldown 时长 (Claude D-4)

- 3 连封 + 30min / 1 次 1h / 指数退避 / 配置可调
- Codex 未单列
- Claude 推 D (配置可调,默认 3+30min)

## §D 推荐执行序

1. **L-A bootstrap** — 注册表式 vendor exchanger + device-code + SSO entry;**先 fake mode** (Codex D-A-001 A),覆盖 PKCE/CSRF/RFC 8628 风险测试
2. **L-B endpoint catalog** — 加密 payload 先 (Codex D-B-001 A);PG 表后续切片;cover copilot endpoint.api / gemini bl= 采集
3. **L-C mimicry profile resolver** — audit-only 默认 disabled (Codex D-C-001 A);**LEGAL gate 加上** (Codex D-LEGAL-001)
4. **L-D 健康 maintenance** — 写现有 credential/account state + audit (Codex D-D-001 A);**B-1 决策决定是否额外加 history 表**
5. **L-Z dispatcher 接入 health_state** — 压轴

## §E 借鉴项目对照(CLAUDE.md #15)

| 维度 | CLIProxyAPI MIT | sub2api LGPL(paraphrase only) | litellm Apache-2.0 | envoy-ai-gateway Apache-2.0 | portkey-gateway MIT |
|---|---|---|---|---|---|
| 多 vendor login bootstrap | internal/auth/{antigravity,gemini,kimi,xai,codex,claude}/ 各 vendor 独立 oauth_server | oauth_service.go | github_copilot/authenticator.py 单 vendor | 不 bootstrap (是 control plane) | 单 vendor 模式 |
| 动态 endpoint | 无 | enabled flag + endpoint config | 无 | api/v1beta1/ai_service_backend.go:47 first-class | provider 静态 |
| Mimicry transport | utls_transport.go (per vendor) | tls_fingerprint_profile_service.go (默认 disabled) | 无 | 无 | 无 |
| 长周期 health | 无 | channel_monitor_service.go:269 history + 日 rollup,runner.go:33 长周期 monitor | proxy/_experimental health_check | 无 | 无 |
| 适合 HUAKAI | bootstrap entry 主参考 | health/monitor 行为参考(LGPL paraphrase) | copilot refresh + cache lock 参考 | endpoint first-class 模式参考 | provider registry 参考 |

## §F Owner 决策清单(Surface)

| ID | 决策 | 选项 | 推荐 | 必要性 |
|---|---|---|---|---|
| TR-D1 (B-1) | Health 持久化方案 | (A) JSON metadata + audit only / (B) schema 加 health_state 列 + history 表 | **Owner 选 — 影响 dashboard 能力 + ops 复杂度** | **必决**,卡 L-D |
| TR-D2 (B-2) | Legal review gate 加不加 | (A) 加 D-LEGAL-001 启用前 Owner/legal review / (B) 不加 (Claude 立场) | **(A) 加** — codex 思路对,信任链项目要 legal 闸门 | **必决**,卡 L-C |
| TR-D3 (B-3) | Vendor enablement 顺序 | (A) 第一波全 fake/test mode,逐 vendor 解锁 / (B) 4 真 vendor 同时上 | **(A)** — Claude 同意 codex 谨慎序 | **必决**,卡 L-A |
| TR-D4 (Claude D-4) | Cooldown 策略 | 3+30min / 1+1h / 指数 / 配置可调 | (D) 配置可调默认 3+30min | 中等,后排 |
| TR-D5 (Codex D-B-001) | Endpoint catalog 持久化时机 | (A) payload 先,后续 schema | (A) 共识 | 已决 |
| TR-D6 (共识) | Mimicry 默认 | disabled | 已共识 | 已决 |

## §G Lane + UTC

- Synthesis: Claude (claude-opus-4-7)
- UTC: 2026-05-24T07:48Z
- Inputs: Claude 6 D + Codex 5+3 D (含 meta-rule SCHEMA / LEGAL / REF)
- 关键 cross-discuss 收获:codex 加的 **D-LEGAL-001 mimicry 启用 legal 闸门**、**D-A-001 vendor enablement 谨慎序**、**D-SCHEMA-001 任何新 schema Owner 必决** 是 Claude plan 缺的维度,纳入 synthesis
