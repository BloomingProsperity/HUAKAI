/*
 * 版本与维护(运维台/admin)前端类型 —— 镜像后端 buildinfo.Info 与 SMTP 设置/测试端点的 JSON。
 *
 * 端点 1:GET /v1/admin/version(admin token 鉴权,只读)
 *   返回 backend/internal/buildinfo/buildinfo.go 的 Info:
 *   { version, commit, build_time, go_version }
 * 端点 2:POST /v1/admin/email/test(admin token 鉴权,触发动作)
 *   请求 backend/internal/gatewayhttp/admin_email_settings_handler.go:40 的 adminEmailTestRequest:
 *   { tenant_id, to };成功返回 { tenant_id, sent:true }。
 * 端点 3:GET /v1/admin/email/settings?tenant_id=N(admin token 鉴权,只读回填)
 *   返回 admin_email_settings_handler.go:66 的 { tenant_id, settings:[{key,value,configured?,updated_at,updated_by}] };
 *   口令字段被掩码(maskEmailSettings,handler:231):value 恒空字符串,只给 configured 布尔,绝不回显明文。
 * 端点 4:PUT /v1/admin/email/settings(admin token 鉴权,保存)
 *   请求 admin_email_settings_handler.go:28 的 adminEmailSettingsRequest:
 *   { tenant_id, smtp_host?, smtp_port?, smtp_username?, smtp_password?, smtp_from?, smtp_from_name?,
 *     smtp_use_tls?, email_verify_enabled? };成功返回 { tenant_id, updated:N }。
 */

/** 构建版本信息(后端 buildinfo.Snapshot 快照)。 */
export interface BuildInfo {
  version: string
  commit: string
  build_time: string
  go_version: string
}

/** SMTP 测试请求体。tenant_id 单租户运营默认 0;to 为收件邮箱。 */
export interface SmtpTestRequest {
  tenant_id: number
  to: string
}

/** SMTP 测试成功响应(后端 writeAuditJSON 输出)。 */
export interface SmtpTestResponse {
  tenant_id: number
  sent: boolean
}

/**
 * GET /settings 返回的单条掩码设置项(后端 maskEmailSettings,handler:223)。
 * 口令项 key='smtp_password' 时 value 恒为空串,只附带 configured 标志;
 * 其余项直接给 value。updated_at/updated_by 为审计元数据。
 */
export interface EmailSettingItem {
  key: string
  value: string
  /** 仅口令项携带:后端存储里是否已配置过非空口令。 */
  configured?: boolean
  updated_at: string
  updated_by: string
}

/** GET /settings 顶层响应。 */
export interface EmailSettingsResponse {
  tenant_id: number
  settings: EmailSettingItem[]
}

/**
 * PUT /settings 请求体(adminEmailSettingsRequest)。除 tenant_id 外字段全可选;
 * 后端对空字符串/0 的处理见 admin_email_settings_handler.go:184 adminEmailSettingsValues:
 *  - 字符串字段空白(trim 后为空)→ 不写入,保留原值(留空=不改);
 *  - smtp_port 为 0 → 不写入,保留原值;
 *  - smtp_password 是指针:省略(undefined)→ 保留原口令;传空串 "" → 加密空串覆盖=清除口令。
 *    因此前端「口令留空」必须省略本字段,绝不发空串,避免误清除。
 */
export interface EmailSettingsUpdateRequest {
  tenant_id: number
  smtp_host?: string
  smtp_port?: number
  smtp_username?: string
  smtp_password?: string
  smtp_from?: string
  smtp_from_name?: string
  smtp_use_tls?: boolean
  email_verify_enabled?: boolean
}

/** PUT /settings 成功响应(updated=本次写入的设置项数)。 */
export interface EmailSettingsUpdateResponse {
  tenant_id: number
  updated: number
}
