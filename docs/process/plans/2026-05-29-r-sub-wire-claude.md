# R-SUB-WIRE 实施计划（Claude 独立草案）

> Owner 定调 Option 3「全接通」：分组→路由 + 配额闸→热路径，含新机钱路径。本草案为 CLAUDE.md #10 平行双草中 Claude 一方，未看 opus-architect 草案。codex 用量上限到 2026-05-30 23:15，其平行草 + retro 待补。

## 目标

让订阅在运行时**真生效**：
- **R-SUB-WIRE-1**：订阅授予的 `users.user_group` 真正**限制路由**——某档用户只能用该档允许的渠道/模型（routes.user_group_match）。
- **R-SUB-WIRE-2**：订阅安装的 `quota_policies`（cost_usd 日历窗 daily/weekly/monthly caps）真正在**请求热路径拦截**——超额返 429，不转发。

## 现状（来自只读测绘工作流 wf_0f5479b3）

- `users.user_group`（P3a migration 0073）+ `routes.user_group_match`（migration 0001）**都存在，从未连接**。
- `GroupPolicyGate` 是 `internal/pool/router/gates.go` 里的占位 `AllowAllGate`（永远放行）——接口在我地盘，待实现。
- `quota.Service.Reserve()`（`internal/quota/service.go`）**已建好已测**，返回 `{Allowed, Decision, Reservation}`，但**请求路径从不调用**。
- 热路径：`chat_completions_handler.go` → `reserveClaim()`（dispatch.go:151）→ `billing.ClaimGate.Reserve()`（dispatch.go:159，新机钱路径，Tx1 余额/幂等）→ `selectPoolAccount()`（pool 选择）→ `upstream_dispatcher.Dispatch()`（转发）。

## 领地分类（硬约束）

| 文件 | 归属 |
|---|---|
| `internal/auth/api_key_resolver.go` (Identity) | 我 |
| `internal/router/route_plan.go` (RequestContext) | 我 |
| `internal/pool/router/{types.go,gates.go}` (SelectionRequest, GroupPolicyGate) | 我 |
| `internal/quota/*` (Service.Reserve) | 我（已建） |
| 新建 `internal/subscriptionenforce/*`、`internal/quotagate/*` | 我 |
| `cmd/gateway/{selector_wiring.go,wiring.go}` | 共享（串行） |
| `internal/gatewayhttp/chat_completions_dispatch.go` (prepareRoute, reserveClaim, selectPoolAccount) | **冻结**；reserveClaim 段 = **新机钱路径** |
| `internal/gatewayhttp/chat_completions_handler.go` (ChatHandlerDeps) | **冻结** |
| `internal/gatewayhttp/chat_completions_attempt.go` (selectPoolAccount) | **冻结**（路由，非钱） |
| `internal/billing/claim_gate.go` | **新机钱路径——绝不改** |

## 切片分解（小切片闭合，安全序）

### Slice A — 分组→路由判定逻辑（我地盘，安全，零行为变化）
- 新 `internal/subscriptionenforce` 包：实现 `GroupPolicyGate`（读 `routes.user_group_match`，按 (pool_group, model) 匹配请求的 user_group；**user_group 空 → 放行**，向后兼容无订阅用户）。
- `RoutesRepo` 接口 + PG 只读实现（查 routes 表；routes 是路由配置非钱，非冻结非新机）。
- `router.RequestContext` + `pool/router.SelectionRequest` 加 `UserGroup string` 字段（默认空，老代码无感）。
- 判别测试：同 model 不同 group 命中/拒绝；空 group 放行；mutation 删过滤→拒绝变放行→红。
- **不碰冻结/钱。Gate 在 selector_wiring 接好但 user_group 还没流进来 = 惰性。**

### Slice B — 配额闸适配（我地盘，安全）
- 新 `internal/quotagate` 包：`Gate.Admit(ctx, ReserveRequest) → (allowed, Decision, error)` 包 `quota.Service.Reserve()`，解释 decision；**未配置 → 放行（fail-open，惰性）**，瞬时错 → fail-closed（与 quota.Service 既有 fail-closed 一致）。
- 判别测试：超额→deny；未超→admit；store 错→fail-closed。
- **不碰冻结/钱。惰性直到热路径调用。**

### Slice C — auth 解析 user_group（auth 我地盘，契约被冻结消费）
- `auth.Identity` 加 `UserGroup string`；resolver 读 `users.user_group`（随 API key/session 解析一并取，向后兼容字段加）。
- 判别测试：有订阅用户解析出 group；无订阅默认空。
- **auth 是我地盘；Identity 被冻结代码读（只多读字段，不破坏）。**

