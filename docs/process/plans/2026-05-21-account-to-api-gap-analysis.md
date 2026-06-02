# 账号转 API 链路 — 功能完整性 gap 分析

- 日期: 2026-05-21
- 触发: Owner「感觉少了很多东西」,要求按步骤审账号转 API 链路
- 输入: 3 份 specifier-lane 调研 —— CLIProxyAPI 15 步深挖 / sub2api+new-api+one-api 交叉参照 / HUAKAI 自审
- 状态: 调研完成,**待 Owner 定方向**

> 【2026-06-02 已更新】本文件的“待 Owner 定方向”“跨池 Router 是 L0 桩”
> “AttemptSeq:1 写死”“非流式 anthropic 是 501 桩”等是 2026-05-21 历史。
> 后续 Owner 已锁定 C 方向：Go `gatewayhttp` 作生产大脑，Rust 重定位为高性能 +
> 强伪装出站 sidecar；②⑥ 单请求 retry/failover/跨池已由 `router.DefaultRouter`
> 多 attempt plan + `chatExecution` attempt loop 接通；③ Anthropic Messages
> raw buffered 已有“不再 501”测试与实现；transport mimicry / sidecar 已由
> `transport.Factory` 和 `SidecarSocketPath` 接线。`ApplyMimicryPlan` 本轮全仓搜索
> 未观察到非测试 caller，因此应用层 body cloaking 不标为已闭；以下为历史。

## 0. 最关键发现:HUAKAI 有两套并行数据面

`grep RouteQuery|RouteService|AttemptReport backend/` = 零命中;`backend/` 内无任何 gRPC server;`route.proto` 只存在于 `exploratory/rust-core-gateway/`。

- **Go `gatewayhttp` 管线** —— 当前**唯一在生产、真正转账号转 API** 的链路。`/v1/chat/completions`、`/v1/messages`、`/v1/responses` 全挂在它上。主干覆盖度高。
- **Rust `core_gateway`** —— CP/DP 分离架构的探索数据面。它通过 `route.proto` 的 `RouteService` gRPC 调一个 Go 控制面;**但那个控制面在 `backend/` 根本没有实现**。Rust DP 现在对着不存在的控制面写代码,靠 `mock_control_plane.rs` 跑测试,**服务不了任何真实流量**。

C 档四波(性能/防打爆/伪装/Docker)一直在加固 Rust `core_gateway` —— 即一个尚未接通控制面、且功能很薄的数据面。

## 1. 15 步链路 gap 表

| # | 步骤 | 标准做法(参考项目) | HUAKAI Go 生产管线 | HUAKAI Rust DP | 缺口 |
|---|---|---|---|---|---|
| 1 | 请求接收+协议识别 | path 路由 + 少量 UA 分流 | 完整: 3 端点 + adapter registry | 部分: 2 协议硬编码 path,无 Gemini | 小 |
| 2 | 入站鉴权+对调用方限流 | 鉴权遍历 provider;**调用方限流连 CLIProxyAPI 都没有**;sub2api 有幂等键+并发槽 | 鉴权完整;**对调用方 QPS/并发限流缺通用实现** | 无 | **中: 调用方限流** |
| 3 | 请求解析+模型映射 | 完整;new-api 还有参数/header 覆盖、强制 system | 完整(Registry.ResolveModel) | 只把 requested_model 传 CP | 小 |
| 4 | 账号选择 | 优先级+亲和;sub2api 多维评分+TopK 加权随机 | 池内 9-gate + PASR 很强;**跨池 Router 是 L0 桩** | 全在 CP(account_planner 只发 query) | **中: 跨池多候选编排** |
| 5 | 凭据+OAuth token 刷新 | 后台刷新 + 热路径失效检测 + 刷新去重 | 凭据获取完整;**刷新只后台定时器,热路径不查 token 过期** | CP 职责 | **中: 热路径即时刷新** |
| 6 | 请求格式翻译(跨协议) | executor 内翻译,任意协议矩阵 | 做(HCSF graph,跨协议);有损翻译记 loss | **完全不做(纯字节透传)** | **Rust DP 无** |
| 7 | 应用层 cloaking | CLIProxyAPI 极重: system 重建/计费头伪造/假 user-id/工具名改写/cache_control/body 签名/整套 header 伪造 | **库代码全有,但 `ApplyMimicryPlan` 零调用 = 死代码**;真实出站 body 原样转发 | 无 | **大: 死代码,与反掺水定位矛盾** |
| 8 | 传输层伪装 | CLIProxyAPI uTLS 仅 Anthropic + 标准库 h2(非精确) | transport.Factory 是壳,默认 standard 不伪装 | L1 TLS 已验证;L2 HTTP/2 待做 | Go 管线不伪装;Rust 是强项但 L2 未完 |
| 9 | 上游转发 | 完整 | 完整(UpstreamDispatcher + 代理) | 完整(forward_inner) | 无 |
| 10 | 响应流式回传 | 完整 | 完整(StreamForwarder) | 完整(relay) | 无 |
| 11 | 响应格式翻译 | 完整 | 流式真;**非流式 anthropic 是 501 桩** | 不做 | **中: Go 非流式 anthropic 桩** |
| 12 | usage 抽取+计费 | CLIProxyAPI 只内存 usage 无计费;new-api/one-api 预扣+后扣 | 完整重模块(ClaimGate 幂等 + settle 策略) | attempt_reporter 上报给不存在的 CP | Rust 端无效 |
| 13 | 错误+重试+账号 failover | **参考项目普遍做单请求内重试换账号**(sub2api 已实证, 见 §4);sub2api 的 failover 状态机跨尝试排除已失败账号 | **L0 桩,AttemptSeq:1 写死,上游一错就甩客户端,无单请求内重试** | forward_planned 单次,previous_attempts 空 | **大: 两套都缺** |
| 14 | 账号健康/冷却/配额/429 退避 | sub2api 6 态最细;CLIProxyAPI 按状态码分档冷却+指数退避 | channelhealth 完整状态机(active/degraded/cooling/ramping/...) | CP 职责 | 无大缺口(Go 强) |
| 15 | 记录/审计/可观测 | CLIProxyAPI 靠日志文件,无 metrics | 完整: 信任链审计+签名+六跳链+metrics | tracing + metrics | 无大缺口(Go 是 HUAKAI 优势) |

