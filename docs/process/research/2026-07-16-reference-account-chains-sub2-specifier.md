# 2026-07-16 参考项目账号链行为核实（Sub2API specifier）

## 元数据

| 项目 | 值 |
| --- | --- |
| Lane | specifier |
| 核心对象 | Sub2API 的 Claude、Gemini、Antigravity、Kimi 账号链；Grok 仅作已知上下文 |
| 补充镜像 | CLIProxyAPI、New API |
| Sub2API 本地可读 SHA | `09c6c6d74050cf49ed2fb864be6c11647798ef53` |
| CLIProxyAPI 本地可读 SHA | `26d45fd46a2d2911adef14772465131066dae465` |
| New API 本地可读 SHA | `246d62aa5ed3ba2a4728322c269c180a016dc9cd` |
| Observed regions | 38 |
| Inferences | 4 |
| Open questions | 12 |

> 版本边界：已尝试对三个参考仓执行非破坏性 `git fetch --prune origin`，但当前执行环境不允许写入参考仓 `.git/FETCH_HEAD`；随后尝试只读远端查询，又因 DNS 无法解析 GitHub 而失败。因此本文只声称核实上述本地可读 SHA，不声称它们等于 2026-07-16 的远端最新 HEAD。三个参考工作树均未被修改。

## 结论摘要

### Observed

Sub2API 在本地可读版本中，对 Claude、Gemini、Antigravity 已形成“授权入口—凭据保存—自动或手动刷新—调度门—请求执行—错误回写—额度/健康展示—管理恢复”的账号级闭环。三者共用账号运行状态和调度基础设施，但授权形态、账号元数据、出站协议、额度探测和错误语义并不相同。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:148`

Sub2API 中没有观察到与上述三者同级的 Kimi OAuth 服务、Kimi token provider、Kimi 专属额度探针或 Kimi 管理授权入口。当前可读证据显示，Kimi 被纳入通用 API-key/兼容协议能力：模型白名单、请求协议差异、thinking 透传、缓存 token 统计修正与计价识别，而不是独立订阅账号生命周期。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/thinking_protocol.go:41`

CLIProxyAPI 提供了 Sub2API 当前未观察到的 Kimi 独立账号链：设备授权、轮询确认、设备身份绑定、访问令牌与续期令牌保存、到期前续期、代理感知传输，以及专属 coding 上游执行。`router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/auth/kimi/kimi.go:59`

New API 对 Kimi/Moonshot 的明确补充是传统渠道链：API key、多 key 轮换、模型列表同步、余额查询、OpenAI/Claude 两类入口适配和跨渠道 retry；没有观察到 Kimi 订阅 OAuth 生命周期。`QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:relay/channel/moonshot/adaptor.go:49`

### Inferred

1. **Sub2 的核心强项是账号状态与运营恢复的一体化。** 该判断由统一调度门、刷新持久化、临时冷却、额度视图和管理恢复动作共同支持，不是对任一单文件的外推。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:2139`
2. **Sub2 的 Kimi 当前更接近“模型/协议能力”，而不是“订阅账号能力”。** 依据是已观察到 Kimi 模型白名单、协议兼容和计量证据，而专属账号服务链在本轮阅读区域中未观察到。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/thinking_protocol.go:41`
3. **CLIProxyAPI 的 Kimi 链证明设备授权账号可以被做成独立产品能力，但不能据此声称 Sub2 已拥有同一能力。** 前半句来自设备授权、令牌保存和续期链；后半句受本文 Sub2 阅读边界约束。`router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/auth/kimi/kimi.go:59`、`router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/auth/kimi/token.go:81`
4. **New API 更强调通用渠道聚合与渠道级故障切换；Sub2 更强调账号级运行状态、额度和恢复操作。** New API 一侧由渠道选择与跨渠道 retry 支持，Sub2 一侧由统一账号调度门和管理员恢复动作支持。`QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:middleware/distributor.go:443`、`QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:controller/relay.go:191`、`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:148`、`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:2139`

## 按账号类型的行为矩阵

