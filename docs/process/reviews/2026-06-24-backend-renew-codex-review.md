# 后端 renew 代码质量与架构审查（Codex，2026-06-24）

| 项 | 内容 |
| --- | --- |
| Owner 指令 | “根据我给你的文档继续刚刚未完成的renew” / “写一个文档给我” |
| 审查类型 | 代码质量、架构刷新、包纪律、死代码、重复、复杂度、测试质量、重构债务 |
| 审查边界 | 以 `backend/` Go 生产数据面、`exploratory/rust-core-gateway/merged/crates/tls-sidecar` parked Rust sidecar、`backend/internal/transport/mimicry` Go 桥为主 |
| 不在范围 | 前端、纯安全专项、另一个目标的计划文件 |
| 输出性质 | 源码证据审查记录；未改生产代码 |
| 测试状态 | 未运行测试；本轮为读码审查与证据复核 |

## 结论

本轮 renew 审查已经结束，未发现本专项范围内可直接定为 S0 的确定性资源耗尽、必现不可用或纯 money-loss 级问题。

核心判断：当前最大风险不是某个单点 bug，而是“检查体系本身不够可信”叠加“多个核心包已超出可维护预算”。优先级应先修 CI / quality-gate / codebudget 这三类门禁，再拆 `gatewayhttp`、`payment`、`cmd/gateway`，随后处理 HCSF guard、credential scan、provider session 占位测试、sidecar 边界。

## S0

未发现本次代码质量专项范围内可直接定为 S0 的确定性问题。

纯安全类问题，如跨租户 IDOR、密钥泄露、资金 S0 等，应转 security 专项，不在本文展开。

## S1

### CI 假绿：`integration_pg` 钱路与跨租户测试没有在主 CI 编译运行

**证据**

- `/home/ubuntu/HUAKAI/.github/workflows/backend-ci.yml:71` 只跑 `go test -race -count=1 -timeout 5m ./...`
- `/home/ubuntu/HUAKAI/.github/workflows/backend-ci.yml:74` 只注入 `HUAKAI_TEST_DATABASE_URL`
- 当前测试文件中，读取 `HUAKAI_DATABASE_URL` 的 `*_test.go` 文件为 74 个；读取 `HUAKAI_TEST_DATABASE_URL` 的 `*_test.go` 文件为 1 个

**问题**

大量带 `integration_pg` 的钱路、退款、配额、跨租户测试没有在主 CI 带 tag 编译运行；同时 CI 注入的 DB env 名与多数测试读取的 env 名不一致，容易造成“看似有数据库，实际跳过”的假绿。

**修法**

新增独立 CI job：

- `go test -tags=integration_pg -race -count=1 -timeout 10m ./...`
- 同时设置 `HUAKAI_DATABASE_URL` 与 `HUAKAI_TEST_DATABASE_URL`，或统一测试 helper fallback
- 对 money/security/quota 相关 integration 测试禁止静默 `t.Skip`，没有 DB 时让专门 job fail loud

### 质量门允许 `--update` 洗 baseline

**证据**

- `/home/ubuntu/HUAKAI/backend/scripts/quality-gate.sh:16` 在传入 `--update` 时直接重写 staticcheck 与 deadcode baseline
- `/home/ubuntu/HUAKAI/backend/scripts/staticcheck-baseline.txt` 当前 93 条
- `/home/ubuntu/HUAKAI/backend/scripts/deadcode-baseline.txt` 当前 787 条

**问题**

当前 quality gate 只比较“新增 findings”，但脚本本身提供无约束的 baseline 重写入口。若 PR 用 `scripts/quality-gate.sh --update` 代替修复，就会把债务洗进基线，门禁失去约束力。

**修法**

- CI 禁止 `--update` 路径
- baseline 只允许减少；任何增加必须单独 Owner 批准，并在 `docs/process/reviews/DEFERRED-*.md` 记录理由、范围和后续清理计划
- 增加脚本自检：检测 baseline 行数增长时直接失败，除非显式设置仅限本地的重写 env 且不在 CI 中生效

