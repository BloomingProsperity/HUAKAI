/*
 * 厂商模型同步(运维台/admin)前端类型 —— 镜像 adminhttp/model_sync_handler.go 的 JSON。
 * 端点:POST /admin/v1/model-sync(admin token 鉴权,仅 platform_admin 可触发)。
 * 触发后会刷新全局模型目录,影响所有继承 global catalog 的租户,故为高权操作。
 */

/** 单个厂商(vendor)的同步明细;镜像后端 modelSyncResultItemBody。 */
export interface ModelSyncResultItem {
  vendor: string
  added: number
  updated: number
  reactivated: number
  disabled: number
  unchanged: number
  snapshot_bumps: number
}

/** 一次同步的汇总结果;镜像后端 modelSyncResponseBody。 */
export interface ModelSyncResult {
  object: string
  completed_at: string
  total_added: number
  total_updated: number
  total_disabled: number
  results: ModelSyncResultItem[]
}

/** 触发同步的请求体;reason 可选,后端约束 ≤200 字符,空则后端兜底 admin_manual。 */
export interface ModelSyncRequest {
  reason?: string
}

/** reason 最大字符数(与后端 utf8.RuneCountInString > 200 校验保持一致)。 */
export const REASON_MAX = 200