| 轴 | Sub2 Claude | Sub2 Gemini | Sub2 Antigravity | Sub2 Kimi | 三镜明确差异 |
| --- | --- | --- | --- | --- | --- |
| 1. 导入/授权入口 | 浏览器 PKCE；另有基于网页登录态的自动换取路径；区分完整授权与仅推理授权。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/oauth_service.go:64` | 浏览器 PKCE；区分代码助手、个人订阅、AI Studio 三种授权语义；后者要求自备 OAuth client。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/gemini_oauth_service.go:101` | Google OAuth PKCE，可选代理；授权后继续发现账号所需项目身份。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/antigravity_oauth_service.go:32` | 未观察到专属 OAuth/设备授权入口；观察到通用 API-key 兼容能力。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:frontend/src/composables/useModelWhitelist.ts:179` | CLIProxyAPI 有 Kimi 设备授权并轮询用户确认。`router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/auth/kimi/kimi.go:179` |
| 2. 凭据与敏感边界 | 保存访问令牌、可续期令牌、有效期、授权范围及组织/账号身份；授权临时态只驻留会话存储，成功后删除。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/oauth_service.go:143` | 保存令牌、有效期、授权模式、项目身份、套餐归一化信息；部分 Drive 元信息进入非核心扩展信息。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/gemini_oauth_service.go:197` | 保存令牌、有效期、账号邮箱与项目身份；刷新时合并旧的非令牌设置，避免丢失运行配置。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:1199` | 未观察到专属持久化形态；通用 API-key 账号的敏感值由统一账号凭据容器承载。 | CLIProxyAPI 将 Kimi 令牌、到期时间和设备身份写入权限受限的本地 JSON 文件。`router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/auth/kimi/token.go:81` |
| 3. 刷新/续期/恢复 | 请求前在到期窗口内刷新；有缓存、分布式锁、版本检查；失败策略可短 TTL 使用旧令牌或直接返回错误。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/claude_token_provider.go:68` | 请求前刷新并缓存；手动刷新后保留非令牌配置；缺少项目身份会阻断需要该身份的授权模式。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/gemini_oauth_service.go:531` | 自动与手动刷新；项目身份暂时发现失败时可保存新令牌、保留旧项目身份并返回警告，而不直接把账号判死。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:1212` | 未观察到 Kimi 专属续期器、刷新失败状态或重新授权入口。 | CLIProxyAPI 在到期前五分钟判定需要刷新，且要求存在续期令牌。`router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/auth/kimi/token.go:112` |
| 4. 可调度条件 | 统一要求账号启用且允许调度，并排除过期、过载、限流、临时冷却；API-key 账号还受本地额度门约束。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:148` | 同左；套餐与模型族额度可进一步影响运行选择。 | 同左；还可因上游验证、违规、认证失败、429 或内部错误进入不同恢复路径。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/antigravity_quota_fetcher.go:58` | 仅观察到通用 API-key 调度门；未观察到 Kimi 订阅额度或设备会话参与调度。 | New API 在选中渠道后从启用 key 中取一个，并可在重试时改选渠道。`QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:middleware/distributor.go:443` |
| 5. 模型范围 | 管理端可对账号查询模型，并在连接测试时应用账号级映射。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_test_service.go:209` | 授权模式和套餐决定项目/配额语义；管理端有账号模型查询入口。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:2327` | 可从上游模型响应得到逐模型额度、能力、推荐标志和模型迁移信息。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:158` | 有静态/管理白名单与模型映射；未观察到 Kimi 账号级动态发现。 | New API 可从 Moonshot 兼容模型端点同步渠道模型。`QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:controller/channel_upstream_update.go:304` |
| 6. 出站协议与传输 | OAuth 走 Anthropic Messages；API key 可走自定义 HTTPS 上游；连接测试模拟官方 CLI 客户端姿态。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_test_service.go:232` | 原生 Gemini/代码助手协议，按授权模式携带 API key 或 OAuth 身份；使用账号代理。 | 将 Claude/Gemini 入口转换到 Antigravity 上游协议，使用 OAuth 身份、项目身份、客户端姿态及代理。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/antigravity_gateway_upstream.go:1` | Kimi/第三方兼容路径可走 OpenAI chat 或 Anthropic-compatible 语义，并保留其 thinking 历史块。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/thinking_protocol.go:41` | CLIProxyAPI 的 Kimi 专属执行使用 Bearer 身份、代理感知客户端；Claude 输入可转到 coding 上游。`router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/runtime/executor/kimi_executor.go:41` |
| 7. retry/fallback 与状态反馈 | 刷新失败区分可重试、不可重试、共享 provider 配置故障和超时；不可重试错误可阻断账号调度。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_refresh_service.go:963` | 项目身份发现失败在不同授权模式下有“允许后续补齐”与“立即阻断”两类结果。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/gemini_oauth_service.go:531` | 单账号内部 retry 与账号池切换分开；错误可映射为冷却、验证、封禁、重新授权或 provider 周期隔离。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/antigravity_gateway_retry.go:1` | 未观察到专属 Kimi 错误分类回写；沿用通用 API-key/兼容上游处理。 | New API 按全局 retry 次数重新选择渠道，并把错误交给渠道状态处理。`QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:controller/relay.go:191` |
| 8. 额度/套餐/健康 | 可主动查询 OAuth 使用窗口，并以短缓存和错误负缓存控制探测风暴。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:107` | 展示共享/Pro/Flash 的日与分钟维度，且授权时归一化套餐。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:191` | 展示逐模型额度、能力、订阅等级、积分余额、转发规则和验证/封禁/重授权状态。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:198` | 未观察到 Kimi 专属套餐、余额或健康同步。 | New API 可查询 Moonshot 可用余额并换算为系统显示币种。`QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:controller/channel-billing.go:325` |
| 9. 管理端操作 | 列表可见状态、并发、RPM、当前窗口费用、活跃会话和调度评分；可刷新、测试、清限流、清冷却、重置额度、切换可调度、查模型。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:168` | 同左，另有授权能力探测和多授权模式输入。 | 同左，另有隐私状态设置、逐模型额度与人工验证语义。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:2608` | 可作为通用 API-key 账号管理；未观察到 Kimi 专属重新授权或额度面板。 | CLIProxyAPI 管理面可列出、上传、修改、下载、删除认证文件，并提供多 provider 授权入口；这是文件型凭据管理，不等同 Sub2 的账号运营视图。`router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/api/handlers/management/auth_files.go:268` |
| 10. 测试证据 | token 缓存、刷新锁、旧 token 降级、连接测试均有单测文件。 | OAuth、模型、会话、配额与多平台路径有测试。 | OAuth、刷新、额度分类、单账号 retry、内部错误惩罚、模型映射与隐私均有测试。 | 有 Kimi 协议、计价、thinking 与缓存 token 修正测试；未观察到独立账号生命周期测试。 | CLIProxyAPI 有 Kimi 代理、刷新和执行器测试；New API 有渠道模型同步与通用 retry 测试。 |

