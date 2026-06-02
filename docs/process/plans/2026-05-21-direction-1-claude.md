# 方向 1 执行计划 — Go 管线作大脑 / Rust 作传输层(Claude 稿)

- 日期: 2026-05-21
- 方向: Owner 已锁定「方向 1」—— Go gatewayhttp 管线 = 账号转 API 大脑(补它的洞); Rust core_gateway = 重定位为高性能 + 强伪装的出站传输层
- 输入: `docs/process/plans/2026-05-21-account-to-api-gap-analysis.md`(15 步 gap 分析, 参考项目证据已在该文 + 3 份 specifier 报告引用)
- 平行纪律: 按 CLAUDE.md #10 独立起草, 未读 codex 平行稿(`2026-05-21-direction-1-codex.md`)
- 状态: 待与 codex 稿交叉对比 → surface Owner

> 【2026-06-02 已更新】本 Claude 独立稿是 2026-05-21 的历史输入。当前 C 方向已锁定并推进：
> ②⑥ retry/failover/跨池、③ Anthropic buffered、transport mimicry/sidecar
> 接线已落地；旧 `core_gateway` 控制面方向按 C 退役为 legacy。本文关于
> `ApplyMimicryPlan` “零 caller”的应用层 body cloaking 判断，本轮全仓搜索仍未发现非测试
> caller，因此不更新为已闭；以下为历史草案。

## 0. 一句话

Go 管线已覆盖 15 步里的 ~13 步、主干扎实, 差 6 个洞、都是周级补丁。Rust 不再追求做完整数据面(那要先建一个不存在的控制面, 数月级), 改做一件别人做不好的事: 字节级 TLS / HTTP2 伪装的出站传输。两者用一个**可降级**契约连接 —— 「可降级」就是 Owner「Go 和 Rust 交互不能出问题」的工程答案: Rust 这一跳坏了, Go 自动回退原生 transport, 网关照常服务。

## 1. Go 管线补 6 个洞

### 洞 ② + ⑥ — 单请求内重试 + 账号 failover + 跨池编排【P0, 最高优先】

- 现状: `internal/router` 是 L0 桩, handler 写死 `AttemptSeq:1`, 上游一错就把错误甩给客户端; 跨池只有桩。
- 思路: handler 外包一层 attempt 循环。预读并缓冲请求体(可重放); 每次 attempt 前由 Router 跨池选候选账号、排除已失败账号(FailoverState); dispatch 上游; 成功或「流已开始」即结束; 账号级失败(401/403/429/连接错/5xx)记录、降级该账号、重试下一候选; 客户端自身 4xx 不重试。
- 改哪些包: `internal/router`(实现真 Router + 跨池候选)、`internal/gatewayhttp`(handler attempt 循环 + AttemptSeq 递增 + previous_attempts)、账号池选择接口、`internal/gateway/forwarder`。
- 风险: ❶ 请求体缓冲的内存成本 —— 超阈值大体标记不可重试; ❷ 计费重复扣 —— ClaimGate 幂等键必须跨 attempt 稳定, 或计费只发生在胜出 attempt 之后; ❸ 流式响应一旦发首字节就不能再 failover(硬规则); ❹ 与洞 ④ 交叉: 401-token 过期应「刷新 + 重试同账号」而非「failover 换号」, 重试循环要能区分。
- 回归面: 全部流量。
- 估时: ~3 周 codex(本计划最大块)。
- 为什么最高优先: 所有参考项目都做这个; 不做 = HUAKAI 遇任何上游瞬时错误就丢请求, 不是一个能用的网关。

### 洞 ④ — OAuth token 热路径即时刷新【P0】

- 现状: 只有后台定时器刷新; 定时器赶不上 → 热路径拿过期 token 打上游 → 401。
- 思路: 凭据获取步骤加 token 过期检测; 过期 / 临近过期则就地刷新, 用 singleflight 去重(同账号并发请求只刷一次)。
- 改哪些包: OAuth token store / 凭据获取步骤、handler 凭据步骤。
- 风险: 就地刷新给热路径加延迟(仅刷新这一次); 刷新失败要有处理。
- 回归面: OAuth 账号流量。
- 估时: ~1 周 codex。
- 与洞 ② 的接口: 刷新失败 / 仍 401 才升级为账号 failover。

### 洞 ① — 应用层 cloaking 接线【P1, 但是差异化, 不能滑】

