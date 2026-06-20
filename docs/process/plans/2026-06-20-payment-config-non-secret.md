# payment_provider_config 改判为非密钥配置(Owner 拍板"放开")

## 背景
PR #69(审计 S1 secret-mask)修复 moderation 外部审核 bearer 密钥读路径明文泄露时,我**顺手把
`payment_provider_config` 也加进了 `secretSettingKeys`**(防御性,怕它将来承载支付凭据)。其结果:
管理员读平台设置时整块被脱敏成 `value_configured: true`,看不到具体支付配置。这是当时记的 S3
("从严脱敏保留与否")。Owner 现拍板 **放开**(当非密钥配置正常显示)。

## 真码核实(放开是否安全)
- 存储 schema 是**封闭**的:`validatePaymentProviderConfigValue`(types.go:442)只接受
  `{manual,taobao}`,每个仅 `{enabled bool, checkout_url string}`——**无任何密钥/凭据字段**。
- 写路径(cmd/gateway/payment_provider_config.go)与运行时解析器只读写 manual/taobao 的
  enabled+checkout_url,不碰其它字段。
- 读路径是 **admin-only**(controlhttp 平台设置仅 platform_admin 可读)。
- 项目已有惯例:真密钥单独立 key(moderation_external_api_keys 就是独立 secret key)。
- 结论:`payment_provider_config` 本就非密钥,过度脱敏只伤运维(读不到支付开关/URL),放开安全。

## 范围(纯减项,不动 money 写路径/schema)
1. `secret_keys.go`:从 `secretSettingKeys` 移除 `KeyPaymentProviderConfig`(只留 moderation),
   并注释说明"真支付密钥应另立专用 secret key,不塞进这张开关表"。
2. 翻转 3 处守护测试,使其仍**判别**(变异=把 payment 加回密钥集 → 全 RED):
   - `TestIsSecretKeyClassifiesCredentialKeys`:断言 payment 不判为密钥类。
   - `TestAuditPayloadRedactsAllSecretKeys`:断言 payment 配置(checkout_url)进入审计 payload(可追溯)。
   - `TestHandlerGETListMasksSecretKeysButNotPublicKeys`:断言读路径原样返回 payment 配置、不带 value_configured。

## 成功标准
- moderation 密钥仍脱敏(读路径 + 审计);payment 配置 + site_name 正常明文返回。
- 3 翻转测试变异(payment 加回密钥集)全 RED(已验)。
- platformsettings + controlhttp 全量 + cmd/gateway 构建 + codebudget 全绿(已验)。

## blast radius / 可能出错
- 纯移除一个 map 条目 + 测试翻转 + 注释,不动 money 写路径、不动 schema、不动 moderation 脱敏。
- Owner 已显式拍板此 default-behavior flip(满足 #2 gate)。
- 残留风险(低、admin 自伤):校验器对额外字段返回 verbatim,管理员若经 raw API 故意往配置里塞密钥,
  放开后会回吐——但读是 admin-only(回吐给设置者自己),且违背 schema 意图;真密钥应另立 secret key。
  作为 hardening follow-up 可让校验器 re-marshal 收口(剥离额外字段),本切片不含。
