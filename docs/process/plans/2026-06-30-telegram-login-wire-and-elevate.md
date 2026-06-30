# Telegram 登录:接线 + 升华(fusion-upgrade)

日期:2026-06-30 · 作者:Claude PM · 触发:Owner `/loop`「Telegram 接线,先排查接入方法,
接线完毕做端到端/点到点测试(尤其逻辑),核对能否和三个借鉴项目一样的运行逻辑」+「要升华,
优化成为自己的东西」。

## 1. 排查结论(已亲核真码)

### HUAKAI 后端:telegram 登录**后端已完整**
- 端点 `POST /v1/auth/telegram-login`(`auth_handler.go:180` 挂载,`:657` handler)。
- 请求 `{tenant_id, params:map[string]string, device_info?}`;`params` 即 Telegram Login Widget
  回传的字段集(id/first_name/last_name/username/photo_url/auth_date/hash)。
- 校验:`telegramauth.VerifyWidget`(`telegramauth.go:15`)——`secret=sha256(botToken)`,
  对排序后的 `k=v\n` data-check-string 做 HMAC-SHA256,比对 `hash`;另含 `auth_date`
  时效窗(`maxAge`,默认 24h)+ 未来时间戳拒绝。
- bot token 来源 = env `HUAKAI_TELEGRAM_LOGIN_BOT_TOKEN`(`routes.go:618`),**密钥,永不入库/下发**。
- 成功:`ApplyVerifiedSocialIdentity`(合成邮箱 `telegram:<id>`)→ 建 session → `{user, session}`;
  含新设备确认分支 `handleDeviceConfirmationRequired` + 审计 `user_social_login_*`。

### 缺口(为什么前端今天接不上)
- Telegram Login Widget 渲染按钮**需要 bot username**(公开值,即 `t.me/<name>` 里那个名)。
- 公开 `GET /v1/site/config`(`sitepublichttp/handler.go` 的 `stringKeys` allowlist)**未暴露**任何
  telegram 字段 → 前端拿不到 username → 渲染不出 widget。
- 前端 `loginEnhance.ts:172` 已把 `telegram` 列入 `BACKEND_SOCIAL_PROVIDERS`,但 `LoginPage.tsx:359`
  对所有 oauthProviders 一律渲染「调 oauth-init」按钮——telegram **不走 oauth-init**,会是坏按钮,必须特判。

### 三借鉴项目运行逻辑对照(§15/§16)
| 项目 | 是否有 telegram 登录 | 校验算法 | 配置 | 前端 |
|---|---|---|---|---|
| **new-api** `@QuantumNous` `controller/telegram.go` | 有 | `checkTelegramAuthorization`:`sha256(token)` 当 HMAC 密钥、排序 `k=v\n`、比对 hash —— **与 HUAKAI 同款** | `TelegramBotToken`(密钥)+ `TelegramBotName`(公开)+ `TelegramOAuthEnabled`,均存通用 OptionMap | **占位 stub**:`telegram-bind-dialog.tsx:80` 注释 "would require the react-telegram-login library",只有虚线占位框,**未真正注入脚本** |
| **sub2api** | 无(仅 release.yml 提及) | — | — | — |
| **CLIProxyAPI** | 无(仅 README 提及) | — | — | — |

结论:三镜中**只有 new-api 有 telegram 登录**;其核心校验算法与 HUAKAI 完全一致(同款 Telegram 官方 widget 协议),
但 new-api 登录要求账户**先 bind**、且**前端 widget 是半成品**。

## 2. 升华设计(HUAKAI 自己的东西,三维 delta)

### 架构升级:密钥/公开值清晰分离 + allowlist 披露边界
- bot **token** = 密钥 = 仅 env,永不入库、永不经任何 API 下发。
- bot **username** = 公开值 = 新增 platformsettings key `telegram_bot_username`,经 sitepublic
  **投射式 allowlist** 显式下发(白名单,非黑名单)。
- 对比 new-api:token 与 name 同塞通用 OptionMap 同一面,token 经 admin option API 可读回;
  HUAKAI 把密钥彻底挡在公开/可读面之外。

### 算法升级:重放防护 + 输入硬化
- `auth_date` 时效窗 + 未来戳拒绝:new-api `checkTelegramAuthorization` **只校验 HMAC、无任何时间校验**
  → 泄露的有效 widget payload 可被**永久重放**;HUAKAI 已拒绝过期/未来戳。
