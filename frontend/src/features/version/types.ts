/*
 * 版本与维护(运维台/admin)前端类型 —— 镜像后端 buildinfo.Info 与 SMTP 测试端点的 JSON。
 *
 * 端点 1:GET /v1/admin/version(admin token 鉴权,只读)
 *   返回 backend/internal/buildinfo/buildinfo.go 的 Info:
 *   { version, commit, build_time, go_version }
 * 端点 2:POST /v1/admin/email/test(admin token 鉴权,触发动作)
 *   请求 backend/internal/gatewayhttp/admin_email_settings_handler.go 的 adminEmailTestRequest:
 *   { tenant_id, to };成功返回 { tenant_id, sent:true }。
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