## Sub2API 分账号深读

### Claude

#### Observed

- 授权支持两条入口：标准浏览器 PKCE，以及由网页登录态代办组织发现和授权码获取；两者都可绑定代理。完整授权与仅推理授权使用不同 scope 语义。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/oauth_service.go:64`
- 授权临时态包含随机 state、PKCE verifier、scope、代理和创建时间；成功换取 token 后删除会话。失效或不存在的会话会直接拒绝。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/oauth_service.go:143`
- 请求时优先读 token 缓存；临近到期则通过统一刷新协调器续期。并发刷新由锁收敛，等待者会短暂等待缓存；刷新失败时是否继续使用旧 token 由 provider 策略决定。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/claude_token_provider.go:68`
- 缓存写入前检查持久化凭据版本，避免旧请求把过期 token 再写回缓存；刷新失败后的旧 token 使用短 TTL。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/claude_token_provider.go:121`
- 连接测试区分 OAuth、API key、云服务账号和 Bedrock。OAuth 使用 Messages beta 入口；API key 可配置自定义上游，但 URL 受安全校验。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_test_service.go:209`
- 管理员可手动刷新；保存时保留原有非令牌配置，刷新成功后使 token 缓存失效。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:1252`

#### Open Questions

1. 本轮未追踪 Claude 上游每一种 HTTP 状态到冷却时长的完整映射。
2. 本轮未观察到组织成员资格变化后是否有独立周期同步。
3. 本轮未实测网页登录态入口在 MFA、风控挑战或多组织选择时的用户体验。

### Gemini

#### Observed