- bot username 服务端字符集硬化:仅 `[A-Za-z0-9_]`、长 5–32、须 `bot` 结尾(Telegram 真实命名规则)。
  既挡配置笔误,又确保该值注入前端 `data-telegram-login` 属性时**不可能携带引号/尖括号**(纵深防御 XSS)。
  new-api 不校验,原样存。

### 生态升级:登录即注册 + 新设备确认 + 审计 + 真接 widget
- `ApplyVerifiedSocialIdentity` 原子建号(登录即注册),无需 new-api 的单独 bind 步。
- 新设备 telegram 登录走设备确认(new-api 无)。
- 审计事件 succeeded/failed。
- 前端**真正接好官方 widget**(new-api 留 stub):脚本懒加载、fail-closed(镜像既有 Turnstile 模式
  `LoginPage.tsx:457`),并把 telegram 从 oauth-init 按钮特判出来。

## 3. 落地切片(本分支 feat/fe-wire-users-mod 叠加,telegram 是「全接线一个不漏」最后一块)

### 后端(无迁移:platform_settings 是 key-value 表,加 key 仅注册)
- `platformsettings/types.go`:加 `KeyTelegramBotUsername` const + 进 `orderedSettingKeys` +
  `defaultSettingValueMap`(默认 `""`=关闭)+ `ValidateValue` 分支 + `validateTelegramBotUsernameValue`。
- `sitepublichttp/handler.go`:`stringKeys` 加 `{"telegram_bot_username", KeyTelegramBotUsername}`。
- 测试:validator 变异测试(空/超长/非法字符/缺 bot 后缀各自 RED)+ sitepublic 暴露该字段。

### 前端
- `siteConfig.ts`:`RawSiteConfig.telegram_bot_username` + `SiteConfig.telegramBotUsername` 解析。
- 新 `telegramLogin.ts`(纯逻辑):`telegramLoginRenderable(cfg)`(telegram∈providers && username 合法)、
  `validateTelegramBotUsername`、widget 回调 `params` 规整(把 widget user 对象映射成后端 `params` map)。
- 新 `telegramLoginApi.ts`:`telegramLogin(tenantId, params, deviceInfo?)` → POST `/v1/auth/telegram-login` → `{tokens,user}`。
- 新 widget 组件:懒加载 `telegram.org/js/telegram-widget.js`,`data-onauth` 回调 → POST → `setSessionTokens` → 跳转;fail-closed。
- `LoginPage.tsx`:社交按钮行**排除 telegram**(特判),改在下方渲染 widget 组件。
- 测试:telegramLogin 纯逻辑 §14 变异。

### 端到端/点到点逻辑测试(Owner 重点)
- 后端集成:构造**有效** widget payload(用测试 bot token 真算 HMAC)→ 端点 200 + 建 session;
  篡改 hash/过期 auth_date → 拒。证「与 Telegram 官方协议 + new-api 同款校验」端到端成立。
- 前端:params 映射 + renderable 判定纯逻辑。

## 4. 成功标准
- 后端 + 前端 telegram 登录端到端打通(operator 设 env token + admin 设 username + oauth_providers 含 telegram 即可用)。
- 端到端测试证明校验逻辑与 Telegram 官方协议一致(= 与 new-api 同款),且 HUAKAI 多三层升级。
- 全门绿(go test ./... + quality-gate + vitest),对抗审查零 S0/S1。

## 5. Owner 须知(部署侧,非代码)
- operator 三步开启:① env `HUAKAI_TELEGRAM_LOGIN_BOT_TOKEN=<BotFather token>`;
  ② admin 设置 `telegram_bot_username=<bot 名>`;③ `oauth_providers_enabled` 含 `telegram`。
- BotFather 里须把 bot 的 Login Domain 设为本站域名(Telegram 官方 widget 要求)。
- 若站点启用严格 CSP,放行 `script-src https://telegram.org` + `frame-src https://oauth.telegram.org`。
- 外部脚本 `telegram.org/js/telegram-widget.js` 与既有 Turnstile 外部脚本同类(已有先例),
  且客户端数据一律服务端 HMAC 校验(不信任前端),信任边界正确。