- 现状: `mimicry_compose.go` / `system_rewrite.go` / 工具名改写 / cache_control / `ApplyMimicryPlan` 全实现, 零 caller = 死代码; 真实出站 body 原样转发。
- 思路: 请求解析后、出站 dispatch 前调 `ApplyMimicryPlan`, 用按 vendor / 账号的 cloaking profile。
- 改哪些包: `internal/gatewayhttp` handler、`internal/gateway/forwarder`。
- 风险: cloaking 改出站 body, profile 不对会破坏上游兼容 → 必须按 vendor / 账号 feature flag, 分阶段开。
- 回归面: 全部 chat completions 流量(flag 关时零影响)。
- 估时: ~1 周 codex。
- 注: 这是 HUAKAI 反掺水 / 反封禁定位的应用层一半(另一半是 Rust 传输层伪装)。死代码状态与定位矛盾, 优先级虽列 P1 但不能无限后推。

### 洞 ③ — 非流式 anthropic_messages 响应翻译【P1】

- 现状: `chat_completions_handler` 对非流式 anthropic 响应是 501 桩(流式已真)。
- 思路: HCSF 翻译图加非流式分支 —— 解析完整 anthropic Messages 响应 JSON, 映射到目标协议; 有损字段记 loss。
- 改哪些包: `internal/proto` 翻译层、`internal/gatewayhttp` handler。
- 风险: 低-中, 翻译边界清晰。
- 回归面: 非流式 `/v1/messages`。
- 估时: ~1 周 codex。
- 可与洞 ① 并行(不同包, 另起 codex lane)。

### 洞 ⑤ — 对调用方限流【P2】

- 现状: 无按 tenant / api-key 的 QPS / 并发上限。
- 思路: 入站限流中间件, 按 tenant / api-key key, token-bucket / 滑窗, 限额可配、默认宽松或关(与 Rust C 档 P4「全局默认关」一致)。单实例内存版起步; 多实例需共享状态(Redis)—— 决策点见 §6。
- 改哪些包: `internal/gatewayhttp` 中间件、可能新 `internal/ratelimit`。
- 风险: 低-中; 分布式限流需共享状态。
- 回归面: 全部入站。
- 估时: ~3-4 天 codex。
- 注: 与 Rust P4 同主题不同层 —— P4 是进程级总闸, 本洞是按调用方的公平性。两层都做, 呼应 Owner 防打爆「两层都做」决策。

### 优先级汇总

| 洞 | 优先级 | 估时 | 可并行 |
|---|---|---|---|
| ②+⑥ 重试/failover/跨池 | P0 | ~3 周 | 是, 独立 lane |
| ④ token 热刷新 | P0 | ~1 周 | 与 ② 同 lane(交叉) |
| ① cloaking 接线 | P1 | ~1 周 | 是, 独立 lane |
| ③ 非流式 anthropic | P1 | ~1 周 | 与 ① 同 lane |
| ⑤ 调用方限流 | P2 | ~3-4 天 | 是 |

先做: ②+⑥+④(correctness 地基)。同时另起一条 lane 做 ①+③。⑤ 收尾。

## 2. Rust core_gateway 重定位为传输层

### 保留
- L1 TLS 伪装(BoringSSL backend, 见记忆 `project_l1_tls_boringssl`)—— 核心伪装价值。
- L2 HTTP/2 伪装(Wave C, 未做)—— 真差异化。
- `proxy_engine` 出站转发 + 连接池; `stream_pipeline` 流式 relay。
- C 档加固: P4 过载卸载、P6 超时 + 自研 serve loop + SIGTERM。
- redaction / metrics / tracing; mimicry profile / dispatch / adapter 机制。

### 退役 / 降级
- `account_planner` 对控制面的查询职责 —— 方向 1 里 Go 选号, Rust 收到的是「已选定目标」。
- `route.proto` `RouteService` / `route_client` —— 调一个不存在的 CP, 退役。
- `attempt_reporter` 向不存在 CP 的上报 —— 退役; 传输层统计改在响应里内联回传 Go。
- `mock_control_plane.rs` —— 随 CP 退役(注意: C 档测试依赖它, 退役要重写相关测试)。
- `heartbeat` 对 CP 的心跳 —— 退役。
- `listener` 作「终端用户入口」的角色 —— 变成「接收 Go 大脑的已备请求」, 调用方从终端用户变成可信内部组件。
- `drain` —— 由 CP 驱动改为本地 / Go 驱动。

