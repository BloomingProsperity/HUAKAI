# 2026-05-15 HIGH Risks (R-SEC-002 / R-TRANSPORT-001 / R-LIC-003) Mitigation Claude 平行评审

| Lane | Claude（与 codex γ 平行；非交流前独立产出） |
| Coverage | `docs/10_RISK_REGISTER.md` 3 个 HIGH:R-SEC-002 / R-TRANSPORT-001 / R-LIC-003。 |
| Method | retrospective + 推荐 mitigation,不实施 |
| Reviewer | Claude Opus 4.7 (1M context),不读 reference repos |

## Risk 1 — R-SEC-002 Rust 数据面 / 控制面 transport (HIGH, Open)

**当前状态**: RoutePlan 含 per-attempt upstream auth material;exploratory Rust 数据面 → Go 控制面无认证传输,credential 可能在传输路径泄漏。Mitigation 已 declared (mTLS / UDS / 本地认证 transport) 但未实施。

**Claude 推荐**:
- 前置: R-3 R-E mainline 切换之前必须 GREEN
- 实施 lane: 单独 atom (不与 R-E 切换合并),独立 plan + 独立 cargo + 独立 review
- 设计选项 (与本人 R-E plan 一致): UDS 优先,TCP+mTLS 备选
- 测试要求: 单测 + 集成测 + fuzz 测试 (auth material 跨 transport boundary 不泄漏)
- 状态: Open → Mitigated (mainline 切换后)

**截止时间建议**: R-E ramp Phase 0 (ratio=0.001) 之前完成。即 R-3 整体 4-8 天里前 2-3 天必须出原型。

**Owner**: Codex 实施 (R-SEC-002 line 写 Codex),Claude 审查。

## Risk 2 — R-TRANSPORT-001 Exact TLS mimicry patch burden (HIGH, Open)

**当前状态**: L2-A5 系列已落 (cipher_suites + ALPN + groups + sigalgs + EC point formats + extension 22 + extension list ordered-subset),全在 feature flag `mimicry-openssl` + `mimicry-http2-fork` 之后。已有 KnownGap 严格 fail-fast + provenance 记录。但 OpenSSL upstream 升级 / disabled algorithms 变化可能仍要打 patch。

**Claude 推荐**:
- 维持现状: feature flag 严格守门 + KnownGap fail-fast,production dispatch 永远 OCAW gate
- 加: 定期 cargo deny + 真上游回放回归 (每周 / 每月 cron),漂移立刻告警
- 加: `docs/RISK_REGISTER.md` 补一节 "OpenSSL patch policy" — 仅当 upstream 主线无法做时才打 patch;每次 patch 必须 doc + LICENSE 合规审 + review 签字
- 状态: Open → Ongoing-Monitor (策略到位但风险无法零),不是 Mitigated

**截止时间建议**: 策略文 + cron 监控 = R-3 R-E ramp Phase 2 (ratio=0.01) 之前必须有。

**Owner**: Claude 写 policy 段,Codex 实施 cron 监控。

## Risk 3 — R-LIC-003 GPL/LGPL runtime dep creep (HIGH, Open)

**当前状态**: wreq-util / rquest-util 已经 ban (per R-3 plan §0 spike 结论);但其它 transport / browser mimicry preset 类 dep 可能仍带 LGPL/GPL 风险。

**Claude 推荐**:
- 立刻跑 cargo deny check licenses (γ lane 应做) — 这次会跑成
- 加 dep 添加纪律: PR 引入新 dep 必须先跑 cargo tree --edges=normal | grep -i 'gpl' + 在 PR description 列出 license
- 加: `.deny.toml` (cargo-deny config) ban 列表显式含 `wreq-util`, `rquest-util`,以及未来发现的同类
- 加: 每次 R-E milestone 前一次性 license re-audit
- 状态: Open → Mitigated (cargo deny + ban list + PR 纪律到位后)

**截止时间建议**: cargo deny + ban list 今日完成 (γ lane 输出);PR 纪律下次 PR template update 时落地。

**Owner**: Codex 实施 (γ lane 已在做)。

## 横切观察

- 3 HIGH 全跟 R-3 强相关 → 推 R-E 是同时收 3 个 risk 的最佳时机。
- 建议序列: **γ lane cargo deny 今日出 → 1 周内 R-SEC-002 UDS 原型 → 2 周内 R-E ramp Phase 1 → 3-4 周内 ramp 完毕,R-SEC-002/R-LIC-003 升 Mitigated,R-TRANSPORT-001 升 Ongoing-Monitor**。

## 与 codex γ 可能分歧

- (a) codex γ 可能给 R-TRANSPORT-001 一个更激进的 Mitigation 选项 (例如直接换 BoringSSL 主线 fork),Claude 坚持 ongoing-monitor 不是 fully mitigated。
- (b) codex γ 可能漏掉"加 .deny.toml ban list" 这一具体落地,Claude 坚持要这步。
- (c) codex γ 可能把 R-SEC-002 截止时间设在 R-E 实施期间,Claude 坚持 R-E 切换前。

Synthesis: 看 codex γ 推荐与 Claude 对齐度;不齐处 surface Owner。

## 严禁

- 实施任何 mitigation (本评审只评估)
- 改 risk register 状态 (codex γ 负责)
- 跑 cargo deny (codex γ 负责跑)

## Source

Read: `docs/10_RISK_REGISTER.md`,`docs/plans/2026-05-14-r3-on-merged-closure-codex.md`,`docs/plans/2026-05-15-r-3-r-e-mainline-claude.md` (本人 R-E plan),`docs/reviews/2026-05-15-l2-lane2-retrospective-bulk-codex-review.md`。

Lane: Claude parallel  
Agent: Claude Opus 4.7 (1M context)  
UTC timestamp: 2026-05-15T12:06:00Z
