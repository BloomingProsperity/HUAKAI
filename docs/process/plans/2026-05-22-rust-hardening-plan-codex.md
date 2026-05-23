# 2026-05-22 HUAKAI Rust Gateway Hardening Plan - Codex Specifier Draft

| 字段 | 内容 |
|---|---|
| Owner directive | "你是 Codex 独立 specifier 车道，按 CLAUDE.md #10 严格双稿并行模式独立起草 HUAKAI Rust 网关加固计划。" |
| 输出路径 | `docs/process/plans/2026-05-22-rust-hardening-plan-codex.md` |
| 起草车道 | Codex specifier; 本会话没有读取 `2026-05-22-rust-hardening-plan-claude.md` 内容。 |
| 范围 | W11 安全边界 D-1/D-2/D-3/D-6/D-10; W12 账务遥测 D-4/D-5/D-7/D-8/D-9/O-2; 指纹 L1 TLS 缺口闭合 + L2 HTTP/2 客户端伪装接线。 |
| 明确不做 | 不改 `backend/`; 不 vendor 参考项目; 不写实现代码; 不改变 `LICENSE`; 不提交 commit。 |
| 工程边界 | 只动 `exploratory/rust-core-gateway/`; WSL2 Ubuntu Rust 1.95 为编译环境; Windows 因 UDS Linux-only 不作为通过标准。 |
| 关键结论 | D-1 是架构闸门: 当前 Rust 数据面没有客户端认证, `RouteQueryRequest` 也没有认证主体字段, 所以 W11 必须先补身份边界, 否则 D-4/D-5/D-8 的账务遥测只能记录伪造身份。 |
| Clean-room | 本稿读取了 LGPL/AGPL/MIT/Apache 参考源码, 只输出行为释义与测试要求; 不输出上游代码、结构、字段清单、注释或逐行算法。 |
| First-cite recency | 本地 clone HEAD 均等于 Owner 指定 SHA; commit 日期均在 90 天内 (first-cite 入选窗口). **Synthesis 修正 (per AGENTS.md / CLAUDE.md #12 30-day rule, 闭 Codex per-commit review 第 4 轮 P2)**: 实施时若 citation 老于 **30 天** 必须 re-fetch HEAD 后再依赖, 并校验 SHA 是 reachable-from-default-branch (即在 `git log --first-parent <default-branch>` 中可达). 此处的 90 天**仅作 first-cite 入选窗口, 不可作长期使用窗口**. Shell GitHub API 因网络受限不可达; GitHub 页面/搜索可见项目为公开活跃仓库, 未观察到 archive banner. |
| 估时单位 | codex-day。 |

## §0 背景方法

本计划从两个 HUAKAI 内部证据开始: Rust 深度审计明确 D-1 到 D-10 的失败场景, 其中 W11/W12 被权威波次计划归为 Rust exploratory 预生产硬化; 权威波次还规定 Rust 仍在最后, 除非 Owner 决定 canary, 则 W11/W12 必须提前到 W7 前并成为生产流量 release gate。HUAKAI citation: `docs/process/research/2026-05-22-deep-audit-rust.md:1`, `docs/process/plans/2026-05-22-audit-remediation-wave.md:71`, `docs/process/plans/2026-05-22-audit-remediation-wave.md:103`.

方法是"先证据、再修法、再判别性测试"。每条 finding 使用 HUAKAI 现状源码定位, 再用多源参考项目提取行为形状; 参考项目只证明能力/机制存在, 不决定 HUAKAI 的文件结构或实现名。CLAUDE.md #12 要求能力/机制/差异化 claim 有源码引用, #14 要求测试在缺陷被植入时变红。HUAKAI citation: `CLAUDE.md:61`, `CLAUDE.md:115`.

参考项目使用范围:
- sub2api: LGPL-3.0, 只作机制释义; 观察到它对 URL 配置、usage worker 参数、重试/队列参数有显式校验。`Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/config/config.go:1967`, `Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/config/config.go:2562`
- cliproxyapi: MIT, 只作机制释义; 观察到它从多种客户端凭据载体建立认证结果、读取请求体后再进入翻译/执行链、并将 usage 事件进入可订阅队列。`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/access/config_access/provider.go:55`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/request_body.go:14`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/redisqueue/plugin.go:20`
- clewdr: AGPL-3.0, 只作释义、禁 vendor; 观察到它对用户/API 管理请求有显式 bearer/API-key 检查, 对限流响应提取重置时间, 并在 HTTP 客户端层声明浏览器风格 emulation。`Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/middleware/auth.rs:36`, `Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/error.rs:301`, `Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/utils/mod.rs:58`
- smg: Apache-2.0, 只作机制释义; 观察到它的 Rust client 将 429/5xx 作为可重试类, 读取 retry-after, server 侧按 body model 路由且健康接口由真实 worker 健康数驱动。`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:clients/rust/src/transport.rs:14`, `lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:clients/rust/src/transport.rs:116`, `lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/server.rs:183`
- litellm-rs: MIT, 只作机制释义; 观察到它将 API key hash、active/expiry、user 关联与 usage stats 作为认证/账务对象, 并有错误 retryability 与 server request metrics 测试。`majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/auth/api_key/creation.rs:124`, `majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/auth/api_key/creation.rs:258`, `majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:tests/integration/error_handling_tests.rs:76`.

## §1 范围与成功标准

W11 成功标准:
- D-1: 所有数据面请求必须先经 Rust 本地客户端凭据解析; tenant/client identity 不再来自客户端可伪造 header; route query 只带认证后的主体和从 body 解析的协议元数据。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/listener.rs:72`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs:205`, `exploratory/rust-core-gateway/merged/proto/route.proto:28`
- D-2: mock upstream 只允许 dev/test 显式模式; production/canary 启动时遇到 mock endpoint fail fast, 或进入明确演练模式且不携带真实上游凭据。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs:274`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/listener.rs:72`
- D-3: planned vendor endpoint 在生产必须是 HTTPS 且 host 通过控制面策略; HTTP 仅限 mock/test 且不能注入真实 token。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs:244`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:30`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/auth.rs:46`
- D-6: 客户端传来的供应商账户选择类 header 不得随 gateway Bearer 凭据转发; 只允许 route plan/control plane 注入上游账户维度。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/headers.rs:54`
- D-10: mimicry backend 选择必须先尊重 profile 的 known-gap/unsupported 判定, 不能因为编译了 Boring feature 就绕过阻断; 同时闭合 L1/L2 指纹接线。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/backend_resolver.rs:76`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs:181`

W12 成功标准:
- D-4: 成功可计费 terminal attempt report 不得静默丢弃; 队列满、control plane 慢、后台不可用时必须 fail-closed、阻塞退避或落 durable spool。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/mod.rs:140`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:390`
- D-5: 非 SSE 成功响应也提取协议 usage; 解析失败进入 reconciliation 风险状态, 不再默默填 missing。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:64`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/stream_pipeline/openai.rs:63`
- D-7: heartbeat 填真实 node id、start time、in-flight、queue depth、latency/error 统计; unavailable 用 unknown/degraded 表达, 不能用 0 伪装健康。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/heartbeat.rs:74`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/resource_limits.rs:96`
- D-8: 429/408 与供应商限流体分类为 retryable/rate-limited, 并带账号健康信号。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs:370`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/types.rs:61`
- D-9: request bytes_in 来自实际 body 计数, 不是只信 Content-Length。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs:223`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs:380`
- O-2: Rust smoke/CI 能证明 W11/W12 关键路径在 Linux 上通过; Windows 不作为 UDS 编译闸门。HUAKAI citation: `docs/process/plans/2026-05-22-audit-remediation-wave.md:79`.

全局成功标准:
- 每条 finding 至少一条判别性测试, 并写明 mutation: 缺陷植入后红、正常后绿。
- 每个切片提交前运行 `codex exec review --uncommitted --full-auto`。
- 不触碰 `backend/`; 若实施中发现必须改 control-plane proto 消费端, 停下让 Owner 决策, 不能绕过。

## §2 W11 逐 finding

### D-1 HIGH - 路由身份与 model/stream 来自可伪造 header

HUAKAI 现状: listener 在 mock 分支后直接以 request headers 做 route planning, Rust route query 构造函数只接收 header map, tenant/model/stream 均从 header/default 推导; `RouteQueryRequest` 目前只有 request_id、tenant、requested_model、session、protocol、stream、deadline、previous attempts、hints, 没有客户端凭据或认证主体字段。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/listener.rs:72`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs:205`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs:208`, `exploratory/rust-core-gateway/merged/proto/route.proto:28`.

参考做法多源: cliproxyapi 在进入执行链前从 Authorization、Google key、Anthropic key、query key 等载体建立认证结果, 且后续读取请求体用于协议转换; smg server 的多个路由入口把已解析 body model 交给路由层; litellm-rs 把 client key hash、active/expiry、user 关联作为认证判断输入。Reference citations: `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/access/config_access/provider.go:62`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/request_body.go:14`, `lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/server.rs:183`, `majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/auth/api_key/creation.rs:258`.

修法: 新增 Rust 数据面 `client_auth` 小模块, 负责从 Authorization / x-api-key 等入口解析客户端凭据, 只保存 hash/prefix/source, 不记录 raw secret; listener 在 body-size 上限内读取/tee 请求体前缀, 按 OpenAI/Anthropic/Gemini 解析 model 与 stream, 失败则返回公开 400/401; route query 只接受认证主体和 body-derived protocol metadata。D-1 必须触发 `route.proto` 受控变更, 详见 §4.5。

融合升级 delta: 架构上把 client identity 从 header trust 转为 auth context; 算法上使用 body parser 的 self-proving fixture 证明 header 与 body 冲突时选择 body; 生态上保持 OpenAI Bearer 与 Anthropic x-api-key 兼容, 但只作为 HUAKAI client key, 不作为 upstream account selector。

判别性测试: `listener_rejects_missing_client_identity_before_route_query` 发送带 body model 但无 client credential 的请求, 断言 control plane 未收到 route query 且响应 401; mutation 删除 auth gate 后会变绿为 route query 发生, 测试应红。`route_query_uses_body_model_not_header_model` 同时设置 header model=cheap、body model=expensive, 断言 mock control plane 收到 expensive; mutation 回到 header parser 时应红。`tenant_cannot_be_spoofed_by_header` 使用 valid key 属于 tenant A, header 声称 tenant B, 断言 route query tenant=A; mutation 信 header 时红。

切片: W11-A, 0.8 codex-day. 文件: create `crates/core_gateway/src/client_auth.rs`, modify `listener.rs`, `account_planner.rs`, `route.proto`, generated route proto, focused tests under Rust crate tests. Owner 决策: API key 来源和 control-plane key resolver 是本切片关键架构点; 若 Rust gateway 暂不接真实 resolver, 必须显式 Manual First/Feature Flag, 不能继续信任 header。

### D-2 HIGH - mock upstream 可绕过控制面与 attempt ledger

HUAKAI 现状: config 从环境读取 mock upstream endpoint; listener 只要看到该值就直接 forward endpoint, 发生在 route planning 之前; `forward_endpoint` 传入 planned=None 和 terminal reporter=None。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs:274`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/listener.rs:72`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs:258`.

参考做法多源: sub2api 对 gateway usage record 的 worker、queue、overflow 等 production config 做正值和枚举校验; cliproxyapi 把 usage publication behind explicit enablement and payload queue, 不把真实请求无记录旁路当成默认路径; clewdr 对用户/admin 入口有认证 guard, 即本地代理也不默认裸放。Reference citations: `Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/config/config.go:2562`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/redisqueue/plugin.go:24`, `Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/middleware/auth.rs:73`.

修法: 添加 `RuntimeMode` 或等价配置, production/canary 启动时 mock endpoint 存在即 fail fast; dev/test mock 仍可用, 但必须生成 explicit mock attempt event, 并拒绝真实 upstream auth material 注入。listener 顺序必须变为: config mode gate -> client auth -> request metadata -> route/mock decision -> attempt reporting。

融合升级 delta: 架构上把 mock 从 secret bypass 变为 mode-gated exercise path; 算法上 mock path 仍创建 audit/attempt envelope; 生态上保留本地开发便利但默认生产不可启动。

判别性测试: `production_rejects_mock_upstream_at_startup` 设置 production mode + mock endpoint, 断言 config load/startup error; mutation 删除 mode check 后红。`dev_mock_path_emits_mock_attempt_without_real_token` 在 dev mode 走 mock, 断言 reporter 收到 explicit mock status 且 upstream auth absent; mutation 保留旧 forward_endpoint no reporter 后红。

切片: W11-B, 0.35 codex-day. 文件: modify `config.rs`, `listener.rs`, `proxy_engine/mod.rs`, tests. 不改 backend。

### D-3 HIGH - planned vendor endpoint 允许 HTTP

HUAKAI 现状: account planner 只校验 vendor endpoint 有 scheme/authority, 不要求 HTTPS; default client connector 使用 HTTPS-or-HTTP; planned auth 会注入 Bearer。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs:244`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs:249`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:30`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/auth.rs:46`.

参考做法多源: sub2api 的 absolute URL 校验拒绝空、相对、非 HTTP(S)、fragment, 并对 auth/OIDC URL 做校验与 insecure warning; clewdr 固定上游默认 endpoint 为 HTTPS URL 且 reverse proxy 是显式配置; smg client transport 通过 base URL + auth header 建立请求, retry/error 行为围绕 HTTP status 而非裸 TCP fallback。Reference citations: `Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/config/config.go:2770`, `Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/config/config.go:2040`, `Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/config/clewdr_config.rs:377`, `lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:clients/rust/src/transport.rs:20`.

修法: `PlannedRoute` validation 增加 production HTTPS-only + host allowlist policy. HTTP endpoint 仅可在 dev/mock route plan 且 auth material absent 时通过。`https_or_http` fallback 在 default connector 中改为 HTTPS-only; 若 Boring connector feature 已启用, 同样拒绝 non-HTTPS SNI。

融合升级 delta: 架构上 endpoint trust 从 control-plane opaque string 升级为 policy object; 算法上先校验 endpoint 再注入 credentials; 生态上保留 lab HTTP mock 但禁止真实 credential over cleartext。

判别性测试: `planned_http_endpoint_rejected_before_bearer_injection` 返回 `http://127.0.0.1` route plan with auth material, 断言 planner/proxy error 且 mock upstream 未收到 Authorization; mutation 删除 scheme guard 后红。`dev_http_mock_without_auth_allowed` 在 explicit dev mock policy 下允许 HTTP 但断言无 upstream Bearer; mutation 无差别拒绝 dev mock 时红。

切片: W11-C, 0.35 codex-day. 文件: modify `account_planner.rs`, `proxy_engine/http_client.rs`, `proxy_engine/boring_tls_connector.rs`, tests.

### D-6 MED - 客户端供应商账户 header 透传

HUAKAI 现状: proxy header logic 在应用 planned auth 后仍允许 OpenAI org/project header 被转发; 这会让客户端影响上游 account scope, 但 HUAKAI route plan 仍以另一个账号记录。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/headers.rs:37`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/headers.rs:54`.

参考做法多源: cliproxyapi 有独立 upstream header filtering, 会移除 hop-by-hop 与安全敏感/代理注入类 header; clewdr 的 gateway auth 与 upstream cookie/API state 是分开的; smg server route entry 使用 request body/model 与 tenant metadata, 不让客户端直接指定 worker account。Reference citations: `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/header_filter.go:39`, `Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/middleware/auth.rs:81`, `lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/server.rs:187`.

修法: 将供应商账户选择类 header 从 client allowlist 移到 route-plan-only injection list。若 control plane 要传 org/project, 放入 RoutePlan 的 explicit upstream metadata, 并标记 source=control-plane; client-supplied 同名值全部剥离并在 debug/audit 中记录 stripped count, 不记录原值。

融合升级 delta: 架构上分离 client protocol compatibility header 与 upstream account ownership header; 算法上 allowlist 变为 direction-aware; 生态上降低 OpenAI/Anthropic/Gemini account leakage。

判别性测试: `client_org_project_headers_are_stripped_under_gateway_bearer` 发送 org/project header 和 valid body, mock upstream 断言没有收到客户端值; mutation 保留现 allowlist 时红。`control_plane_injected_account_headers_survive` route plan explicit metadata 注入同类 header, 断言上游收到 control-plane value; mutation 简单全删时红。

切片: W11-D, 0.25 codex-day. 文件: modify `proxy_engine/headers.rs`, route plan mapping if metadata exists; if route.proto lacks metadata, defer control-plane injection to Mandatory Roadmap and still strip client headers now.

### D-10 MED - mimicry feature 绕过 profile known-gap 阻断

HUAKAI 现状: profile backend resolver 看到 Boring feature 即返回 allowed Boring, 后续才在另一路函数中存在 profile intent 判定; profile 本身已能把 known-gap 标成 blocked。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/backend_resolver.rs:72`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/backend_resolver.rs:76`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs:181`.

参考做法多源: clewdr 用 HTTP client emulation 作为明确构造期选择, 不把未知 profile 默认放行; HUAKAI 自身 profile validation 已要求 HTTPS endpoint、redacted auth、sample/source fields; smg 的健康/routing 对 worker health 不是 optimistic default。Reference citations: `Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/utils/mod.rs:58`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs:382`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs:395`, `lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/server.rs:109`.

修法: 统一 resolver: 先计算 profile backend intent, known-gap/unsupported 直接 block, 再检查 feature availability, 最后验证 selected backend matches profile. 删除"feature-on 即允许"的先行返回。L1/L2 指纹接线同步在 §4 展开。

融合升级 delta: 架构上 profile truth 优先于 compile feature; 算法上 known-gap 是 deny state 而非 fallback; 生态上可安全打开 mimicry-boring 而不把未验证 profiles 推进生产。

判别性测试: `boring_feature_does_not_override_known_gap_profile` 在 mimicry-boring feature 下加载 known-gap profile, 断言 dispatch block; mutation 恢复 feature-first return 后红。`verified_profile_with_boring_feature_allows_boring` 使用明确支持 Boring 的 profile, 断言 allow; mutation 无差别阻断时红。

切片: W11-E, 0.35 codex-day. 文件: modify `mimicry/backend_resolver.rs`, `mimicry/dispatch.rs`, feature-gated tests. 与 §4 L1/L2 可以同 commit 或紧邻 commit, 但必须一波一提交。

## §3 W12 逐 finding

### D-4 HIGH - terminal attempt report best-effort 可丢账

HUAKAI 现状: reporter 使用 bounded try-send; 队列满只返回 dropped result; relay 和 listener 调用点忽略该结果。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/mod.rs:140`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/mod.rs:151`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:390`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/listener.rs:160`.

参考做法多源: sub2api 将 usage record worker/queue/overflow/auto-scale 参数显式配置和校验, 说明 usage 队列是生产参数而非隐形 best-effort; cliproxyapi 将 usage record 进入插件/队列并测试订阅 payload; litellm-rs database tests 检查 usage stats update 可被回读。Reference citations: `Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/config/config.go:1843`, `Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/config/config.go:2573`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/redisqueue/plugin_test.go:74`, `majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:tests/integration/database_tests.rs:289`.

修法: 把 terminal report 分级: billable success、billable failure、planning failure、non-billable mock. billable terminal report 不允许 `DroppedFull`; 实现顺序建议先阻塞退避 + deadline, 再本地 durable spool。若 deadline 内仍不可持久化, response path 必须 fail-closed 或返回 accepted-but-not-delivered 禁止记成功。

融合升级 delta: 架构上 terminal report 是 money/trust fact; 算法上队列状态反馈回 relay outcome; 生态上支持未来 durable spool 与 control-plane backpressure。

判别性测试: `billable_success_report_queue_full_fails_response_or_spools` 填满 reporter queue 后发成功 upstream 响应, 断言不是 client 200 with no report; mutation 忽略 DroppedFull 时红。`planning_failure_report_can_degrade_without_billing_success` route plan error report drop 不产生成功账务, 断言 public failure and metric increment。

切片: W12-A, 0.6 codex-day. 文件: modify `attempt_reporter/mod.rs`, `attempt_reporter/types.rs`, `proxy_engine/relay.rs`, `listener.rs`, tests.

### D-5 HIGH - 非流式成功响应不解析 JSON usage

HUAKAI 现状: relay 只有 SSE response 才创建 usage tap; non-SSE body chunk 只记 bytes; OpenAI JSON usage parser 已存在但未接入 proxy relay。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:64`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:155`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/stream_pipeline/openai.rs:63`.

参考做法多源: cliproxyapi zero-usage test 证明 provider/model/total tokens 即使为零也要进入 usage queue; clewdr 对不同 Claude 订阅窗口做用量 bucket; litellm-rs 的 completion route/integration tests 要求 response usage 可被读取。Reference citations: `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:test/usage_logging_test.go:21`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:test/usage_logging_test.go:68`, `Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/config/cookie.rs:221`, `majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:tests/integration/completions_route_tests.rs:205`.

修法: 增加 bounded non-stream response tap: 在 max report body bytes 内复制响应 body 到 parser, 不中断透传; OpenAI/Anthropic/Gemini 按 protocol 解析 usage. success response 缺 usage 时标记 `usage_source=missing_reconcile_pending`; parse error 记录 redacted parse class, 不记录 raw body。

融合升级 delta: 架构上 usage extraction 覆盖 stream/non-stream; 算法上零 token 与缺 token 区分; 生态上为 reconciliation/billing confidence 提供来源标签。

判别性测试: `non_stream_openai_usage_reaches_attempt_report` upstream 返回 JSON usage input=3/output=5, 断言 report tokens total=8; mutation 不接 relay parser 时红。`zero_usage_is_reported_not_missing` upstream 返回 usage total=0, 断言 source=reported zero 而不是 missing; mutation 使用 truthy/nonzero 判定时红。`oversize_json_body_marks_reconcile_pending` 超过上限 body 不解析 raw, 断言 pending flag。

切片: W12-B, 0.45 codex-day. 文件: modify `proxy_engine/relay.rs`, `stream_pipeline/openai.rs`, add anthropic/gemini non-stream parser helpers if absent, tests.

### D-7 MED - heartbeat 硬编码健康数据

HUAKAI 现状: heartbeat node id 固定, start time/in-flight/queue/dependent metrics 全为 0; resource limits 与 reporter 已有真实 in-flight/queue depth 来源。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/heartbeat.rs:74`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/heartbeat.rs:78`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/resource_limits.rs:96`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/mod.rs:166`.

参考做法多源: smg server health response 基于 healthy worker count 与 routing mode, 不是固定健康; smg CLI/config exposes health check thresholds, timeouts, interval; litellm-rs server metrics types include request status, duration, payload size and auth identifiers. Reference citations: `lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/server.rs:109`, `lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/main.rs:405`, `lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/main.rs:1227`, `majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/server/types.rs:22`.

修法: 在 server state 中保存 start time、node id、sliding latency/error recorder; heartbeat 从 ResourceLimits、AttemptReporter、ProxyEngine stats 读真实值。无法采集的值用 explicit unknown/advisory 或 omitted metric, 不可填 0。

融合升级 delta: 架构上 heartbeat 成为 control-plane scheduling signal; 算法上滑动窗口 P95/error rate 代替常量; 生态上支持 drain/overload/canary safety。

判别性测试: `heartbeat_reflects_inflight_and_queue_depth` 持有一个 in-flight guard、填入 reporter queue, 断言 heartbeat >0; mutation hardcode 0 时红。`heartbeat_unknown_latency_not_zero_when_no_samples` 无样本时断言 unknown/degraded flag, 不是 p95=0; mutation 默认 0 时红。

切片: W12-C, 0.35 codex-day. 文件: modify `heartbeat.rs`, `server_runtime.rs`, `resource_limits.rs` accessor if needed, `attempt_reporter/mod.rs`, tests.

### D-8 MED - 429/408 归为不可重试 4xx

HUAKAI 现状: status classifier 将所有 4xx 归为 upstream 4xx; retryable 只包含 transport/timeout/5xx; terminal report 使用该分类。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs:370`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/types.rs:61`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:256`.

参考做法多源: smg Rust client 明确把 429 放入可重试状态集并读取 retry-after; smg client error tests 单独断言 429 rate-limit class; clewdr 从 provider error headers/body 中推导 reset time; litellm-rs tests 断言 rate limit 和 network/unavailable/timeout retryability 与 auth/model error 不同。Reference citations: `lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:clients/rust/src/transport.rs:14`, `lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:clients/rust/src/transport.rs:122`, `lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:clients/rust/tests/test_error.rs:31`, `Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/error.rs:301`, `majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:tests/integration/error_handling_tests.rs:76`.

修法: 扩展 attempt status taxonomy: rate_limited, upstream_timeout, provider_transient_4xx. 429、408、vendor body/header 中明确临时限流信号均 retryable; 400/401/403/404 保持 non-retryable, 且 client auth 401 与 upstream auth 401 分开。

融合升级 delta: 架构上 attempt report 同时服务 billing 与 health; 算法上分类使用 status + bounded redacted body/header signal; 生态上可接账号 cooldown/failover。

判别性测试: `status_429_is_retryable_rate_limited_not_plain_4xx` 断言 class 和 retryable; mutation 全 4xx 分支时红。`status_401_is_not_retryable` 防止过宽分类; mutation 把所有 4xx retryable 时红。`retry_after_is_recorded_redacted` 429 with retry-after 断言 health hint present。

切片: W12-D, 0.35 codex-day. 文件: modify `attempt_reporter/types.rs`, `proxy_engine/mod.rs`, `proxy_engine/relay.rs`, tests.

### D-9 MED - bytes_in 只信 Content-Length

HUAKAI 现状: proxy forward 前从 header 计算 request_bytes_in; helper 只读 Content-Length, 缺失/解析失败返回 0; relay 不计 request body 实际 bytes。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs:223`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs:380`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/types.rs:214`.

参考做法多源: cliproxyapi 先读取并解码请求体, 因而路由/翻译逻辑不依赖 Content-Length; smg server handlers 接收 validated JSON body 后路由, 不用 body absence 推导 model; litellm-rs server metrics tests include large payload request metrics, 说明 payload size 是可观测维度。Reference citations: `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/request_body.go:14`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/request_body.go:40`, `lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/server.rs:195`, `majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/server/types.rs:378`.

修法: 包装 inbound body, 在转发时累加 chunk bytes; D-1 body tee 可复用同一计数器, 避免重复 buffer。Content-Length 只作为 expected hint, 实际 bytes 以 observed counter 为准; mismatch 进入 telemetry field。

融合升级 delta: 架构上 request accounting 与 body stream 同生命周期; 算法上 header hint + observed counter 双轨; 生态上支持 HTTP/2/chunked uploads。

判别性测试: `chunked_or_h2_body_without_content_length_reports_observed_bytes` 构造无 Content-Length body 12 bytes, 断言 report bytes_in=12; mutation 用 header-only helper 时红。`content_length_mismatch_records_observed_not_claimed` header=999 body=12, 断言 observed=12 and mismatch flag; mutation 信 header 时红。

切片: W12-E, 0.3 codex-day. 文件: modify `proxy_engine/mod.rs`, body wrapper helper in focused module, `attempt_reporter/types.rs`, tests.

### O-2 - Rust lane smoke/CI 闭环

HUAKAI 现状: 权威波次把 Rust W11/W12 作为 exploratory 预生产硬化, 并说明 Windows 编译不作为闸门; 用户要求 WSL2 Ubuntu Rust 1.95。HUAKAI citation: `docs/process/plans/2026-05-22-audit-remediation-wave.md:79`.

参考做法多源: sub2api DEV guide uses split unit/integration commands; smg has client and gateway tests plus metrics/health checks; litellm-rs has integration test suites for database/auth/config/error handling. Reference citations: `Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:DEV_GUIDE.md:54`, `lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/tests/security_tests.rs:1`, `majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:tests/integration/config_validation_tests.rs:268`.

修法: 增加 WSL-only verification script/doc step under exploratory tree, not backend. Required commands: `cd exploratory/rust-core-gateway/merged && cargo test -p core_gateway`, `cargo test -p core_gateway --features mimicry-boring`, `cargo test -p core_gateway --features mimicry-http2-fork`, plus focused integration smoke for UDS if available.

融合升级 delta: 架构上把 Rust readiness 从 "can build somewhere" 转为 Linux release gate; 算法上 feature matrix 分别验证; 生态上为 future canary 提供 reproducible smoke.

判别性测试: `feature_matrix_ci_runs_boring_and_http2` is a plan-level acceptance check: missing feature command fails review. Mutation 删除 mimicry-http2 command 时 review checklist 应红。

切片: W12-F, 0.15 codex-day. 文件: exploratory Rust docs/scripts only; no backend.

## §4 指纹 L1 + L2

L1 TLS 缺口闭合现状: Cargo 已声明 `mimicry-boring`、`mimicry-openssl`、`mimicry-http2-fork`, default empty; default outbound client 仍保留 hyper-rustls HTTPS兜底, Boring connector 仅 feature gated; vendor profile audit notes 记录 Boring wire 与 expected profile 仍有 mismatch。HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:8`, `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:36`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:26`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/vendor_profile_audit_notes.md:27`.

L1 修法: Boring production profile 必须有 byte-level preflight: actual ClientHello hash/order equals profile expectation, or dispatch blocks with known-gap. Do not use rustls/hyper-rustls as mimicry fallback for production; rustls remains non-mimicry HTTPS only where policy explicitly says "no fingerprint claim." L1 cutover must include per-profile allowlist: only profiles with passing byte fixture can be used for production dispatch.

L1 判别性测试: `verified_profile_requires_matching_clienthello` captures Boring output and compares against profile evidence; mutation accepting mismatch should red. `non_mimicry_https_cannot_claim_profile` default hyper-rustls path with profile requested must block; mutation allowing fallback should red.

L2 HTTP/2 当前现状: HTTP/2 fork adapter exists but comments state it is feature-gated and not wired into ProxyEngine; profile validation requires H2 settings/order fields only when available; built-in Anthropic profile says HTTP/2 order capture unavailable. HUAKAI citation: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http2_adapter.rs:1`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http2_adapter.rs:65`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs:340`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profiles/anthropic_claude_code.json:123`.

L2 修法: Wire HTTP/2 adapter into the Boring HTTPS client path only after L1 profile passes. The connection builder must apply profile SETTINGS order, SETTINGS values, and pseudo-header order; if any captured profile marks these unavailable, production dispatch blocks or downgrades to "no L2 claim" depending Owner policy. clewdr demonstrates client emulation as a first-class client construction concern; HUAKAI should keep that concern inside mimicry transport, not at handler level. Reference citation: `Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/utils/mod.rs:58`.

L2 判别性测试: `http2_adapter_writes_profile_settings_before_headers` uses memory capture to assert SETTINGS order and first request HEADERS are profile-derived; mutation using default h2 builder should red. `profile_missing_h2_capture_blocks_l2_claim` loads a profile with unavailable H2 order and asserts production dispatch blocks or labels known-gap; mutation silently defaulting order should red.

Fingerprint slice: W11-F, 0.75 codex-day. Files: `mimicry/backend_resolver.rs`, `mimicry/dispatch.rs`, `mimicry/http2_adapter.rs`, `proxy_engine/boring_tls_connector.rs`, `proxy_engine/http_client.rs`, profile tests and audit notes. Owner decision: whether profiles with L1 pass but L2 missing may serve canary traffic under "L1-only" feature flag, or must be blocked until full L2 capture exists.

## §4.5 `route.proto` 受控变更集

D-1 强制变更 because current route query cannot carry authenticated client identity. Existing fields stop at capability hints field 9; attempt report already carries route/account/acquisition material but route query has no safe client principal. HUAKAI citation: `exploratory/rust-core-gateway/merged/proto/route.proto:28`, `exploratory/rust-core-gateway/merged/proto/route.proto:77`.

Proposed controlled additions:
- Add `ClientIdentity` message with non-secret fields only: stable client id, tenant id resolved by auth, credential hash/prefix, auth scheme, source, and optional scopes. No raw API key, no upstream credential.
- Add `ClientIdentity client_identity = 10` to `RouteQueryRequest`.
- Add body-derived `request_model_source = 11` and `stream_source = 12` or equivalent enum/string so control plane can distinguish body-derived vs internal override.
- Reserve/deprecate direct trust in header-derived tenant/model in Rust planner; keep existing fields for backward wire compatibility but populate them from authenticated/body context only.

Compatibility plan:
- Phase 1: Rust sends both old fields and new identity; control plane may ignore new fields while Rust still enforces auth locally.
- Phase 2: control plane rejects route queries missing client identity for Rust gateway schema version.
- Phase 3: remove any Rust path that reads `x-tenant-id`/model/stream as authority.

判别性 proto test: `route_query_redacting_debug_never_prints_client_secret` builds route query with client identity hash/prefix and raw key absent, asserts debug does not include raw key. Mutation accidentally putting raw credential in proto field should red.

Owner decision: if control plane cannot resolve client key yet, choose one: `Manual First` static local key map for canary, `Feature Flag` disables public Rust ingress, or `Mandatory Roadmap` blocking Rust production. Do not ship header-derived tenant as a safe equivalent.

## §5 波次顺序估时

| 顺序 | 切片 | 内容 | 估时 |
|---:|---|---|---:|
| 1 | W11-A | D-1 client auth + body-derived route metadata + proto change | 0.8d |
| 2 | W11-B | D-2 mock upstream production gate | 0.35d |
| 3 | W11-C | D-3 HTTPS-only planned endpoint + credential guard | 0.35d |
| 4 | W11-D | D-6 strip client-supplied provider account headers | 0.25d |
| 5 | W11-E | D-10 resolver respects profile known-gap before feature availability | 0.35d |
| 6 | W11-F | Fingerprint L1 byte preflight + L2 HTTP/2 adapter wiring decision | 0.75d |
| 7 | W12-A | D-4 billable terminal attempt report cannot drop silently | 0.6d |
| 8 | W12-B | D-5 non-stream JSON usage extraction | 0.45d |
| 9 | W12-C | D-7 heartbeat real metrics | 0.35d |
| 10 | W12-D | D-8 retry/rate-limit status taxonomy | 0.35d |
| 11 | W12-E | D-9 observed request body bytes | 0.3d |
| 12 | W12-F | O-2 WSL2 feature-matrix verification | 0.15d |

Total: 5.05 codex-day. 权威波次给 W11/W12 合计 5.5d; 本稿余量 0.45d 用于 route.proto generation, WSL setup drift, and codex review fixes.

Recommended commit grouping:
- Keep W11-A separate because proto/client auth is the architecture gate.
- W11-B/C/D may be separate commits, not one "security cleanup" commit, because tests and rollback differ.
- W11-E/F may be two commits unless L2 wiring requires resolver changes in same file.
- W12-A must precede W12-B/E because report durability determines whether usage/bytes facts matter.
- W12-D can run before W12-C only if no heartbeat health signal depends on new retry taxonomy; otherwise keep listed order.

Verification commands per implementation commit:
- `wsl -d Ubuntu -- bash -lc 'cd /mnt/c/HUAKAI/server-clones/HUAKAI-code/exploratory/rust-core-gateway/merged && cargo test -p core_gateway'`
- `wsl -d Ubuntu -- bash -lc 'cd /mnt/c/HUAKAI/server-clones/HUAKAI-code/exploratory/rust-core-gateway/merged && cargo test -p core_gateway --features mimicry-boring'`
- `wsl -d Ubuntu -- bash -lc 'cd /mnt/c/HUAKAI/server-clones/HUAKAI-code/exploratory/rust-core-gateway/merged && cargo test -p core_gateway --features mimicry-http2-fork'`
- `codex exec review --uncommitted --full-auto` before each commit.

## §6 影响面风险

Architecture risk:
- D-1 introduces local client auth before control-plane support is confirmed. Safe path is feature-flagged local resolver for canary plus proto fields; production requires Owner approval.
- `route.proto` change affects generated Rust code and mock control plane tests. Since scope is exploratory Rust, this is acceptable, but if backend/control-plane must consume it, stop for Owner.

Security risk:
- D-3/D-6 reduce credential leakage risk. Regression risk is provider compatibility when clients relied on org/project passthrough; mitigation is control-plane explicit injection, not client passthrough.
- D-2 may break developer workflows that use mock endpoint in normal env. Mitigation is explicit dev/test mode and documented command.

Billing/trust risk:
- D-4 may turn previously successful responses into failures when attempt reporting is unavailable. This is intentional for billable success; Owner must decide fail-closed vs durable spool first if user-visible availability is prioritized.
- D-5/D-9 can change reported token/byte counts. Tests must include fixtures where old and new outputs differ to avoid false confidence.

Fingerprint risk:
- L1/L2 strict gating may block profiles that currently "work" by accident. Mark blocked profiles as KnownGap or Feature Flag, never silently downgrade while claiming mimicry.
- clewdr AGPL behavior is only evidence; no vendoring or code adaptation from AGPL source. Reference citation for client emulation behavior remains `Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/utils/mod.rs:58`.

Package/file structure:
- New Rust modules allowed under `exploratory/rust-core-gateway/merged/crates/core_gateway/src/` if cohesive.
- Do not add files under frozen Go packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`; this plan does not touch `backend/`.

## §7 Owner 决策点

1. D-1 client identity source: approve one of local static API key resolver for Rust canary, control-plane resolver RPC, or no public Rust ingress until resolver exists.
2. `route.proto` controlled additions: approve adding non-secret `ClientIdentity` to `RouteQueryRequest` now, with old fields populated from authenticated/body context for compatibility.
3. D-4 reporting failure policy: choose fail-closed on billable success when reporter cannot enqueue, or durable local spool first. Best-effort drop is not an acceptable option.
4. Fingerprint policy: decide whether L1-only verified TLS profile can serve canary traffic if L2 HTTP/2 capture is missing, or whether any missing L2 means block.
5. Mock mode policy: approve production fail-fast for `HUAKAI_MOCK_UPSTREAM_ENDPOINT`; dev/test mode remains available.

## §8 clean-room 交接

This session is contaminated for implementation because it read LGPL and AGPL reference source. Implementation must be done in a fresh clean session reading this plan and HUAKAI-owned docs/code only. The implementer must not open `C:/Users/h/refs/sub2api/` or `C:/Users/h/refs/clewdr/` for this artifact. MIT/Apache references are still treated here as evidence only; vendoring, if ever considered, requires a separate dependency-license audit and Owner decision.

Reference behavior claims in this plan are paraphrased. No upstream source code blocks, comments, distinctive schemas, UI source, or tests are copied. File:line citations are evidence anchors only.

实施交接 checklist:
- Start from W11-A; do not touch W12 until D-1 identity boundary is settled.
- For every test, write the mutation sentence in the test name/comment or nearby doc.
- For every new Rust file, state cohesive responsibility in commit message.
- Run WSL2 Linux Rust tests, not Windows cargo, for pass/fail.
- Run `codex exec review --uncommitted --full-auto` before commit.

Implementation-ready acceptance map:
- W11-A gate 1: missing client credential returns public 401 before any route query is emitted.
- W11-A gate 2: body-derived model wins over spoofable header model in the route query.
- W11-A gate 3: tenant comes from authenticated principal, not from `x-tenant-id`.
- W11-A gate 4: route query debug/log output contains only hash/prefix, never raw client secret.
- W11-B gate 1: production/canary startup fails when mock upstream endpoint is configured.
- W11-B gate 2: dev/test mock still emits an explicit mock attempt report.
- W11-C gate 1: HTTP planned endpoint is rejected unless the route is marked mock/test.
- W11-C gate 2: upstream credential injection is blocked for non-HTTPS planned endpoints.
- W11-D gate 1: client-supplied provider-account headers are stripped from outbound requests.
- W11-D gate 2: control-plane injected provider metadata still reaches the upstream path.
- W11-E gate 1: profile known-gap blocks production mimicry even when a transport feature is compiled.
- W11-E gate 2: resolver reports the blocking reason instead of silently downgrading.
- W11-F gate 1: TLS preflight mismatch blocks profile use.
- W11-F gate 2: HTTP/2 settings/order fixture distinguishes profile-derived behavior from default builder behavior.
- W12-A gate 1: billable success cannot complete after a bounded reporter queue drop.
- W12-A gate 2: non-billable failed attempt may degrade without losing the terminal billable event.
- W12-B gate 1: non-stream JSON usage is extracted into the attempt report.
- W12-B gate 2: malformed success usage is marked reconciliation-needed, not `missing`.
- W12-C gate 1: heartbeat exposes actual in-flight/queue counters.
- W12-C gate 2: unavailable metrics are `unknown`/`degraded`, not fabricated zeroes.
- W12-D gate 1: 429/408 are retryable or rate-limited with account-health signal.
- W12-D gate 2: 401/403 remain credential/auth failures and are not retried.
- W12-E gate 1: request bytes come from observed body chunks when Content-Length is absent.
- W12-E gate 2: Content-Length mismatch reports observed bytes plus mismatch flag.
- W12-F gate 1: Linux feature-matrix commands are part of the release checklist.
- W12-F gate 2: removing one required feature command makes the checklist fail review.

Non-goals for the implementation session:
- Do not implement full backend account hub or billing ledger changes in this Rust hardening wave.
- Do not rely on reference repositories during implementation; use this plan as the clean-room bridge.
- Do not claim production readiness until D-1, D-4, and fingerprint policy decisions are closed.
- Do not add a new runtime dependency without Owner approval and dependency-license audit.
- Do not move tests into weak smoke-only assertions; every listed gate needs a discriminating fixture.

Chinese Owner summary: 本稿独立起草了 Rust W11/W12 加固计划, 真观察来自 HUAKAI Rust 审计、权威波次和本仓 Rust 源码; 合理推断是把参考项目的认证、usage、retry、health、fingerprint 机制转成 HUAKAI 自主修法; open questions 共 5 个, 主要是 D-1 身份来源、proto 变更、D-4 fail-closed/spool、L1-only canary 和 mock 生产策略。没有功能缩水; clean-room 风险已隔离为 specifier-only; 安全风险集中在未批准 D-1 时不得让 Rust 接生产流量。

Source files read: docs/process/research/2026-05-22-deep-audit-rust.md; docs/process/plans/2026-05-22-audit-remediation-wave.md; CLAUDE.md; docs/05_CLEAN_ROOM_POLICY.md; docs/RULES.md; exploratory/rust-core-gateway/merged/proto/route.proto; exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml; exploratory/rust-core-gateway/merged/crates/core_gateway/src/listener.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/auth.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/headers.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/boring_tls_connector.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/mod.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/types.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/stream_pipeline/openai.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/stream_pipeline/anthropic.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/heartbeat.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/resource_limits.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/backend_resolver.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http2_adapter.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profiles/anthropic_claude_code.json; exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/vendor_profile_audit_notes.md; C:/Users/h/refs/sub2api/DEV_GUIDE.md; C:/Users/h/refs/sub2api/backend/internal/config/config.go; C:/Users/h/refs/cliproxyapi/internal/access/config_access/provider.go; C:/Users/h/refs/cliproxyapi/sdk/api/handlers/request_body.go; C:/Users/h/refs/cliproxyapi/sdk/api/handlers/header_filter.go; C:/Users/h/refs/cliproxyapi/internal/redisqueue/plugin.go; C:/Users/h/refs/cliproxyapi/internal/redisqueue/plugin_test.go; C:/Users/h/refs/cliproxyapi/test/usage_logging_test.go; C:/Users/h/refs/clewdr/src/middleware/auth.rs; C:/Users/h/refs/clewdr/src/error.rs; C:/Users/h/refs/clewdr/src/config/clewdr_config.rs; C:/Users/h/refs/clewdr/src/config/cookie.rs; C:/Users/h/refs/clewdr/src/utils/mod.rs; C:/Users/h/refs/smg/clients/rust/src/transport.rs; C:/Users/h/refs/smg/clients/rust/tests/test_error.rs; C:/Users/h/refs/smg/model_gateway/src/server.rs; C:/Users/h/refs/smg/model_gateway/src/main.rs; C:/Users/h/refs/litellm-rs/src/auth/api_key/creation.rs; C:/Users/h/refs/litellm-rs/tests/integration/error_handling_tests.rs; C:/Users/h/refs/litellm-rs/tests/integration/database_tests.rs; C:/Users/h/refs/litellm-rs/tests/integration/completions_route_tests.rs; C:/Users/h/refs/litellm-rs/src/server/types.rs.
Lane: specifier
Agent: Codex
UTC: 2026-05-23T04:14:47Z
