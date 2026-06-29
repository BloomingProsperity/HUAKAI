/*
 * 渠道健康台前端类型 —— 镜像后端 internal/gatewayhttp/channel_health_admin_handler.go 的 JSON 形态。
 *
 * 读端点(admin token,挂 /v1/admin/channel-health,见 cmd/gateway/routes.go:985-990):
 *   GET /v1/admin/channel-health?tenant_id=N&limit=&offset=   渠道健康列表(handler:60 newChannelHealthListHandler)
 *   GET /v1/admin/channel-health/summary?tenant_id=N          各状态聚合(handler:90)
 *   GET /v1/admin/channel-health/{channel_id}?tenant_id=N     单渠道详情 + 审计事件(handler:108)
 *
 * 写端点(admin token,挂 /v1/admin/provider-accounts/{id}/...,routes.go:927-973 内 MountChannelHealthAdminRoutes:48):
 *   POST .../{id}/channel-health/pause          人工置「manual_paused」(handler:138)
 *   POST .../{id}/channel-health/resume         人工恢复(解除人工暂停,handler:144)
 *   POST .../{id}/channel-health/force-active    强制置 active(handler:150)
 *
 * 关键:列表/详情项返回完整坐标(tenant_id/vendor/account_credential_id/credential_version/provider_account_id),
 * 故写动作可直接用列表项里的坐标构造请求体(channelHealthOverrideRequest,handler:40)。
 * platform_admin 角色下 tenant_id query 必填(resolveChannelHealthAdmin + parsePositiveQueryInt)。
 * 本页只读总览 + pause/resume/force-active,不触碰任何 pool/channel/gateway 写路径。
 */

/** 渠道健康状态机取值(镜像 channelhealth HealthState,types.go:16-23)。 */
export type HealthState =
  | 'active'
  | 'degraded'
  | 'cooling_down'
  | 'ramping'
  | 'disabled'
  | 'manual_paused'
  | string

/**
 * 单条渠道健康记录(镜像 channelHealthResponse,channel_health_admin_handler.go:242)。
 * 时间字段后端按非零才输出(omitempty 语义),故均为可选。
 */
export interface ChannelHealthItem {
  /** 渠道所属租户。 */
  tenant_id: number
  /** 上游账号 ID(写动作 URL 的 {id})。 */
  provider_account_id: number
  /** 账号凭证 ID(写动作 body account_credential_id)。 */
  account_credential_id: number
  /** 凭证版本(写动作 body credential_version)。 */
  credential_version: number
  /** 厂商(写动作 body vendor)。 */
  vendor: string
  /** 稳定渠道 ID:显式 channel_id 或 vendor:cred:vN(StableChannelID,types.go:100)。 */
  channel_id: string
  /** 当前健康状态。 */
  state: HealthState
  /** 健康分(0~?,后端打分)。 */
  score: number
  /** 进入当前态的信号类别(SignalClass)。 */
  reason_class: string
  /** 置信层级(observed/inferred/operator_override)。 */
  confidence_tier: string
  /** 策略版本(channel-health-v1 等)。 */
  policy_version: string
  /** 爬坡阶段百分比(ramping 态用)。 */
  ramp_stage_pct: number
  /** 爬坡失败计数。 */
  ramp_failure_count: number
  state_entered_at?: string
  last_transition_at?: string
  last_signal_class?: string
  last_signal_at?: string
  updated_at?: string
  /** 冷却到期时刻(cooling_down 态用)。 */
  cooldown_until?: string
  ramp_started_at?: string
}

/** 列表响应(handler:82)。 */
export interface ChannelHealthListResponse {
  items: ChannelHealthItem[]
  limit: number
  offset: number
}

/** 状态聚合响应(channelHealthSummaryResponse,handler:282)。 */
export interface ChannelHealthSummary {
  /** 各状态 → 数量(键为 HealthState)。 */
  by_state: Record<string, number>
  /** 总渠道数。 */
  total: number
  /** 最早进入冷却的时刻(若有渠道在冷却)。 */
  oldest_cooldown_at?: string
}

/** 审计事件(channelHealthAuditEventResponse,handler:293)。 */
export interface ChannelHealthAuditEvent {
  event_type: string
  tenant_id: number
  channel_id: string
  vendor: string
  provider_account_id: number
  account_credential_id: number
  credential_version: number
  new_state: HealthState
  reason_class: string
  policy_version: string
  /** 脱敏后的事件载荷(RedactForAudience AudienceInternal)。 */
  payload?: unknown
  previous_state?: HealthState
  request_id?: string
  actor_id?: string
  occurred_at?: string
}

/** 详情响应(handler:131)。 */
export interface ChannelHealthDetailResponse {
  state: ChannelHealthItem
  audit_events: ChannelHealthAuditEvent[]
}

/**
 * 写动作请求体(channelHealthOverrideRequest,handler:40)。provider_account_id 走 URL 的 {id},
 * 不在 body。后端 ChannelKey.Validate 约束(types.go:84):tenant_id>0、vendor 非空、
 * account_credential_id>0、credential_version>0;reason 由 handler 单独要求非空(handler:186)。
 */
export interface ChannelHealthOverrideRequest {
  tenant_id: number
  vendor: string
  account_credential_id: number
  credential_version: number
  reason: string
}

/** 三种写动作类型。 */
export type OverrideAction = 'pause' | 'resume' | 'force-active'