### `cmd/gateway` 是 codebudget 盲区

**证据**

- `/home/ubuntu/HUAKAI/backend/internal/codebudget/budget_test.go:42` 固定扫描 `internal/`
- `/home/ubuntu/HUAKAI/backend/cmd/gateway/wiring.go` 1792 行
- `/home/ubuntu/HUAKAI/backend/cmd/gateway/routes.go` 1095 行
- `/home/ubuntu/HUAKAI/backend/cmd/gateway/wiring.go:293` `buildTransportFactory` 等生产装配逻辑仍在 `cmd/`

**问题**

`cmd/gateway` 不受 codebudget 管，启动门、路由装配、runtime wiring 可以持续膨胀并规避“一个包/文件一个职责”的约束。`cmd` 已经不再只是 thin startup。

**修法**

- 将 `cmd/gateway` 纳入 codebudget，或增加专门的 `cmdbudget`
- 抽 `startupgate`：release mode、captcha、email、audit signer、credential key、session key 等启动门
- 抽 `runtimewiring`：DB pool、worker、settler、eventbus、transport factory 等装配
- 抽 `routebind`：admin/user/gateway route 分组绑定

### HCSF 轻量版本守门函数未接入生产 hot path

**证据**

- `/home/ubuntu/HUAKAI/backend/internal/proto/envelope_guard.go:36` 定义 `ValidateEnvelopeVersionGuard`
- `/home/ubuntu/HUAKAI/backend/scripts/deadcode-baseline.txt:508` 仍列为 `unreachable func: ValidateEnvelopeVersionGuard`
- `/home/ubuntu/HUAKAI/backend/internal/gateway/upstream_dispatcher_hcsf.go:73` `DispatchHCSF` 只做 nil / adapter / transport / family 检查
- `/home/ubuntu/HUAKAI/backend/internal/gatewayhttp/chat_completions_dispatch.go:777` 直接调用 `dispatcher.DispatchHCSF`

**问题**

源码注释明确说该函数用于 forwarder / dispatcher / SSE adapter 等 hot path 的 envelope 边界，但生产路径没有调用。测试里调用 guard 不能证明生产边界被守住。

**修法**

- 在 `DispatchHCSF` 入口调用 `proto.ValidateEnvelopeVersionGuard`
- 在 SSE adapter / protocol adapter 的 envelope 边界增加同一轻量 guard
- 将 deadcode baseline 中该项删除，新增生产调用测试：构造 `Version=""` 的 envelope，断言 dispatcher fail closed

### `gatewayhttp` 继续违反包职责与预算

**证据**

- `backend/internal/gatewayhttp` 当前 33 个非测试 Go 文件
- `backend/internal/gatewayhttp` 当前约 13599 行非测试 Go 代码
- 同包混居 chat relay、admin pool、admin credential、billing settings、auth、session、voucher、invitation、audit verify 等功能面

**问题**

该包已经远超 6000 行 / 20 文件预算，且职责混杂。后续任何加性编辑都会继续扩大耦合面，让 handler、auth、admin、billing 边界更难审计。

**修法**

按功能域拆包：

- `adminpoolhttp`
- `admincredhttp`
- `adminbillinghttp`
- `authhttp`
- `sessionhttp`
- `voucherhttp`
- `invitationhttp`
- `auditverifyhttp`

`gatewayhttp` 仅保留 chat-completions 中继主链路与必要的共享类型，不能再接纳新 admin / auth 功能。

### 凭据存储扫描逻辑重复，字段漂移风险真实存在

**证据**

