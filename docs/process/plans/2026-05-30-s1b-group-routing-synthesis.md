# S1b 分组路由激活 — 双草交叉综合 + 终稿(R-SUB-WIRE-1 第二阶段)

状态:设计锁定,待实现。日期 2026-05-30。
独立双草来源(CLAUDE.md #10,各自不看对方):
- Claude 草:`2026-05-30-s1b-group-routing-claude.md`(Workflow architect lane,wvccn8pko)
- Codex 草:`2026-05-30-s1b-group-routing-codex.md`(codex gpt-5.5/xhigh,bs666e14l;全文 `/home/codex/codex_s1b_out.txt`)
跨模型 retro(S0+S1a):codex gpt-5.5/xhigh,b71qr27yt,裁决 APPROVE-WITH-FOLLOWUP。

## 背景

S1a 已休眠落地(commit e07ed8d):`internal/subscriptionenforce.GroupPolicyGate`(实现 `poolrouter.Gate`)+ `PostgresRoutesRepo` 建好已测,但 `DefaultGateChain` 仍是 `AllowAllGate`,`SelectionRequest.UserGroup` 字段存在但无人灌。S0(ffe51eb)已把 `auth.Identity.UserGroup` 回填。S1b = 激活。

## 双草交叉结论

### Hoist 机制(每 Select 一次而非每候选)— 采 codex 方案
两草**都否决**:H3 repo TTL 缓存(跨请求失效/租户隔离/配置可见性问题)、"selector 入口硬编码 precheck 弃用 gate"(绕开 gate chain + routing reason)。
分歧:
- Claude 草:`SelectionRequest` 挂可变指针 memo,gate 首调填充。**问题**:memo 字段须 export 给外包 `subscriptionenforce` 写 → 破坏封装(Claude 自己也标了 R3 分层异味)。
- Codex 草:`GateChain.ForSelection(ctx, req) GateChain` 返回本次 Select 专用的局部链;实现 `SelectionGatePreparer` 的 gate 做一次准备(查库)后返回只读缓存 gate;**绝不写回 selector 实例**(多 goroutine 共享会竞态)。
**裁决:采 codex 的 ForSelection**。封装更干净(prepared gate 留在 pool/router 局部链,外包只实现接口),且 codex 抓到 Claude 漏的并发正确性点。两草都复用已测 gate(无功能缩水)。

### 空集语义(retro F1)— Owner 决策:白名单式拒绝
codex retro 挖出两计划都没覆盖的真缺口:现 gate.go:51 `len(allowed)==0 → 放行`,把「该档零规则」和「该档配了规则但本 model 没命中」混为一谈 → entitlement 泄漏(premium 只配 claude-*,请求 gemini 空集放行进未授权池)。
**参考项目对照(#15,已 #12 源码核实)**:
- new-api@20d3e73:`model/ability.go:106-141` `GetChannel` 在 group+model 无 ability 绑定时走 `else → return nil`(无可用渠道→拒);白名单语义,无"未配置档放行"兜底(每 group 路由性由 ability 行完全定义)。
- sub2api@91da8159:`channel_repo.go:371` `channel_groups` 多对多 + `api_key_repo.go:699-717` 的 `IsExclusive`/`ClaudeCodeOnly`/`ModelRoutingEnabled`;档位绑定渠道,独占档只能用绑定渠道。
**Owner 拍板(AskUserQuestion 2026-05-30):拒绝(白名单式)** —— 该档配了任何有效路由后,本 model 没命中任何规则 → **拒**;该档零有效路由 → 放行(兼容还没配分组路由的老租户)。= 两参考一致 + HUAKAI 兼容兜底 delta。

### 其余决策
- **fail-open(repo 出错)**:保留 S1a 决策(entitlement 非钱非安全闸,DB 抖动不拒付费用户)+ 加 `group_policy_fail_open_total` 计数 + WARN(R4:持续故障必须可告警,否则静默越档)。
- **拒绝的对外语义 503 vs 403**:**延后**(codex 明确 scope 外)。拒走 `ErrNoEligibleAccount`→handler 映射 503 `pool_no_capacity`(dispatch.go:240-244),不泄漏但语义不精确;403 + 升级提示作 follow-up(碰冻结 error 映射,单独切片)。

## retro 工程修(非 Owner 决策,折进实现)
- **F1 实现**:repo 一次查询返回「该档是否有任何有效路由」+「命中本 model 的 pool 集」;gate 据此实现白名单(零路由放行 / 有路由+本 model 未命中拒 / 命中且 PoolGroupID∈集放行)。
- **F2**:repo JOIN `pool_groups` 校验目标池 `tenant_id/enabled/deleted_at`,防返回跨租户/已禁用/软删的目标 pool id。
- **F3**:强化 gate 单测 fake repo,断言 `tenantID/userGroup/model` 入参透传(防 gate 传错参单测仍绿)。
- **F4**:集成测加 seed 软删 route + 指向禁用/跨租户 pool_group 的 route,守 JOIN 与软删谓词。

## 已核对通过(retro)
- ffe51eb auth/sqlc 一致;`ModelPatternMatches` 对 `*`/空/`prefix*`/精确符合声明(中段 `a*b` 当精确属 S3 admin CRUD 校验);冻结包纪律两提交均合规;subscriptionenforce 是新包。

## 关键插入点(已 recon 核实,file:line)
- gate 调用:`default_selector.go:144`(filter)+ `:173`(tryLayer);PASR `pasr.go:208`(段内)+ `:335`(HRW 全 ring),底层 `allowAccount:378`/`healthStatus:385` 读 `p.gates`。
- `GateChain` 结构 + `ordered()` nil 补齐:`gates.go:55-125`;`Gate` 接口 `:23-25`;`DefaultGateChain` GroupPolicy=AllowAllGate `:67-72`。
- 接线点:`selector_wiring.go:62-72`(`gates := pool.DefaultGateChain()` 后设 `gates.GroupPolicy`;`pgPool` 在 `:49` 在 scope);同一 `gates` 复用于 default selector + actual PASR + shadow PASR(`:66-72/103-109/119-125`)→ 接一次覆盖 5 模式。
- 灌 UserGroup:冻结 `chat_completions_dispatch.go:226-239` 的 `pool.SelectionRequest{...}` 加一行 `UserGroup: ex.ident.UserGroup`(`ex.ident` 是 auth.Identity,已有 UserGroup;existing-file wire 编辑,非新文件)。
- 传播测试 helper:`chat_completions_dispatch_test.go` 的 `recordingSelectionRequestSelector`。

## 实现拆分(一 commit 一模块;每 commit 走 codex per-commit review ≤2 轮,S0/S1 阻塞)
- **Commit A — pool/router ForSelection 管线(行为保持)**:gates.go 加 `SelectionGatePreparer` 接口 + `GateChain.ForSelection`;default_selector 在 Select 算一次 scoped 链穿 filter/trySticky/tryLayer;PASR 同样穿 allowAccount/healthStatus(Select+scheduleHRWFullRing+scheduleNoSegment)。AllowAllGate 不实现 preparer → ForSelection 不改任何东西 → 全程行为保持。判别测:fake preparer gate 计数,断言每 Select PrepareForSelection 恰 1 次、prepared.Allow 每候选 1 次(mutation:仍用 s.gates → Prepare 0 次红;每候选 prepare → 计数>1 红)。
- **Commit B — subscriptionenforce 白名单 + JOIN + 测**:gate 实现 `PrepareForSelection`(查一次→只读缓存 gate);repo 改返回 `{GroupConfigured bool, Allowed set}`(F1)+ JOIN pool_groups(F2);gate 决策落白名单语义;F3 强化单测 + F4 集成测加软删/禁用/跨租户 fixture。仍休眠(默认链 AllowAllGate)。
- **Commit C — wiring 激活**:selector_wiring 接 `subscriptionenforce.NewGroupPolicyGate(NewPostgresRoutesRepo(pgPool))`(SHARED,与新机串行)+ fail-open metric/log 注入;冻结 dispatch 灌 `UserGroup`(1 行);集成测 premium→premium 池、default→premium 池拒(ErrNoEligibleAccount);跑全量 `go test ./...`(OpenAPI 一致性闸只全量抓)。

## 风险
- R1 SHARED `selector_wiring.go` 与新机撞车 → 编辑前查 git 状态 + 串行,改动小(几行)。
- R2 冻结包诱惑 → UserGroup 只在既有 struct literal 加一行,禁加 helper/新文件。
- R3 ForSelection 穿线碰两个并发敏感 selector → 行为保持 + 判别测 + 逐 commit review。
- R4 fail-open 掩盖真故障 → 必须 metric 非仅 debug log。
- R5 全量套件闸(OpenAPI 一致性)→ 本切片不加路由,低风险但仍跑全量。
- R6 memo 后 gate 链仍每候选迭代 9 gate ×2(filter+tryLayer)→ 既有、scope 外,不在 S1b 修。