### Slice D — 激活分组→路由（冻结 gatewayhttp 路由 call-site，非钱）
- `chat_completions_dispatch.go:prepareRoute`：把 `ident.UserGroup` 灌进 `RequestContext.UserGroup`。
- `chat_completions_attempt.go:selectPoolAccount`：把 user_group 灌进 `SelectionRequest.UserGroup`。
- `cmd/gateway/selector_wiring.go`：用真 `GroupPolicyGate`（替 AllowAllGate）。
- **冻结编辑（2 处路由 call-site，非钱）。需 Owner 认可窄解冻。激活后分组真生效。**
- 验证：集成测试 premium 用户只命中 premium 渠道；default 用户被 premium-only 渠道拒。

### Slice E — 激活配额闸→热路径（冻结 + 新机钱路径，最高风险）
**这是越红线段，需 codex + 新机协调。核心难点 = 双预留生命周期：**
- `billing.ClaimGate.Reserve()`（Tx1）建 claim；`quota.Service.Reserve()` 也建 quota reservation。两者都有 commit/release 生命周期。
- **关键设计决策（待 codex + 新机定）：配额闸放在 billing reserve 之前还是之后？**
  - **方案 E-pre（Claude 倾向）**：配额闸**先于** billing.ClaimGate.Reserve。配额 deny → 直接 429，**不建 billing claim**（无孤儿 claim）。代价：配额 reserve 自身若成功但后续 billing 失败，需释放 quota reservation。
  - **方案 E-post（测绘 agent 建议）**：billing reserve 后、selectPool 前插配额闸。配额 deny → 已建的 billing claim 成**孤儿**（reserved 未 settle）——除非新机 settler 能回收 reserved-never-settled claim。**这正是必须问新机的点。**
- `chat_completions_handler.go` ChatHandlerDeps 加 `QuotaGate` 字段（冻结）。
- `chat_completions_dispatch.go:reserveClaim` 插配额闸 + 早 deny 路径（冻结 + 钱）。
- `cmd/gateway/wiring.go` 实例化 quota.Service + 注入（共享）。
- **跨机协调**：我在 work/quota-subsystem 分支改 reserveClaim；新机在其分支可能也改同文件 → 合并冲突 + 钱语义冲突风险。需新机知会 + 合并时人对人协调。

## 钱路径安全不变量（Slice E 必守）
1. 配额 deny **绝不**导致 billing claim 泄漏（孤儿 reserved）或重复入账。
2. 配额闸**只读**评估 + 自己的 reservation，**绝不**碰 billing_events/payment_credits/净余额（与新机零耦合）。
3. 配额 fail-open 仅限「未配置订阅」；瞬时存储错 fail-closed（与 quota.Service 一致，避免超额放行偷钱反向）。
4. 顺序原子性：billing claim 与 quota reservation 要么都成立要么都不建/都释放——不能一个成立一个泄漏。

## 三镜子参考（#15，clean-room specifier）
- **sub2api**：**双闸**——订阅窗口闸在 auth 中间件（内存 USD 日/周/月 caps，惰性重置，异步 DB 维护）`sub2api@91da815:server/middleware/api_key_auth.go:133-208` + 缓存余额/RPM 闸 `sub2api@91da815:service/billing_cache_service.go:561-700`。账号按组绑定 + 调度器按组分桶 `scheduler_snapshot_service.go:640-680`。
- **new-api**：配额=单整数池 reserve/settle/refund 在 relay billing 层 `new-api@20d3e73:service/billing.go:19-66` + `billing_session.go:342-432`；(组,模型,渠道) 能力表筛渠道 `model/ability.go:17-164`。无窗口 cap 概念。
- **CLIProxyAPI**：单租户，**无**分组→路由、**无**入站配额闸（显式无）`CLIProxyAPI:internal/api/server.go:1521-1548`；其 quota 仅上游 429 冷却 `sdk/cliproxy/auth/types.go:124-149`。
- **HUAKAI delta（三维）**：架构=配额闸独立成层、与 auth/billing 分离（vs sub2api 塞 auth 中间件、new-api 塞 relay）；算法=日历月窗确定性边界重置（vs sub2api 惰性 elapsed 重置有漂移竞态）；生态=user_group 与 API key 解耦，可不换 key 改档。**parity 风险**：sub2api 是双闸（窗口 + RPM/余额），HUAKAI 配额闸目前只覆盖 cost_usd 窗，RPM/余额闸属新机——E 不能让单 ClaimGate 漏掉 sub2api 已有的 RPM 闸（否则功能缩水），需确认 RPM 归谁。

## 执行建议（待 Owner/codex 确认）
- **A/B/C/D 现在做**：纯我地盘 + 冻结路由（非钱），逐切片 opus 替补 review + commit。分组→路由今天能真生效。
- **E 暂缓**：双预留生命周期 + 孤儿 claim + RPM parity = 钱路径正确性风险，强烈建议等 codex 5/30 平行评估 + 新机协调 reserveClaim 编辑顺序后再动。否则可能引入丢钱/超发/孤儿 claim。
- 若 Owner 坚持 E 立刻做：选 E-pre（配额先于 billing，无孤儿 claim），最小附加编辑，opus-architect 先验，标注 codex retro + 新机合并协调。