### 最终形态
一个「会伪装的 forward proxy」sidecar: 暴露内部转发端点, 收 Go 的「已完全准备好的出站 HTTP 请求 + 伪装 profile id」, 应用 L1/L2 伪装、发上游、把响应原样流式回传。C 档加固(P4/P6)全部作用在这个 sidecar server 上。对应记忆 `project_r3_rust_sidecar` 的 sidecar 定位。

## 3. Go↔Rust 传输层契约【命脉 — Owner「交互不能出问题」】

### 核心安全设计 — 可降级(先讲这个, 因为它是答案)
Rust 传输层是 Go `transport.Factory` 的一个**可选 backend**, 与现有「standard」Go 原生 transport 并列, feature flag 控制。Rust 不可达 / 不健康 / 版本不符 → Go **自动回退 standard transport**(打 metric + 日志), 网关功能不受影响, 只损失这一跳的伪装增强。

这样 Go↔Rust 这条缝**永远不是关键路径上不可缺的一环**。它坏了, 网关照常服务。Owner「交互不能出问题」最稳的工程答案不是「把契约写得绝对不出错」(做不到), 而是「契约出错时系统自动绕过它」。

### 正常路径
Go 大脑选好号 / 取好凭据 / 翻译好 / app-层 cloaking 完成 → 把出站 HTTP 请求(目标 URL / 方法 / 头 / 体)+ 伪装 profile id 交给 Rust → Rust L1/L2 伪装 + 发上游 + 流式回传响应字节 → Go 的 `stream_pipeline` / `proto` 层抽 usage 计费。

### 传输介质 — 推荐 UDS 为主
- UDS(Unix Domain Socket): 同 pod co-located, 无 TCP 开销, 文件权限隔离。**主推**。
- localhost TCP: 跨容器不便共享 UDS volume 时的备选。
- 不推荐自定义 gRPC service: 负载本身就是 HTTP 请求/响应, 用 HTTP 语义传输最自然; 避免 proto schema 双向维护。Rust 现在已是 axum HTTP server, 复用成本最低。

### 9 个失败模式 × 应对
| # | 失败模式 | 应对 |
|---|---|---|
| ① | Rust 未启动/崩溃/不可达 | Go 健康探测 + 自动回退 standard transport(见上「可降级」) |
| ② | Go/Rust 版本不符 | 契约带版本号; 字段不匹配 → 回退 standard |
| ③ | 流式中途失败 | Rust 挂 → Go 看到 UDS 连接断 → 知道流不完整 → 报错误 attempt, **不**把半截响应当完整计费(Go 持计费决策权, 能算对) |
| ④ | 背压 | UDS 天然有 socket buffer 背压; Go 慢读 → Rust 慢写 → Rust 慢读上游, 一路传导(Rust P6 downstream-write-idle 已处理 Rust 内部段) |
| ⑤ | 超时归属 | Go 大脑持整体 deadline(它要做重试预算); Rust 持「单次出站」的连接/握手/首字节超时(P6 已有)。不重叠 |
| ⑥ | 错误分类 | Rust 回传结构化错误类别(TLS 握手失败 / 连接超时 / 上游 4xx/5xx), 映射到 Go 错误 taxonomy, 让洞 ② 重试/failover 知道该不该换号 |
| ⑦ | 取消传播 | 客户端断 → Go 取消 → 关 UDS 连接 → Rust 看到断 → 中止上游 + 释放连接(Rust P4/P6 guard 已覆盖) |
| ⑧ | 计费-传输原子性 | **Go 持计费权**: Rust 纯传输, 上游响应字节原样回传, Go 抽 usage。不需要「信任 Rust 的 usage 上报」—— 架构上消解 |
| ⑨ | 进程生命周期 | sidecar 由 Docker / 编排平台监督(独立容器, 同 pod), `restart: always`; Go 不负责拉起 Rust; Rust SIGTERM 优雅停机(P6)保证重启排空在途 |

### Docker 部署推荐
Rust sidecar 与 Go 同 pod 不同容器; UDS 经 shared volume; 不便时退 localhost TCP。Go 容器配 standard-transport 回退, 保证 sidecar 容器没起来也能服务。

## 4. C 档已做工作在方向 1 下的收敛

| 工作 | 收敛 |
|---|---|
| Wave A SSE 性能(memchr usage 抽取) | usage 计费权归 Go; Rust 端 usage 抽取降为**可观测性**用途(memchr 让它近零成本, 留着不亏)或后续删。perf 本身有效 |
| Wave B-P4 过载卸载 | **直接复用** —— sidecar 仍需进程级过载保护 |
| Wave B-P6 超时 + serve loop + SIGTERM | **直接复用** —— sidecar 是长驻 server, 需超时 + Docker 优雅停机 |
| Wave C L2 HTTP/2 伪装 | 不是「收敛」, 是 Rust 新角色的**核心主线**, 优先级抬到最高 |
| Wave D Docker | 复用, 但形态从「独立网关」变「sidecar 容器」 |

