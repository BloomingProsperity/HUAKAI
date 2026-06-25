/*
 * Ops 告警控制台(运营台)前端类型 —— 镜像 alertinghttp 的 JSON DTO。
 * 端点(admin token 鉴权,/v1/admin/* 由 tokenForPath 注入):
 *   GET    /v1/admin/alert-rules                      列出规则(需 tenant_id query)
 *   POST   /v1/admin/alert-rules                      新建规则(tenant_id 在 body)
 *   GET    /v1/admin/alert-rules/{id}                  取规则(需 tenant_id query)
 *   PUT    /v1/admin/alert-rules/{id}                  改规则(需 tenant_id query)
 *   DELETE /v1/admin/alert-rules/{id}                  删规则(需 tenant_id query)→ 204
 *   GET    /v1/admin/alert-events                     列出事件(需 tenant_id;可选 rule_id/state)
 *   POST   /v1/admin/alert-events/{id}/manual-resolve  手动恢复事件(需 tenant_id query)
 *   GET    /v1/admin/alert-silences                   列出静默(需 tenant_id query)
 *   POST   /v1/admin/alert-silences                   新建静默(tenant_id 在 body)
 *   DELETE /v1/admin/alert-silences/{id}               删静默(需 tenant_id query)→ 204
 * 真码:backend/internal/alertinghttp/{mount,rule_handlers,event_handlers,silence_handlers}.go、
 *       backend/cmd/gateway/routes_alerting.go:9(mountAlertingAdminRoutes,admin 鉴权)。
 * 枚举真值:backend/internal/alerting/types.go:9-38。
 */

/** 比较符。alerting.Comparator(types.go:11)。非此四值后端 400。 */
export type Comparator = 'gt' | 'gte' | 'lt' | 'lte'

/** 告警级别。alerting.Severity(types.go:20)。非此三值后端 400。 */
export type Severity = 'info' | 'warning' | 'critical'

/** 事件状态。alerting.EventState(types.go:34)。列表 state 过滤限此三值(或空)。 */
export type EventState = 'firing' | 'resolved' | 'manual_resolved'

/** 规则响应项(ruleResponse,rule_handlers.go:38)。时间均 RFC3339 串。 */
export interface AlertRule {
  id: number
  tenant_id: number
  name: string
  metric: string
  metric_type?: string
  comparator: string
  threshold: number
  severity: string
  window_seconds: number
  sustained_seconds: number
  cooldown_seconds: number
  notify_email: boolean
  filters?: Record<string, string>
  last_triggered_at?: string | null
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface AlertRuleListResponse {
  object: string
  items: AlertRule[]
  limit: number
  offset: number
}

/** 新建规则请求体(ruleCreateRequest,rule_handlers.go:9)。tenant_id 在 body。 */
export interface CreateRuleRequest {
  tenant_id: number
  name: string
  metric: string
  metric_type?: string
  comparator: Comparator
  threshold: number
  severity: Severity
  window_seconds: number
  sustained_seconds?: number
  cooldown_seconds?: number
  notify_email?: boolean
  filters?: Record<string, string>
  enabled?: boolean
}

/** 改规则请求体(ruleUpdateRequest,rule_handlers.go:24),全字段可选(局部更新)。 */
export interface UpdateRuleRequest {
  name?: string
  metric?: string
  metric_type?: string
  comparator?: Comparator
  threshold?: number
  severity?: Severity
  window_seconds?: number
  sustained_seconds?: number
  cooldown_seconds?: number
  notify_email?: boolean
  filters?: Record<string, string>
  enabled?: boolean
}

/** 事件响应项(eventResponse,event_handlers.go:11)。 */
export interface AlertEvent {
  id: number
  tenant_id: number
  rule_id: number
  state: string
  observed_value: number
  threshold_value?: number | null
  metric_value?: number | null
  dimensions?: Record<string, string>
  fired_at: string
  resolved_at?: string | null
  email_sent: boolean
}

export interface AlertEventListResponse {
  object: string
  items: AlertEvent[]
  limit: number
  offset: number
}

/** 静默响应项(silenceResponse,silence_handlers.go:23)。 */
export interface AlertSilence {
  id: number
  tenant_id: number
  rule_id?: number | null
  reason: string
  starts_at: string
  ends_at: string
  platform?: string
  group_id?: string
  region?: string
  created_at: string
}

export interface AlertSilenceListResponse {
  object: string
  items: AlertSilence[]
  limit: number
  offset: number
}

/** 新建静默请求体(silenceCreateRequest,silence_handlers.go:11)。starts_at/ends_at RFC3339。 */
export interface CreateSilenceRequest {
  tenant_id: number
  rule_id?: number | null
  reason: string
  starts_at: string
  ends_at: string
  platform?: string
  group_id?: string
  region?: string
}