- 授权区分代码助手、个人订阅和 AI Studio。前两者强制使用内置公共客户端；AI Studio 必须由部署者提供自有 OAuth client，否则在生成授权链接和换 token 两处都拒绝。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/gemini_oauth_service.go:124`
- state 必填且必须与会话一致；不同客户端模式选择不同回调姿态。token 有效期会预扣安全窗口，并设置最小下界，防止立即进入刷新风暴。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/gemini_oauth_service.go:445`
- 代码助手账号必须具备项目身份；若授权时未提供，会主动发现。发现失败最终阻断账号建立。个人订阅也会自动发现项目身份，并额外尝试以 Drive 信息识别套餐；套餐探测失败可回退，不阻断 token。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/gemini_oauth_service.go:531`
- 请求时有独立 token 缓存与刷新 provider；其整体结构与 Claude 类似，但账号资格检查限定为 Gemini OAuth。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/gemini_token_provider.go:52`
- 管理额度视图区分共享池、Pro 与 Flash，并同时呈现日级和分钟级维度。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:191`

#### Open Questions

4. 本轮未确认每一种 Gemini 授权模式的模型列表是否全部来自上游动态发现，还是部分来自维护列表。
5. 本轮未追踪套餐变化后后台自动重探的具体周期。
6. 本轮未观察到 AI Studio OAuth 与 AI Studio API key 在调度评分上的明确差异。

### Antigravity

#### Observed

- 授权为 Google OAuth PKCE，会话绑定代理；换 token 后继续发现项目身份。项目发现包含有界退避重试。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/antigravity_oauth_service.go:97`
- 刷新不仅更新 token，还尝试补齐项目身份。若新 token 有效但项目发现暂时失败，管理手动刷新路径会保存 token、保留旧项目身份、设置隐私并返回可恢复警告。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:1212`
- 额度探测首先读取可用模型；403 不直接变成管理 API 500，而是转成账号可展示的验证、违规或普通禁止状态，并可提取人工恢复链接。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/antigravity_quota_fetcher.go:58`
- 账号视图可呈现逐模型使用率、重置时间、图像/思考能力、token 上限、推荐标志、订阅等级、积分余额和旧模型迁移规则。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:158`
- 请求执行区分 Claude 与 Gemini 入口转换；出站携带 OAuth 身份、项目身份和客户端姿态，并使用账号代理。retry 层把单账号内部尝试与跨账号 fallback 分开。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/antigravity_gateway_retry.go:1`
- 管理员可查看/设置隐私状态；可清限流、清临时冷却、切换可调度和主动测试账号。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:2139`

#### Open Questions

7. 本轮未完整核实每个 Antigravity 上游错误到账号冷却时长和熔断阈值的数值表。
8. 本轮未实测需要人工验证时，验证完成后是自动恢复还是必须管理员清状态。
9. 本轮未确认积分余额是否直接参与调度评分，还是仅用于展示。

### Kimi

#### Observed

- Sub2 的模型白名单包含 Moonshot/Kimi 模型族。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:frontend/src/composables/useModelWhitelist.ts:179`
- Kimi/Moonshot 被归入需要保留历史 thinking block 的第三方 Anthropic-compatible 协议族，而非官方 Anthropic 严格过滤族。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/thinking_protocol.go:41`
- 对不具备 Responses 能力的第三方 OpenAI-compatible 上游，系统可以回落到 chat completions；相关注释明确把 Kimi 列为此类上游之一。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_gateway_chat_completions_raw.go:42`
- 使用统计兼容 Kimi 风格的缓存 token 返回，并有流式与非流式测试。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/gateway_cached_tokens_test.go:29`
- 计价层识别 Kimi K 系列及 coding 别名，但对未有可靠价格证据的旧 Moonshot 模型不强行补价。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/billing_service.go:467`
- 未观察到 Kimi 专属 OAuth handler、token provider、quota fetcher、账号测试分支或管理重授权组件。

#### 三镜补充

- CLIProxyAPI 的 Kimi 使用 OAuth2 device flow，授权轮询受上游间隔和最长等待时间约束；token 文件保存设备身份，使授权身份和请求身份保持关联。`router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/auth/kimi/kimi.go:221`
- CLIProxyAPI 的 Kimi 出站采用 Bearer token 和代理感知 HTTP client；Claude 格式输入可直接走 coding 兼容入口。`router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/runtime/executor/kimi_executor.go:58`
- New API 的 Moonshot 渠道使用 API key Bearer 身份，可按 OpenAI 或 Claude 请求格式选择不同上游路径，并能同步模型与查询余额。`QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:relay/channel/moonshot/adaptor.go:49`

#### Open Questions

10. Sub2 是否计划接入 Kimi coding 订阅 OAuth，未在本轮源码中观察到。
11. Sub2 通用 API-key 账号能否完整承载 Kimi coding 专属设备身份头，未观察到明确证据。
12. Sub2 对 Kimi OAuth 订阅额度、封禁、续期失效和重新授权的产品语义，当前均未观察到。

