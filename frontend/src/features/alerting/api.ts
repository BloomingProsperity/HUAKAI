import { apiGet, apiSend } from '../../lib/api'
import type {
  AlertEvent,
  AlertEventListResponse,
  AlertRule,
  AlertRuleListResponse,
  AlertSilence,
  AlertSilenceListResponse,
  CreateRuleRequest,
  CreateSilenceRequest,
  EventState,
  UpdateRuleRequest,
} from './types'

/*
 * Ops 告警控制台数据访问层。端点全走 /v1/admin/alert-*(tokenForPath 注入 admin token)。
 * 真码:backend/internal/alertinghttp/mount.go:36(MountAdminRoutes)、
 *       backend/cmd/gateway/routes_alerting.go:9(接线,admin 鉴权)。
 * 列表/取/改/删需 tenant_id query;新建 tenant_id 在 body。删除返回 204(无体)。
 */

const RULES = '/v1/admin/alert-rules'
const EVENTS = '/v1/admin/alert-events'
const SILENCES = '/v1/admin/alert-silences'

// ── 规则 ────────────────────────────────────────────────
/** 列出规则。limit 1-500,offset>=0。 */
export async function listRules(
  tenantId: number,
  limit = 200,
  offset = 0,
  signal?: AbortSignal,
): Promise<AlertRuleListResponse> {
  return apiGet<AlertRuleListResponse>(RULES, { query: { tenant_id: tenantId, limit, offset }, signal })
}

/** 新建规则。返回 201 + 新规则。tenant_id 已在 body 内。 */
export async function createRule(req: CreateRuleRequest): Promise<AlertRule> {
  return apiSend<AlertRule>('POST', RULES, req)
}

/** 改规则。需 tenant_id query 定位租户作用域。 */
export async function updateRule(tenantId: number, id: number, req: UpdateRuleRequest): Promise<AlertRule> {
  return apiSend<AlertRule>('PUT', `${RULES}/${id}`, req, { query: { tenant_id: tenantId } })
}

/** 删规则。需 tenant_id query。后端 204 无体。 */
export async function deleteRule(tenantId: number, id: number): Promise<void> {
  await apiSend<void>('DELETE', `${RULES}/${id}`, undefined, { query: { tenant_id: tenantId } })
}

// ── 事件 ────────────────────────────────────────────────
/** 列出事件。可选 rule_id / state 过滤。 */
export async function listEvents(
  tenantId: number,
  opts: { ruleId?: number; state?: EventState; limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<AlertEventListResponse> {
  return apiGet<AlertEventListResponse>(EVENTS, {
    query: {
      tenant_id: tenantId,
      rule_id: opts.ruleId,
      state: opts.state,
      limit: opts.limit ?? 200,
      offset: opts.offset ?? 0,
    },
    signal,
  })
}

/** 手动恢复事件(仅 firing 可恢复)。需 tenant_id query。返回更新后的事件。 */
export async function manualResolveEvent(tenantId: number, id: number): Promise<AlertEvent> {
  return apiSend<AlertEvent>('POST', `${EVENTS}/${id}/manual-resolve`, undefined, {
    query: { tenant_id: tenantId },
  })
}

// ── 静默 ────────────────────────────────────────────────
/** 列出静默规则。 */
export async function listSilences(
  tenantId: number,
  limit = 200,
  offset = 0,
  signal?: AbortSignal,
): Promise<AlertSilenceListResponse> {
  return apiGet<AlertSilenceListResponse>(SILENCES, { query: { tenant_id: tenantId, limit, offset }, signal })
}

/** 新建静默。返回 201 + 新静默。tenant_id 已在 body 内。 */
export async function createSilence(req: CreateSilenceRequest): Promise<AlertSilence> {
  return apiSend<AlertSilence>('POST', SILENCES, req)
}

/** 删静默。需 tenant_id query。后端 204 无体。 */
export async function deleteSilence(tenantId: number, id: number): Promise<void> {
  await apiSend<void>('DELETE', `${SILENCES}/${id}`, undefined, { query: { tenant_id: tenantId } })
}