- `/home/ubuntu/HUAKAI/backend/internal/credentialstore/postgres_store.go:315` `Create` 手写 RETURNING 列与 Scan
- `/home/ubuntu/HUAKAI/backend/internal/credentialstore/postgres_store.go:802` `scanRecordForRefresh`
- `/home/ubuntu/HUAKAI/backend/internal/credentialstore/postgres_store.go:1043` `scanRecord`
- `/home/ubuntu/HUAKAI/backend/internal/credentialstore/postgres_store.go:1072` `scanRecordWithCount`

**问题**

同一 credential 记录有多套手写列顺序和 scan 顺序，且 Create / Rotate / Resolve / Refresh 关注字段并不完全一致。新增外部账号、刷新 lead、失败状态等字段时，漏改任一 scanner 都可能造成 metadata 丢失或字段错位。

**修法**

- 抽 `credentialRecordColumns`
- 抽基础 `scanCredentialRecord(rowScanner)`，统一时间字段转换
- `scanRecordWithCount` 只在基础列后追加 `row_count`
- `scanRecordForRefresh` 只在基础列后追加 refresh-only 字段
- 给 Create / Rotate / ResolveActive 增加 metadata parity 测试，特别断言 `ExternalAccountID` / `ExternalAccountEmail` / `RefreshLeadSeconds`

## S2

### `payment` 包与 `store_postgres.go` 超预算，且 Taobao provider 与 DB CHECK 漂移

**证据**

- `backend/internal/payment` 当前 24 个非测试 Go 文件，约 5590 行
- `/home/ubuntu/HUAKAI/backend/internal/payment/store_postgres.go` 969 行
- `/home/ubuntu/HUAKAI/backend/internal/payment/types.go:48` 定义 `ProviderTaobao = "taobao"`
- `/home/ubuntu/HUAKAI/backend/internal/payment/provider.go:87` 起实现 Taobao / 闲鱼 manual-redirect provider
- `/home/ubuntu/HUAKAI/backend/sql/migrations/0071_payment_p1.up.sql:26` `provider_kind` CHECK 只允许 `manual`、`test`、`hmac`

**问题**

`payment` 已接近并超过职责预算，同时 runtime provider kind 与 DB CHECK 约束没有同步。若生产创建 `provider_kind=taobao` 订单，数据库层会拒绝；如果上层绕成 `manual`，则审计与 provider 语义会分叉。

**修法**

- 拆 `paymentorder`、`paymentwebhook`、`paymentreward`、`paymentadmin`、`paymentrefund`
- 补迁移显式允许 `taobao`，或保持 Taobao 仅为 `manual` provider 的 checkout metadata，不落独立 `provider_kind`
- 增加真实 PG 测试：启用 Taobao 配置后 `CreateOrder` 能落库，且 manual confirm 路径不触发 webhook verifier

### Provider session 失败路径是 `t.Skip` 占位，不是覆盖

**证据**

- `/home/ubuntu/HUAKAI/backend/internal/provider/windsurf/windsurf_session_test.go:110`
- `/home/ubuntu/HUAKAI/backend/internal/provider/antigravity/antigravity_session_test.go:109`
- `/home/ubuntu/HUAKAI/backend/internal/provider/copilot/copilot_session_test.go:113`
- `/home/ubuntu/HUAKAI/backend/internal/provider/gemini/gemini_advanced_session_test.go:141`
- `/home/ubuntu/HUAKAI/backend/internal/provider/kiro/kiro_session_test.go:110`
- `/home/ubuntu/HUAKAI/backend/internal/provider/cursor/cursor_session_test.go:113`

**问题**

多 vendor 的 401 reauth、5xx 分类、DLQ retry、账户挂起/不挂起语义以 `t.Skip` 占位存在，容易被误读为“有测试函数就有覆盖”。

**修法**

- 用 `httptest.Server` 模拟 401、403、429、5xx、malformed body
- 明确断言错误分类、是否触发 credential refresh、是否改变 provider account state、是否进入 DLQ/retry
- 保留 TODO 时必须在测试名或文档中标为“未覆盖”，不能作为 coverage 证据