## Sub2 的强项与局限

### Observed strong points

- **账号状态是一等对象。** 启用、可调度、到期、过载、限流、临时冷却和本地额度共同决定是否进入候选池。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:148`
- **刷新不是简单覆盖 token。** 它包含锁、超时、重试、版本一致性、共享 provider 故障隔离、不可重试错误分类、缓存失效和调度状态通知。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_refresh_service.go:837`
- **运营恢复动作完整。** 管理端能主动刷新、测试、查模型、清限流、清冷却、重置额度和切换调度。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:2139`
- **Antigravity 的错误语义较细。** 403 被拆成需要验证、违规封禁和普通禁止；401、429、网络故障也有独立展示语义。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:229`
- **额度不是单一余额。** Claude 有时间窗口，Gemini 有模型族/速率维度，Antigravity 有逐模型能力与积分，Grok 有被动配额和账单快照。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:183`

### Observed limitations

- **Kimi 账号链未达到 Claude/Gemini/Antigravity/Grok 的专属生命周期成熟度。** 当前证据集中在模型、协议、计价和通用 API-key 兼容。
- **部分关键元数据发现依赖授权后的额外上游调用。** Gemini/Antigravity 的项目身份发现失败会产生阻断或降级，增加了授权成功但账号尚不可运行的中间态。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/gemini_oauth_service.go:531`
- **错误与状态体系复杂。** 同一账号可能同时存在持久状态、数据库冷却时间和缓存冷却状态，需要管理视图准确解释其优先级。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:2186`
- **本轮未观察到完整端到端测试覆盖每种 provider 的“授权—首次请求—过期—刷新—失败—人工恢复”全旅程。** 已观察到大量分段单测，但不能据此声称完整旅程已被覆盖。

## 可转化为 HUAKAI 验收标准的行为

以下只描述可验收结果，不给出 HUAKAI 实现方案。

1. **授权会话隔离**：授权入口必须生成不可预测的会话关联值与 PKCE；回调缺失、过期或不匹配时不得保存任何账号凭据。
2. **敏感值边界**：管理列表、日志、错误和模型探测结果不得返回访问令牌、续期令牌、session cookie 或 API key；重新授权成功后仅在受控持久层更新。
3. **凭据原子更新**：刷新成功必须同时保存新 token 与有效期，并保留账号的非令牌运行配置；缓存不得在持久化失败时提前发布新 token。
4. **并发刷新收敛**：同一账号并发遇到临期 token 时，只允许一个上游刷新；其他请求等待结果或使用仍有效的旧 token，不得形成刷新风暴。
5. **旧写覆盖防护**：较早请求不得把旧 token 覆盖到较新持久化版本或缓存。
6. **失败分级**：至少区分可重试网络/5xx、429、认证失效、不可恢复授权错误、共享 provider 配置故障、人工验证和明确封禁；不得把共享故障批量误判为所有账号失效。
7. **调度门可解释**：账号不进入候选池时，管理端必须能指出是手动禁用、过期、并发满、额度耗尽、限流、过载、临时冷却、认证失败还是人工验证。
8. **恢复动作闭环**：管理员必须能执行连接测试、手动刷新、重新授权、清除可恢复冷却、切换可调度，并看到动作后的最新状态。
9. **模型范围可验证**：账号级模型列表必须标明来源是上游动态发现、套餐推导还是管理员配置；探测失败不得伪装为空能力。
10. **额度新鲜度可见**：额度/套餐/健康结果必须携带更新时间、数据来源和错误状态；探测失败时允许展示旧快照，但不得展示为实时成功。
11. **代理一致性**：授权、token 刷新、项目/套餐探测、模型发现和实际推理必须使用账号绑定的同一代理策略，除非管理端明确显示例外。
12. **Kimi 能力分级**：若仅支持 API-key 兼容通道，应明确标为“通用渠道账号”；若宣称支持 Kimi coding 订阅账号，则必须另行验收设备授权、设备身份持久化、自动续期、重新授权、额度/封禁状态和专属出站身份。
13. **retry 不重复计费**：单账号内部 retry 与跨账号 fallback 必须可区分；只有最终被接受的上游用量进入一次计费，失败尝试保留审计但不重复扣费。
14. **测试旅程**：每个专属账号类型至少有一条端到端验收旅程覆盖授权、首次请求、临期刷新、刷新锁竞争、429 冷却、401 重新授权、管理员恢复和敏感值脱敏。

## 真实测试证据与证据缺口

### Observed test evidence

- Claude token provider 有缓存命中、锁等待、刷新策略和版本检查测试文件。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/claude_token_provider_test.go:1`
- Gemini 有 OAuth、模型列表、会话和多平台行为测试。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/pkg/geminicli/oauth_test.go:1`
- Antigravity 有 OAuth、刷新、额度、单账号 retry、内部错误惩罚和模型映射测试。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/antigravity_single_account_retry_test.go:1`
- Kimi 有 thinking 协议、缓存 token 统计、请求兼容和计价测试，但这些证明的是协议/计量能力，不是订阅账号生命周期。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/gateway_cached_tokens_test.go:29`
- CLIProxyAPI 有 Kimi 代理、刷新和执行器测试。`router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/auth/kimi/kimi_refresh_test.go:1`
- New API 的渠道模型同步测试覆盖待新增/移除模型的差异计算，但本轮未观察到 Kimi OAuth 测试。`QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:controller/channel_upstream_update_test.go:1`

### 未观察到

- 未观察到 Sub2 Kimi 专属授权、续期、额度、封禁、重授权的实现或测试。
- 未观察到三项目对同一账号链的统一黑盒契约测试。
- 未观察到 Sub2 每个 provider 都覆盖完整管理恢复旅程的单一端到端测试。
- 未观察到网络隔离、代理失效、系统时钟偏差和持久层部分失败同时发生时的跨 provider 一致性测试。

## Truth-first 汇总

本文的真实观察是：Sub2API 本地 SHA 中 Claude、Gemini、Antigravity 均有专属账号生命周期和运营闭环，而 Kimi 只观察到通用渠道、协议、模型、计量和计价能力；CLIProxyAPI 明确补足了 Kimi 设备授权与续期链，New API 明确补足了 API-key 渠道模型同步、余额与 retry 证据。合理推断共 4 项，集中在成熟度和产品侧重比较；Open Questions 共 12 项，均保留为未核实问题，没有把“未观察到”写成“不支持”。

Source files read: sub2api/backend/internal/service/oauth_service.go; sub2api/backend/internal/service/claude_token_provider.go; sub2api/backend/internal/service/gemini_oauth_service.go; sub2api/backend/internal/service/gemini_token_provider.go; sub2api/backend/internal/service/antigravity_oauth_service.go; sub2api/backend/internal/service/antigravity_token_refresher.go; sub2api/backend/internal/service/antigravity_gateway_retry.go; sub2api/backend/internal/service/antigravity_gateway_upstream.go; sub2api/backend/internal/service/antigravity_quota_fetcher.go; sub2api/backend/internal/service/token_refresh_service.go; sub2api/backend/internal/service/account.go; sub2api/backend/internal/service/account_usage_service.go; sub2api/backend/internal/service/account_test_service.go; sub2api/backend/internal/service/thinking_protocol.go; sub2api/backend/internal/service/openai_gateway_chat_completions_raw.go; sub2api/backend/internal/service/gateway_cached_tokens_test.go; sub2api/backend/internal/service/billing_service.go; sub2api/backend/internal/handler/admin/account_handler.go; sub2api/frontend/src/composables/useModelWhitelist.ts; CLIProxyAPI/internal/auth/kimi/kimi.go; CLIProxyAPI/internal/auth/kimi/token.go; CLIProxyAPI/internal/auth/kimi/kimi_refresh_test.go; CLIProxyAPI/internal/runtime/executor/kimi_executor.go; CLIProxyAPI/internal/cmd/kimi_login.go; CLIProxyAPI/internal/auth/claude/anthropic_auth.go; CLIProxyAPI/internal/auth/claude/token.go; CLIProxyAPI/internal/auth/antigravity/auth.go; CLIProxyAPI/internal/runtime/executor/antigravity_executor.go; CLIProxyAPI/internal/runtime/executor/gemini_executor.go; CLIProxyAPI/internal/api/handlers/management/auth_files.go; new-api/constant/channel.go; new-api/relay/channel/moonshot/adaptor.go; new-api/controller/channel-billing.go; new-api/controller/channel_upstream_update.go; new-api/controller/channel_upstream_update_test.go; new-api/middleware/distributor.go; new-api/controller/relay.go
Lane: specifier
Agent: GPT-5 Codex / root
UTC timestamp: 2026-07-16T09:47:07Z
