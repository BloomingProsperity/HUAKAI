# env 配置 → 后台设置 迁移 backlog(2026-06-30)

触发:Owner「OAuth 配置该搬后台让运营自助配,你还要去查查别的模块有没有这样的问题」。
方法:8-agent workflow 系统性分类全部 env 配置,对照 sub2api(其把运营配置放后台设置 UI、密钥写后不回显)。

## 总账:113 项 → **66 该搬后台 / 44 合理留 env / 3 已有设置(env 冗余)**

核心问题坐实:HUAKAI 一大批**运营者面向的功能配置锁死在 env**(改一下要重部署),而 sub2api 把这类放
**后台管理设置 UI**(运营自助配、随时改、密钥写后不回显 `*_configured`)。

## 进度(2026-07-01 更新)——Tier 1 已全部落地

settings-first-with-env-fallback 架构落地,Tier 1 全部完成(每项零行为变更:设置为空 → 与迁移前一致):
- ✅ **OAuth 7 家凭证 + 端点URL + 字段映射 + min_trust_level**:两 secret-key 拆分 `oauth_providers_config`
  (公开 JSON)+ `oauth_providers_secrets`(密钥,加密+脱敏);`OAuthService` 加请求期 resolver
  (settings-first 覆盖 env、未命中回退 boot 静态)。**Tier 2 的端点URL/字段映射一并覆盖**(config JSON 可带任意字段)。
- ✅ **telegram bot token**:secret-key,登录/绑定端点请求期 settings-first。
- ✅ **captcha turnstile secret**:secret-key,verifier + 配置门请求期 settings-first,消除双源一致性校验。
- ✅ **registration mode**:`registrationMasterGate` 让 registration_enabled/invitation_required
  后台设置真正驱动注册门(密码+社交两路),不再只在 sitepublic 只读展示。
- ✅ **linuxdo min_trust_level / trust_level_field**:并入 OAuth config JSON 的数值/字符串字段。

剩:Tier 3(payment HMAC,money,Owner-gated 待拍板)。下面为原始清单存档。

## Tier 1 — 高优先(运营上线必配,sub2api 已后台化)
- **全部 OAuth provider 凭证**(google/github/qq/dingtalk/discord/nodeseek/linuxdo,各 client_id/client_secret/redirect_uri):
  config.go:305-446 仅从 env 读、boot 时一次性 build(lifecycle.go:330)→ 改要重部署。
  sub2api 全后台化 + DB 覆盖 env(`firstNonEmpty(settings, cfg)`)+ secret 写后不回显。
- **HUAKAI_TELEGRAM_LOGIN_BOT_TOKEN**:bot token(密钥)还留 env(routes.go:297/622),而 bot username 我已搬进设置;
  应同样以 secret-key 写后不回显搬后台,与 username 同源。
- **HUAKAI_CAPTCHA_TURNSTILE_SECRET**:**半截配置**——captcha_enabled/provider/site_key 已在设置,唯独 secret 留 env
  (config.go:92),还得跨双源做一致性校验。搬后台后单一数据源。
- **HUAKAI_USER_REGISTRATION_MODE**:**半截+未接通**——KeyRegistrationEnabled/KeyInvitationRequired 已在设置但目前
  只在 sitepublic 只读展示(handler.go:41)、**不驱动真正的注册门**(注册门读 env config.go:171→service.RegistrationMode)。
  即运营在设置里改 registration_enabled 其实不生效。应让设置真正驱动注册门。
- **HUAKAI_LINUXDO_OAUTH_MIN_TRUST_LEVEL**:登录准入门槛(挡低等级账号),运营业务策略,该后台。

## Tier 2 — 中/低优先
- OAuth 各 provider 的 auth_url/token_url/user_url/jwks/issuer 等端点 URL(有合理默认,多数运营不改)。
- nodeseek/linuxdo 的 *_FIELD 字段映射(高级项)。

## Tier 3 — Owner-gated(money,高风险)
- **HUAKAI_PAYMENT_HMAC_SECRETS**:支付 webhook 验签密钥(验签 bug=伪造充值)。sub2api 放后台但用专用加密 + 写后脱敏;
  HUAKAI 须另立专用加密 secret key、不能塞进现有明文 payment_provider_config。money/凭据高风险,Owner 拍板。

## 合理留 env(44,不该搬)
部署期基建/主密钥/安全项:HUAKAI_DATABASE_URL、CREDENTIAL_KEY_B64/ID、SESSION_SIGNING_KEY_*、AUDIT_*、
RELEASE_MODE、AUTO_MIGRATE、CORS_ALLOWED_ORIGINS、TRUSTED_PROXY_CIDRS、TEST_DATABASE_URL/REDIS_URL、
TRANSPORT_SIDECAR_SOCKET、POOL_SELECTOR_SALT 等——搬后台反而危险(主密钥不该入库 UI)。

## 推荐架构(对齐 sub2api)
settings-first-with-env-fallback:配置读取改为「先读 platformsettings,空则回退 env」——
back-compat(老 env 部署不破)+ 运营可在后台覆盖。secret 类沿用 HUAKAI 已有的 secret-key 写后不回显机制
(platformsettings/secret_keys.go),不回显明文。OAuth 这种 boot 时一次性 build 的还需改成请求期/可刷新读取。