### Rust H2 sidecar 仍在可编译和可激活路径里

**证据**

- `/home/ubuntu/HUAKAI/exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/main.rs:3` 引入 `h2_bridge`
- `/home/ubuntu/HUAKAI/exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/main.rs:4` 引入 `h2_settings`
- `/home/ubuntu/HUAKAI/exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/connect.rs:46` 存在 `ConnectedUpstream::H2`
- `/home/ubuntu/HUAKAI/exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/connect.rs:118` ALPN 为 `h2` 时进入 H2 handshake
- `/home/ubuntu/HUAKAI/exploratory/rust-core-gateway/merged/crates/tls-sidecar/Cargo.toml:14` 仍依赖 `h2`

**问题**

出口决策是不做 H2，Rust sidecar park 不部署。但 H2 不只是残留文件，而是仍被主模块引入、profile 解析、连接分支和测试覆盖。这会让未来误启 sidecar 时出现与当前产品决策不一致的协议路径。

**修法**

- 删除 `h2_bridge.rs` / `h2_settings.rs` 与相关 profile parser
- 或使用明确的 `experimental-h2-sidecar` feature gate，默认不编译
- parked sidecar 保留 H1/raw tunnel 能力即可，不补 H2 SETTINGS

### Go sidecar 控制帧缺默认 ACK deadline，写帧不是全写语义

**证据**

- `/home/ubuntu/HUAKAI/backend/internal/transport/mimicry/sidecar_client.go:64` 只从 request context 继承 deadline
- `/home/ubuntu/HUAKAI/backend/internal/transport/mimicry/sidecar_client.go:78` 读取 ACK
- `/home/ubuntu/HUAKAI/backend/internal/transport/mimicry/sidecar_client.go:134` 写 prefix 只调用一次 `conn.Write`
- `/home/ubuntu/HUAKAI/backend/internal/transport/mimicry/sidecar_client.go:137` 写 body 只调用一次 `conn.Write`

**问题**

sidecar 默认关闭，所以不是当前主链路 S1。但一旦运维显式配置 sidecar socket，如果 request context 没有 deadline，ACK 等待可能长期挂住；短写时也没有补写剩余 frame。

**修法**

- 给 sidecar 控制握手加默认 5-10 秒 deadline
- `writeSidecarFrame` 改为 `io.WriteFull` 等价语义
- 增加短写、半关闭、无 ACK、超长 frame、坏 JSON ACK 的判别式测试

### 静态检查 baseline 中仍有真实弱测试和旧实现

**证据**

- `/home/ubuntu/HUAKAI/backend/scripts/staticcheck-baseline.txt` 当前 93 条
- `/home/ubuntu/HUAKAI/backend/scripts/deadcode-baseline.txt` 当前 787 条
- `/home/ubuntu/HUAKAI/backend/scripts/deadcode-baseline.txt:508` 仍包含生产应接线的 `ValidateEnvelopeVersionGuard`

**问题**

baseline 不只是“噪音列表”，其中混有真实旧实现、未接线实现、弱测试和协议边界未接线问题。长期祖父豁免会让维护者误以为“当前债务已被接受，不需要清”。

**修法**

- 将 baseline 分成 `known-debt` 与 `intentional-deadcode`
- 每一类 deadcode 必须有 owner、原因和清理窗口
- 对 money/proto/auth/transport 相关 deadcode 不允许无限期祖父豁免

## 重构优先级

