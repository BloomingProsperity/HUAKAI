# Claude OAuth serving 路径接线 + body 拟真(system/billing 注入)—— Claude 平行计划

> 本文件依 CLAUDE.md #10 独立起草(未看 codex 版)。配套:`...-codex.md`。
> 依 §16 三镜 + §17 模块配合 + §11 clean-room。

## 背景(已亲自读源码坐实)
- 反转 adapter = 每 vendor 一个 `*_session.go`(HUAKAI 与两镜一致;无通用件)。
- `anthropic/oauth_session.go` 的 `OAuthSessionAdapter` **已存在**:收 OAuthAccessToken/SessionToken、Bearer 注入、Anthropic-Version、beta 白名单、**Claude Code 设备指纹 + 静态头 + session 头**均已做。
- **两处缺口**:
  - A(解阻):registry 未注册它([default.go:234](../../internal/provider/registrydefault/default.go#L234) fail-closed);Claude OAuth 账号无 serving 路由 → 采集到的 `sk-ant-oat` 转不出去。
  - B(硬化):body 原样透传(oauth_session.go:54),**不注入固定 CLI system prompt、不注入 billing 归因块、不改写工具名**;`mimicryidentity/identity.go:109-125` 生产计划仅启用 `MetadataUserID`,`SystemRewrite`/工具名重写/tools-tail cache 均休眠。

## 目标形态(对齐两镜条件式深拟真)
一个池内 Claude OAuth 账号,经 HUAKAI 转发到 `api.anthropic.com/v1/messages?beta=true`,出站 body 满足:
- **非官方客户端入站** → system 首块被覆盖为 [billing 归因块 + 固定 CLI 身份块 + 合并静态提示块],原 system 下沉为首条 user;工具名同步改写;cache 断点补齐(限 4)。
- **官方 Claude Code 直发入站** → 跳过覆盖(真 CLI 自带,套壳=指纹异常反被封),仅走既有头/设备指纹。

## 切片拆分(建议 A 先行解阻,B 紧随硬化)
### 切片 A:serving 路径接线(功能解阻)
1. 定义/复用 `ProtocolAnthropicClaudeSession` family;registry 注册 `OAuthSessionAdapter`。
2. 路由:Claude OAuth/session 账号(credentialstore authMode)→ 该 family(照 codex OAuth 账号定端点的同款机制)。
3. `?beta=true` 处理:OAuth 路默认应带(真 CLI 默认 beta),确认 `EndpointForCredential` 与 beta query 协作。
4. §17 模块配合:选号→OAuth adapter 物化→转发→计费结算→配额→并发槽释放,全链断言。

### 切片 B:body 拟真(反检测)
1. 在 mimicry compose plan 里为 anthropic_claude_session 启用 `SystemRewrite`(固定 CLI 身份 + billing 块)+ 工具名重写 + cache 断点。
2. 固定内容 clean-room 自研(不抄两镜字面);billing 块字段契约见"决策点2"。
3. 条件式判别(决策点3):按入站 UA 识别官方 Claude Code,真 CLI 跳过覆盖。

## 成功判据
- E2E(live,我跑):采集的 Claude OAuth 账号经 HUAKAI → api.anthropic.com 返回 200 且内容非空。
- 出站 body 断言:非官方入站时 system 首块含 CLI 身份 + billing 块;官方入站时不覆盖。
- §14 变异:删 system 注入 → 断言红;删 billing 块 → 断言红;删条件式判别(对官方也覆盖)→ 断言红。
- §17:hold 不泄漏、槽回 0、配额计入、并发子用例无泄漏。
- clean-room:引两镜仅 file:line + 去标识化转述。

## 爆炸半径
- 改 `anthropic/`(启用 body 改写)+ registry + mimicry compose plan;**不动** apikey passthrough 路径;**不动**我们自己的 auth-core(登录/2FA/token)。
- 默认行为翻转(注册新 family + 启用拟真)= CLAUDE.md #2 Owner-gated,需拍板。

## 决策点(surface Owner)
1. **A+B 一起 vs 先 A 后 B**:A 让账号先转起来(可手测),B 才防封。建议**先 A 解阻(小、纯功能)、B 紧随**,两切片各自过门。
2. **billing 块契约**:两镜当前均不带 CCH 签名(仅版本/内容指纹/CLI 入口)。默认对齐"不带 CCH";若要补 CCH 需 Owner 定。
3. **官方客户端判别口径**:按入站 UA(真 `claude-cli/*` 跳过覆盖);是否再叠加其它信号(beta token 集合)?
4. **§16 tiebreaker**:两镜分歧处默认 sub2api(成熟体)。

## 三镜对照(§15)
- CLIProxyAPI@26d45fd:`claude_executor.go:1885-1957`(三块 system + 原 system 下沉)、`claude_signing.go:19-40`(归因签名)、`claude_executor.go:1339-1444`(工具名同步)、`cloak_utils.go:41-50`(入站 UA 判别跳过)。
- sub2api@12d811b:`gateway_forward.go:168-248`(OAuth 非官方分支)、`gateway_claude_oauth_body.go:769-939`(三块 + 消息搬移)、`gateway_billing_block.go:26-94`(归因块,当前不含 CCH)、`gateway_tool_rewrite.go:122-285`。
- new-api:纯 API-key 网关,无 OAuth 反转深拟真 → 无等价(§16 no-equivalent,需 source-cited 补注)。
- HUAKAI delta:设备指纹已 per-account 钉定(DEVPIN)+ beta 白名单(DM-03)已领先;缺的是 body 层 system/billing——补上即对齐。

## 工时估
- 切片 A:~1 人日(注册 + 路由 + serving E2E)。
- 切片 B:~2-3 人日(system/billing 自研内容 + 条件判别 + 变异测试 + body 断言)。
