# 2026-05-15 R-3 R-E Mainline Rust 数据面接入 Claude 平行计划

| Lane | Claude（与 codex β 平行；非交流前独立产出） |
| Owner directive | R-3 R-E = 把 Rust 数据面切到生产主线,R-SEC-002 (HIGH, mTLS / UDS) 是前置,hyper-rustls fallback 仍保留。 |
| Scope | In: 切换闸门设计 (GO/NO-GO 条件);R-SEC-002 transport 鉴权设计 (3 选项对比 + 推荐);fallback 保留期 + 回退脚本;feature flag ramp 策略;切换后回归套件;Owner OCAW 决策点。Out: 实施 mTLS (本计划只设计 + 写 plan);改 LICENSE / billing / quota / auth core;新 dep。 |
| Success criteria | plan 含 4 个 OCAW 决策点;切换前置条件可机器化校验 (cargo 测试 + cargo deny + risk-register R-SEC-002 状态);ramp 策略给出明确比例 + 回退触发器;不删 fallback。 |
| Time estimate | 计划: ~30 min。实施 (R-SEC-002 + ramp wire): 4-8 天 (R-3 plan 估)。 |
| Blast radius | HIGH (生产 transport 主线切换)。任何回退路径破坏会立即影响所有上游 dispatch。 |
| Failure modes | 1. mTLS / UDS 在容器 / K8s / 本地 dev 三种环境兼容性差异;2. ramp 比例没法快速回退 (无 control-plane feature flag 热切换 API);3. fallback 长期保留导致 codepath 维护负担。 |
| Mitigation | mTLS 设计选 UDS (本地无 cert 管理 + 内核保护) 优先,TCP+mTLS 备选;feature flag 通过控制面 config endpoint 热切换 + 兜底 envvar;fallback 保留 12 个月,期间累计指标决定移除。 |

## R-SEC-002 transport 选项对比 (Claude 推荐)

| 选项 | 优点 | 缺点 | 推荐? |
|---|---|---|---|
| **Unix Domain Socket (UDS) + filesystem perm** | 无需 cert 管理;系统 UID/GID 即认证;kernel 隔离;无明文 over network | 同机限定 (无法跨 host);需要 dev/staging 环境特殊处理 | ⭐ Phase R-E 默认 |
| **TCP + mTLS** | 跨 host;成熟 | cert 生命周期 / 旋转管理;handshake 开销 | 备选 (multi-host 部署) |
| **共享 secret HMAC** | 实施最简单;无 cert | secret 旋转难;不防中间人;脆弱性高 | 拒绝 |

UDS 落地最少改动;HUAKAI L1 / L2 personal edition 都是单机部署。L4 SaaS 多机时升级到 mTLS。

## 切换闸门 (GO/NO-GO)

切到 production 主线前必须全 GREEN:
1. R-3 R-D 端到端验真 4 cargo combo PASS + 真上游回放 (Owner 本机) match
2. R-SEC-002 transport 鉴权实施 + 单测 + 集成测 PASS
3. R-LIC-003 cargo deny check licenses 无 GPL/LGPL 进 normal dep
4. hyper-rustls fallback 仍可手动切回 (单测验证 fallback path)
5. Risk register R-SEC-002 状态升 `Mitigated` (γ lane 输出后)
6. Owner OCAW 签字 (4 个决策点全答)

## Ramp 策略

ramp via `config.mimicry.production_ratio` 0.0 → 1.0:

| 阶段 | ratio | 触发回退条件 |
|---|---|---|
| 0 | 0.000 | 默认 |
| 1 | 0.001 | 1 小时内 5xx 上升 > 2× baseline → ratio=0 |
| 2 | 0.01 | 24 小时回归测试 + 0 incident |
| 3 | 0.1 | 同上 + ja3 漂移率 < 1% |
| 4 | 1.0 | 7 天 stable |

每阶段写 `audit_event { type: mimicry_ramp_change, from_ratio, to_ratio, reason }`,运维可视。

## 4 个 OCAW 决策点

(D1) UDS 还是 TCP+mTLS 作为 default? Claude 推 UDS,Owner 确认。  
(D2) hyper-rustls fallback 保留多久? Claude 推 12 个月 stable + 12 个月 monitor。  
(D3) ramp 触发回退是自动 (control plane 看监控)还是手动 (运维 dashboard 按钮)? Claude 推手动 + 自动告警,自动回退 Phase 8+。  
(D4) R-3 R-E commit / push 后是否同步 surface 给 R-D 团队 (即 Lane α 输出)做 final sync? Claude 推必须。  

## 与 codex β 可能分歧

- (a) codex 可能推 mTLS over TCP 作为 default (更通用),Claude 推 UDS (HUAKAI L1/L2 单机)。
- (b) codex 可能不强调 fallback 保留期,Claude 坚持至少 12 个月。
- (c) codex 可能让 R-SEC-002 mTLS 实施与 R-E 切换同一 atom,Claude 推 R-SEC-002 单独 atom 先实施再切换。
- (d) ramp 比例 codex 可能更激进,Claude 0.001 起非常保守。

Synthesis: 看 codex β 选项决议,如果它推 TCP+mTLS,Claude surface "UDS 备选" 给 Owner;如果它推 UDS 一致,直接合并。

## 严禁

- 实施 mTLS / UDS 代码 (本计划只设计)
- 改 backend/ (Go 控制面)
- 引入新 dep
- 改 LICENSE / billing / quota / auth core

## Source

Read: `docs/plans/2026-05-14-r3-on-merged-closure-codex.md` (§4 Phase R-E),`docs/10_RISK_REGISTER.md` (R-SEC-002 / R-TRANSPORT-001),`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/` (现有结构),`docs/plans/2026-05-15-r-3-r-d-claude.md` (本人 R-D 平行计划)。

Lane: Claude parallel  
Agent: Claude Opus 4.7 (1M context)  
UTC timestamp: 2026-05-15T12:06:00Z