结论: P4/P6/D 直接复用; A 降为可观测; C 抬为 Rust 主线。已做的 C 档工作没有白做。

## 5. 分阶段 / 优先级 / 依赖 / 估时

- **Phase 0(进行中)**: Wave B-P6 收尾(SIGTERM)。
- **Phase 1 — Go 管线 correctness(6 个洞)**。Go 用自己的 transport, Rust 不在环里。完成即得一个正确、完整的网关。
  - 1a: ②+⑥+④(重试/failover/跨池/token 刷新)—— ~3 周。
  - 1b(与 1a 并行, 不同包另起 lane): ①+③(cloaking 接线 / 非流式 anthropic)—— ~2 周。
  - 1c: ⑤(调用方限流)—— ~3-4 天。
  - Phase 1 wall-clock ≈ 4 周(1a 是关键路径)。
- **Phase 2 — Rust 重定位 + Go↔Rust 缝**。
  - 2a: 退役 Rust CP 耦合代码 + 重塑 listener 角色 + 重写依赖 `mock_control_plane` 的测试。
  - 2b: 定义 + 实现 Go↔Rust 契约(UDS / 转发端点 / profile 传递 / 9 失败模式 / 可降级)。
  - 2c: Go `transport.Factory` 加 rust-sidecar backend, feature flag, standard 回退。
  - ~2-3 周。
- **Phase 3 — Rust 传输加固 + 伪装**。
  - 3a: Wave C —— L2 HTTP/2 伪装(核心伪装价值, 最难)。
  - 3b: Wave D —— sidecar Dockerfile + 部署拓扑。
  - 3c: 验证后把 rust-sidecar 切默认。
  - ~3-5 周。

**依赖**: Phase 1 完全独立于 Phase 2/3(Go 补洞不需要 Rust)。Phase 2 需要 Phase 1 完成(Rust 收到的是 Go 已备好的请求)。Phase 3 需要 Phase 2 的契约。

**强烈建议先做 Phase 1**: ❶ 让 HUAKAI 立刻正确(不再丢请求); ❷ Go-only, 期间已做的 Rust C 档工作稳定不动、无 churn; ❸ Phase 1 单独交付就是一个能用的网关里程碑。

总估时 ≈ 9-13 周。但 Phase 1(~4 周)单独就交付一个正确网关。

## 6. 风险 + 需 Owner 拍板

**决策点**:
- D1: 阶段顺序 —— 确认 Phase 1(Go correctness)先于 Phase 2/3(Rust 传输)。我推荐: 是。
- D2: Go↔Rust 传输介质 —— UDS 主 / localhost TCP 备 / gRPC。我推荐: UDS 主。
- D3: Rust 传输层是**可选 + 可降级**还是必选 —— 我**强烈推荐可选**(这是「交互不能出问题」的保证)。
- D4: usage 计费权 —— Go 持有, Rust 纯传输。我推荐: 是。
- D5: 调用方限流(洞 ⑤)单实例内存版起步还是直接上 Redis 分布式 —— 看 HUAKAI 是否已多实例部署。

**风险**:
- R1: 洞 ② —— 请求体缓冲内存、计费幂等(重复扣费)、流开始后禁 failover。需细设计 + Codex 交叉评审。
- R2: 洞 ① —— 改出站 body 可能破坏上游兼容; 必须按 vendor flag + 分阶段。
- R3: 退役 Rust CP 代码 —— C 档测试依赖 `mock_control_plane`, 退役要重写这些测试; 确认无其它依赖。
- R4: 是否引入 DB schema 变更 —— 洞 ② 的 FailoverState 若仅 in-request 内存则无 schema 变更(我倾向如此); 若要持久化失败账号记录则 schema 变更 = Owner 确认(高风险项)。
- R5: clean-room —— 6 洞实现参考 sub2api/CLIProxyAPI/new-api 的 retry/failover/cloaking 做法(gap 分析已引证); 实现须 paraphrase 不抄。

---
本稿 lane: planner/architect —— 基于已引证的 gap 分析(`2026-05-21-account-to-api-gap-analysis.md`)做架构与排期, 未读取参考项目源码、未读 codex 平行稿。agent: Claude (claude-opus-4-7). UTC 2026-05-21.
