/*
 * 风控只读总览类型。对应后端 GET /admin/v1/risk/overview 响应
 * (backend/internal/riskoverviewhttp/handler.go overviewResponse)。纯计数,零敏感字段。
 */

export interface RiskOverview {
  object: string
  tenant_id: number
  disabled_keys: number
  firing_alerts: number
  disabled_users: number
  ip_blacklisted_keys: number
}

/** 单张风控卡片(只读展示 + 跳转到已有处置页)。 */
export interface RiskCard {
  key: string
  label: string
  count: number
  /** count>0 时为 'alert'(有风险信号),否则 'ok'。 */
  tone: 'ok' | 'alert'
  /** 「去处理」跳转到的已有运维页路径(本切片不内嵌处置动作)。 */
  actionPath: string
  actionLabel: string
}