| 优先级 | 工作 | ROI | 阻塞性 |
| --- | --- | --- | --- |
| 1 | 修 CI `integration_pg` job 与 DB env 名 | 让钱路、配额、跨租户测试真实运行 | 高 |
| 2 | 锁死 quality-gate baseline 只能下降 | 防止 staticcheck/deadcode 债务被洗白 | 高 |
| 3 | 把 `cmd/gateway` 纳入预算并拆 wiring/routes | 关掉结构债逃逸口 | 高 |
| 4 | 接入 HCSF `ValidateEnvelopeVersionGuard` | 守住协议边界不变量 | 高 |
| 5 | 拆 `gatewayhttp` | 最大 god package，降低后续 PR 风险 | 高 |
| 6 | 拆 `payment` 并修 Taobao provider_kind 漂移 | money path 降风险 | 中高 |
| 7 | 合并 credentialstore scan helper | 防字段错位与 metadata 丢失 | 中高 |
| 8 | 清 provider session `t.Skip` 占位测试 | 补真实失败路径 | 中 |
| 9 | 清 Rust H2 sidecar dead code | 对齐“不做 H2”出口决策 | 中 |
| 10 | 修 Go sidecar frame/deadline 边界 | 防显式启用 sidecar 后卡死 | 中 |

## 建议 PR 切分

1. **PR-1：测试门禁可信化**
   - 增加 `integration_pg` CI job
   - 统一 `HUAKAI_DATABASE_URL` / `HUAKAI_TEST_DATABASE_URL`
   - 禁止相关测试在 CI 静默 skip

2. **PR-2：质量门与预算门可信化**
   - 禁止 `quality-gate.sh --update` 在 CI 中使用
   - baseline 只能下降
   - 将 `cmd/gateway` 纳入预算

3. **PR-3：HCSF guard 接线**
   - dispatcher / adapter 边界调用 `ValidateEnvelopeVersionGuard`
   - 删除对应 deadcode baseline
   - 补坏 version 判别式测试

4. **PR-4：credentialstore 扫描器合并**
   - 抽统一列清单与 scanner
   - 补 Create / Rotate / Resolve / Refresh metadata parity 测试

5. **PR-5：`gatewayhttp` 第一阶段拆包**
   - 先迁 admin pool / admin credential
   - 再迁 auth / session / voucher
   - 每个迁移 PR 保持路由行为不变

6. **PR-6：`payment` 拆包与 Taobao 落库语义**
   - 明确 Taobao 是独立 provider kind 还是 manual metadata
   - 按选定语义补迁移和 PG 测试

## 风险说明

### 功能缩水

本文没有建议删除功能。对 H2 sidecar 的建议是按已拍板的“不做 H2”出口决策清理或 feature-gate 作废路径，不影响当前 Go uTLS + 强制 H1 的生产出口。

### Clean-room 风险

本文只依据 HUAKAI 当前源码与测试证据输出，没有读取或复制非 MIT 参考项目源码；没有引入上游实现结构、函数名或注释。

### 安全风险

本文不是 security 专项。涉及启动门、协议 guard、sidecar deadline、provider session failure 的发现属于代码质量与生产可靠性风险；如后续发现跨租户、密钥、资金 S0，应转 security 专项。

### 需要 Owner 确认

- Taobao / 闲鱼支付落库语义：使用独立 `provider_kind='taobao'`，还是保持 `manual` 并仅在 checkout metadata 中标识 marketplace
- Rust H2 sidecar：直接删除，还是保留为默认不编译的 experimental feature
- `gatewayhttp` 拆包顺序：建议先 admin，再 auth/session，最后 chat relay 内部拆分

## Owner 摘要

本轮做的是后端 renew 代码质量与架构审查落档：已把 CI 假绿、quality-gate baseline、`cmd/gateway` 预算盲区、HCSF guard 未接线、`gatewayhttp` / `payment` god package、credentialstore scan 重复、provider `t.Skip` 占位、Rust H2 sidecar 作废路径、Go sidecar frame/deadline 等核心发现写入本文。未改生产代码，未运行测试，没有功能缩水；clean-room 风险低，因为本文只记录 HUAKAI 当前源码证据。下一步建议先做 PR-1/PR-2，把测试门和质量门变可信，再进入拆包与重复扫描器重构。