## 2. 缺口汇总

### Go 生产管线的真实缺口(按严重度)
1. **应用层 cloaking = 死代码**(步骤 7)。`mimicry_compose.go`/`system_rewrite.go`/工具名改写/cache_control 全实现,`ApplyMimicryPlan` 无任何非测试 caller。对 HUAKAI 反掺水/伪装定位是明显短板。
2. **单请求内重试 + 账号 failover = 没有**(步骤 13)。跨池 Router 是 L0 桩。参考项目普遍做这个(见 §4)。
3. **非流式 anthropic_messages 响应翻译 = 501 桩**(步骤 11)。
4. **OAuth token 不在请求热路径刷新**(步骤 5)。定时器赶不上就拿过期 token 打上游。
5. **无对调用方限流**(步骤 2)。无按 tenant/api-key 的 QPS/并发上限。
6. **跨池多候选编排 = 桩**(步骤 4)。池内选号很强,跨池没做(与 #2 同根)。

### Rust core_gateway 的缺口
- 控制面(RouteService gRPC server)不存在 —— 整套数据面未接通。
- 链路很薄:步骤 3/4/5/6/7/11/13/14 基本不做或全甩给不存在的 CP;只实质做 1(部分)/9/10/12(部分)/15。它是个转发外壳。

### 参考项目实证、值得纳入的「容易漏」步骤
幂等键防重复扣费;请求体预读以便重试重放;流式已开始则禁 failover;账号 slot 排队而非立即 503;429 按响应头精确退避;主动健康探测(不只被动降级);计费与转发的原子性对账。

## 3. 战略岔路(待 Owner 定 + 按 CLAUDE.md #10 与 codex 平行讨论)

- **路 A —— 把 Rust 推到完整核心链路**: 建 Go RouteService 控制面(R-E)+ 在 Rust DP 或 CP 补齐翻译/cloaking/重试 failover 等步骤 + 完成 C 档加固。工作量极大(数月级)。期间 Go 管线仍是事实生产。
- **路 B —— Go 管线作核心,补它的洞,Rust DP 降级/暂停**: Go 管线已覆盖 13/15 步,补 6 个缺口是周级工作。代价: 暂停 Rust 投入。
- **路 C —— 分工重定位(推荐方向待平行讨论)**: Go 管线 = 账号转 API 大脑(选号/凭据/翻译/cloaking/计费/重试),继续补它的洞;Rust = 只做「高性能 + 强伪装的出站传输层」(L1+L2 是连 CLIProxyAPI 都没做好的真差异化),不强求 Rust 做完整 15 步。对应记忆 [[project_r3_rust_sidecar]] 的 sidecar 定位。

## 4. 参考项目源码引证(CLAUDE.md #12)

§1 表格「标准做法(参考项目)」列点名的参考项目行为,逐条 file:line 引证如下。Explore specifier lane 采集于 2026-05-21;Claude 抽查了 sub2api 的 failover 状态机、CLIProxyAPI 的 cloaking 应用函数两条,读源码核对一致。

| 步 | 论断 | 引证 |
|---|---|---|
| 2 | 调用方限流:幂等键防重 | `Wei-Shaw/sub2api@91da8159:backend/internal/repository/idempotency_repo.go:21-50`(INSERT...ON CONFLICT 原子防重) |
| 2 | 调用方并发槽位(等待/排队) | `Wei-Shaw/sub2api@91da8159:backend/internal/service/gateway_service.go:1483-1516`(槽位失败返回 wait plan) |
| 3 | 模型映射 + 参数/header 覆盖 | `Calcium-Ion/new-api@20d3e73:model/channel.go:42,51`(映射 + header 覆盖字段) |
| 3 | 强制系统提示 | `Calcium-Ion/new-api@20d3e73:relay/chat_completions_via_responses.go:48-52`(系统提示覆盖标志) |
| 3 | JSON 模型映射表 | `songquanpeng/one-api@8df4a26:model/channel.go:37,114-119`(JSON 字典解析) |
| 4 | 多维感知分层调度(粘性/路由/并发) | `Wei-Shaw/sub2api@91da8159:backend/internal/service/gateway_service.go:1407-1650`(三层调度) |
| 4 | 会话限制检查 + 路由过滤 | `Wei-Shaw/sub2api@91da8159:backend/internal/service/gateway_service.go:1486-1510` |
| 5 | (反例)CLIProxyAPI 无热路径令牌刷新 | `router-for-me/CLIProxyAPI@21fad9db:internal/runtime/executor/claude_executor.go:88-112`(请求时直接用存储密钥)—— 见下「修正」 |
| 7 | 应用层 cloaking(系统提示重构/假 user-id/敏感词混淆) | `router-for-me/CLIProxyAPI@21fad9db:internal/runtime/executor/claude_executor.go:1790-1844`(cloaking 三变换) |
| 7 | 系统提示重构注入 Claude Code 系统块 | `router-for-me/CLIProxyAPI@21fad9db:internal/runtime/executor/claude_executor.go:1622-1670` |
| 7 | cache_control 三级注入(工具/系统/消息) | `router-for-me/CLIProxyAPI@21fad9db:internal/runtime/executor/claude_executor.go:1846-1870` |
| 7 | OAuth 工具名改写 | `router-for-me/CLIProxyAPI@21fad9db:internal/runtime/executor/claude_executor.go:47-75,1053-1084` |
| 8 | uTLS + Chrome 指纹仅 Anthropic 域 | `router-for-me/CLIProxyAPI@21fad9db:internal/runtime/executor/helps/utls_client.go:21-110` |
| 12 | 分段表达式计费(按 token 量分段求值) | `Calcium-Ion/new-api@20d3e73:service/tiered_settle.go:91-95` |
| 13 | 单请求内重试 + failover(状态机) | `Wei-Shaw/sub2api@91da8159:backend/internal/handler/failover_loop.go:43-125`(切换计数 / 失败账号集 / 同账号重试计数) |
| 13 | 同账号重试 + 临时封禁 | `Wei-Shaw/sub2api@91da8159:backend/internal/handler/failover_loop.go:80-97`(同账号可重试错误 → 用尽后临时封禁) |
| 14 | 账号健康(会话/槽位/并发隐式状态) | `Wei-Shaw/sub2api@91da8159:backend/internal/service/gateway_service.go:1470-1520` |

**修正(步骤 5)**:§1 表格步骤 5 的「标准做法」原写「后台刷新 + 热路径失效检测 + 刷新去重」。引证核实发现 CLIProxyAPI **不做**热路径令牌刷新(请求时直接用存储密钥),引证采集也未在其它参考项目找到热路径刷新的实证。因此「热路径即时刷新」并非参考项目普遍标准 —— HUAKAI 的洞 ④ 仍是真缺口(过期 token → 401 是真 bug),但定位应为 **HUAKAI 的升级项**,而非「落后于参考项目」。

---
本分析 lane: reviewer/synthesizer — Claude 综合 3 份 specifier-lane 调研 agent 报告 + 1 份 Explore specifier lane 引证采集(2026-05-21)。参考项目论断的 file:line 引证见 §4。UTC 2026-05-21
