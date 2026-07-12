# credential 采集流状态机 运行逻辑

> 上游账号凭据的采集:三条入口(OAuth PKCE 授权码 / OAuth 设备码 / CLI 导入)汇合到统一的
> Finalize 落库。本文记各环节**怎么配合 + 状态怎么传 + 失败怎么补偿**。相关
> [relay-forwarding.md](relay-forwarding.md)(凭据物化后如何被转发消费)。

## 1. 请求/操作生命周期(数据流)

三入口,同一个 Finalize 汇合:

1. **OAuth PKCE 授权码**(`oauth.go` startPKCEOAuthFlow → CompleteOAuthCallback):
   - start:生成 state + PKCE verifier/challenge,**session 存 StateHash(非明文 state)**,返回授权 URL;
   - callback:校验 state 匹配 → 用 code+verifier 向 IdP 换 token → 产 `CredentialCandidate`。
2. **OAuth 设备码**(`oauth_devicecode.go`,kimi/openai-codex 等):start 拿 device_code + user_code →
   轮询 token 端点(处理 authorization_pending/slow_down)→ 产 CredentialCandidate。
3. **CLI 导入**(`cli_import.go` ParseCLIImportContent/ParseCSVImportContent):跳过 OAuth,直接解析
   已有 token 文件/CSV(含嵌套 `{token:{...}}` 扁平化 + expiry→expires_at)→ 产 CredentialCandidate。
4. **Finalize 汇合**(`finalizer.go` Finalize):三入口产出的 candidate 统一走
   ValidateCandidate → creator.Create(credentialstore 物化,AES 信封加密)→ markFinalized(session)。

## 2. 关键配合点表

| from→to | 传什么 | 配合关系 | 配合错的后果 | file:line |
|---|---|---|---|---|
| start→session store | flowID / StateHash / PKCE verifier | 存待 callback 取回校验;state 存 hash 非明文 | 明文存 state→泄露可伪造;verifier 丢→换 token 失败 | oauth.go startPKCEOAuthFlow |
| callback→session | 回传 state | `OAuthStateMatches(session.StateHash, state)` 比对(CSRF 防护) | 不校验→CSRF 注入他人授权码 | oauth.go:158 |
| callback→exchanger | code + verifier | 向 IdP 换 token → candidate | verifier 不传→PKCE 校验失败 | oauth.go CompleteOAuthCallback |
| 各入口→finalizer | CredentialCandidate(vendor/authMode/payload) | 统一 Finalize 入口(三入口同构) | candidate 字段缺→ValidateCandidate 拒 | finalizer.go:81 |
| finalizer→credentialstore | CreateCredentialInput | 物化 credential(AES 信封 + AAD 绑租户) | 见 §3 非原子 | finalizer.go:102 |
| finalizer→session | credentialID | markFinalized(consumed 守卫) | 见 §3 补偿 | finalizer.go:117 |

## 3. 失败协作

| 场景 | 涉及模块 | 怎么协作补偿 | file:line |
|---|---|---|---|
| state 不匹配(CSRF/过期) | callback↔session | UpdateStatusFrom→StatusFailed(state_mismatch),不换 token | oauth.go:158-159 |
| ValidateCandidate 失败 | finalizer↔session | MarkFailed + finalizeWriteCtx **脱钩请求 ctx**(防客户端取消致 flow 卡 consumed 非终态) | finalizer.go:93-100 |
| Create 后 markFinalized 失败 | finalizer↔session | retry 3 次收窄窗口→全败返回 CredentialCreatedFinalizeError;expires_at 惰性过期兜底孤儿 | finalizer.go:117-119,130-148 |
| markFinalized 全败后客户端重试 | session BeginFinalize | **consumed_at IS NULL 守卫**:flow 已 consumed→BeginFinalize 不放行→**不会重复 Create** | session_store.go:397 |
| 重复 Create(极端) | credentialstore | active-mode 唯一索引兜底(uq_account_credentials_active_mode)+ 采集幂等键唯一索引 | migration 0016 / 0019 |
| OAuth 无已验证邮箱 | finalizer↔用户建号 | 补邮箱建号闭环(无状态 HMAC token),不阻断采集 | 见 [[oauth-no-email-signup-wired-2026-07-01]] |
| OAuth 错误回显 | exchanger | oauthErrorSummary 消毒(折叠+删控制字符+按 rune 截断),不泄上游响应体 | credentialacq OAuth 消毒(#221) |

**Finalize 非原子的现状(#4,已妥善)**:Create + markFinalized 是两步跨 DB 写,非真原子;但补偿完善——
consumed_at 守卫防重复 Create(核心)+ 唯一索引双保险 + retry + expires_at 兜底 + 脱钩写防卡死,
实际无重复凭证/静默灾难风险(详见 docs/process/reviews/2026-07-11-pre-launch-s1-verification.md 上下文)。

## 4. 三镜对照

HUAKAI 采集流的**配合自洽点**:三入口统一汇合到单一 Finalize(降低分叉)、state 存 hash 非明文、
Finalize 非原子但 consumed 守卫 + 唯一索引 + retry + 惰性过期多重补偿、OAuth 错误消毒防泄露。

> **诚实标注**:三镜(sub2api / new-api / CLIProxyAPI)采集流的逐环节对照(尤其各自 OAuth
> callback 的 CSRF/state 处理、token 物化的原子性手法、设备码轮询节奏)需专项 specifier-lane 真读
> 源码后补(§12 禁凭记忆臆造三镜做法)。本文暂记 HUAKAI 侧自洽逻辑,三镜逐点对照留后续增补——
> 触及采集流下一切片时按 §16 补齐。

## 5. 已知配合缺口(Owner-gated 后续)

- **多版本 KEK 密钥环未落地**:凭据 AES 信封的 KEK 生产单版本;轮换会致旧密文解不开。已有启动期
  fail-closed 自检(key_selfcheck.go)把"轮换致全瘫"前移为响亮启动失败。**运营约束:多版本密钥环
  落地前禁止轮换 CREDENTIAL_KEY_ID/_KEY_B64**。多版本密钥环是登记的后续切片。
- **Finalize 真原子**:当前非原子 + 多重补偿(§3),实际无风险;若要真原子需把共享 tx 穿过
  credentialacq session store + credentialstore.Create(触 credential-store 内部,auth 相邻敏感),
  属 deliberate slice 非 quick-win,留后续。

## 6. 配合点测试清单

| 测哪个配合 | 构造条件 | 判别断言 |
|---|---|---|
| state CSRF 防护 | callback 带错 state | flow→failed(state_mismatch),不换 token |
| consumed 守卫防重复 Create | markFinalized 失败后重试 Finalize | BeginFinalize 不放行,不第二次 Create |
| Finalize 幂等 | 已 finalized 的 flow 再 Finalize | 返回 AlreadyFinalized(既有 credentialID) |
| 脱钩写防卡死 | ValidateCandidate 失败 + 请求 ctx 取消 | MarkFailed 仍完成(flow 不卡 consumed 非终态) |
| cli_import 嵌套 token | `{token:{access_token,expiry,refresh_token}}` | 扁平化出 access/refresh + expiry→expires_at(见 Antigravity G5 变异证) |
| OAuth 错误消毒 | 上游返含控制字符/超长错误体 | 回显被折叠/删控制字符/截断,不泄原文 |