## 验证
- A/B/C：单元 + 判别测试。D/E：集成测试真 PG（分组路由命中/拒；配额超额 429 + 不转发 + 无孤儿 claim + 无 billing_events 增量）。
- 收尾全量 `go test ./...`（含 OpenAPI 一致性——E 不加新路由，D 不加路由，无需补 spec）。

---

## 平行双草交叉综合（Claude 上文 ⨯ opus-architect 独立草，#10）

两份独立草案（互不可见）**高度收敛**，尤其在最危险的钱路径决策上一致。综合定案：

### 一致点（双方独立得出）
1. **R-SUB-WIRE-1（路由）低风险、纯我地盘 → 现在做**；**R-SUB-WIRE-2（配额闸→钱路径）高风险 → 分阶段、钱编辑放最后 + 新机协调**。
2. **配额闸放在 `billing.ClaimGate.Reserve` 之前（E-pre）** —— 双方独立同结论。理由：配额 deny 时**根本不建 billing claim**（零孤儿、零新机账本写、零清理义务）；便宜可逆的先跑；对新机钱段编辑最小（纯前插）。被否方案（配额在后）失败态：billing claim 已建→配额 deny→必须 Abort→Abort 可能失败→孤儿 reserved claim，重试风暴下累积 = 丢钱/账本不一致。
3. 新包都在我地盘；冻结 gatewayhttp 只改既有文件不加新文件；AllowAllGate 默认是「静默不生效」陷阱（测试必须判别）。
4. 跨机 reserveClaim/abort 站点冲突必须先与新机商定 seam 再写。

### architect 三个关键补强（采纳）
1. **S2 影子模式**：在硬闸前插一档——quota.Service 已有 `ModeObserve`（service.go:281），先**只记录决策不拦截**，用真流量验证 predicted-cost + 窗口数学 + fail-closed，再翻成硬拦。零拦真用户风险。**新增一档，我原草没有。**
2. **Release-on-every-abort 不变量（S0 头号风险）**：每条调 `Settler.Abort` 的错误路径必须**同时**调 `quota.Service.Release`，否则配额窗虚高到 lease 过期（5 分钟自愈但会误拒后续请求）。提议 `finalizeAbort(reason)` 单 seam 同时调两者，避免散改新机 abort 站点。quota reservation 有 5 分钟 lease 兜底自愈。
3. **RPM parity = 策略缺口非代码缺口**：quota.Service 已建 `MetricRequests` + `MetricConcurrency`（service.go:310/344），RPM 类 cap 只是没 provision 的策略行，不是代码缺；真 token-bucket 平滑是未来 delta。**非回退**（HUAKAI 从未发过 RPM 闸），进路标，不 scope-creep 进 S3。

### 最终切片序（综合两案，取 architect 的 S0/S1/S2/S3 更清晰分解）
- **S0**（我地盘）：identity plumbing —— auth.Identity + RequestContext + SelectionRequest 加 UserGroup 字段（纯附加）。
- **S1**（我地盘 + 共享 wiring）：R-SUB-WIRE-1 真 GroupPolicyGate 替 AllowAllGate。**待定实现点**：route→pool_group 解析放 Router 还是 GroupPolicyGate（architect 标记 S1 编码前需核 default_router.go + routes/pool_group 关系；listEligibleAccountsByPoolGroup 不按 user_group 筛，限制在 gate 或 route→pool_group 解析处做）。
- **S2**（我地盘 + 共享 wiring）：配额闸**影子模式**插热路径前段（只观测不拦），用 ModeObserve。
- **S3**（冻结 + 新机钱路径，**gated**）：翻硬拦 + E-pre 顺序 + finalizeAbort seam（Settler.Abort 处同释 quota）。**需 codex 5/30 平行 + 新机 seam 协调**（我无权进新机，盲改共享钱文件必撞合并 + 孤儿账本风险）。

### 执行决定
- **S0 + S1 + S2 现在做**（真分组路由 + 配额影子观测全落地，纯我地盘 + 冻结非钱 + 共享 wiring）。逐切片 opus 替补 review + commit + push。
- **S3 gated**：全接通的最后一跳,需新机 seam 协调（要 Owner 牵头，我无权进新机）+ codex 5/30 平行评估。**这是 sequencing 不是 scope 缩减**——S3 仍在计划内,只是把唯一不可逆的钱编辑放最后并置于协调之后。
- codex retro（本双草 + S0/S1/S2 实现）待 5/30 23:15。
