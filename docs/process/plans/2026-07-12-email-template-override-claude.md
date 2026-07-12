# B6 鉴权邮件模板覆盖(零 schema + fail-safe)— Claude 计划 2026-07-12

## 背景与三镜对照(§16)

- **sub2api**(默认跟法):模板覆盖存设置键 `notification_email_template:<event>:<locale>`,
  `{{placeholder}}` 正则替换,内置官方模板为真相源;保存时校验,**渲染失败回退内置正文**
  (user_service.go:1215 "falling back to built-in body")。
- **new-api**:邮件正文硬编码于 common 包,无模板覆盖机制(无等价物)。
- **CLIProxyAPI**:纯 relay,无邮件模块(无等价物)。

## 目标

运营者可自定义 4 类鉴权邮件(verification / password_reset / device_confirmation / oauth_code)
的主题与 HTML 正文;不改 schema、不破坏 auth 邮件送达(任何覆盖异常回退内置默认)。

## 设计

1. **存储**:复用租户级邮件设置存储(`email.SettingsStore`,k/v),新键
   `email_template.<kind>.subject` / `email_template.<kind>.body`。零迁移;
   GET /v1/admin/email/settings 天然回吐(masked 列表按 key 透传)。
2. **渲染** `internal/email/templates.go`:
   - 占位符 `{{link}} {{token}}`(三类鉴权动作)/ `{{code}}`(oauth_code);值经
     SanitizeHeaderValue + HTML 转义(link 用与内置一致的属性转义)。
   - **fail-safe 链**:store 读错 / 覆盖为空 / 出现未知占位符 → 一律用内置默认正文,发送不中断。
   - 保存时校验:body 必含始终可用的凭证占位符(token 或 code),未知占位符拒绝。
3. **管理面**:PUT /v1/admin/email/settings 扩展 `templates` 字段(nil=不改,空串=清除覆盖);
   新增 POST /v1/admin/email/templates/preview(纯渲染样例值,不发信)。同步 OpenAPI。
4. **前端**:设置中心新增「邮件模板」分区(与 SMTP 同页聚合):kind 选择、主题/正文编辑、
   占位符提示、iframe srcDoc 沙箱预览、恢复默认。

## 爆炸半径与风控

- auth 邮件链邻近 → 每封邮件多一次 settings Load(冷却闸限频,可忽略);渲染异常永不阻断发送。
- 不动 SMTP 凭证加密路径;不动 cooldown / DLQ 语义。
- 测试:变异证(删 fallback → 红;放行未知占位符 → 红;砍必含凭证校验 → 红)+ handler 单测。

## 成功标准

tsc/vitest/go build/单测/OpenAPI 一致性全绿;变异全红;覆盖后邮件主题正文按模板出、
异常回退内置;前端可编辑+预览+清除。
