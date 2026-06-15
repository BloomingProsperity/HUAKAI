// 渠道健康（Channel Health）admin 客户端 —— F-CH-002 渠道自动熔断/冷却的运维读写面。
// 全部走管理 token（lib/api/client.ts 的 apiGet/apiPost，从 localStorage 'huakai_admin_token' 取 Bearer）。
// 端点形状逐字段对齐后端真码：
//   读：internal/gatewayhttp/channel_health_admin_handler.go
//        MountChannelHealthReadAdminRoutes → /v1/admin/channel-health{,/summary,/{channel_id}}
//        响应 builder：channelHealthResponse / channelHealthSummaryResponse / channelHealthAuditEventResponse
//   写：MountChannelHealthAdminRoutes 挂在 provider-accounts 路由下
//        /v1/admin/provider-accounts/{id}/channel-health/{pause,resume,force-active}
//        请求体 channelHealthOverrideRequest（仅 5 字段；多余字段无效，故只发这 5 个）
//   状态枚举 internal/channelhealth/types.go HealthState / SignalClass / ConfidenceTier。
//
// 借鉴（CLEAN-ROOM，CLAUDE.md §11/§12，仅功能形态，未抄码）：
//   - sub2api(LGPL) api/admin/channelMonitor.ts：渠道监控「列表 + 状态(operational/degraded/failed)
//     + 可用率 + 手动 run + enable 开关」的运维面字段形态；HUAKAI 改为读后端真实 health state-machine
//     状态(active/cooling_down/manual_paused 等) + 冷却倒计时 + pause/resume/force-active 三动作。
//   - new-api(AGPL) channel ColumnDefs：渠道状态分「启用/禁用/自动禁用」+ 响应耗时分桶配色的展示形态；
//     HUAKAI 映射到自有 state 枚举 + score。
//   仅提取字段/动作集合再自研类型，未复制源码结构。

import { apiGet, apiPost } from './client';

// ── 状态枚举（对齐 channelhealth/types.go） ─────────────────────────────

// HealthState：渠道健康状态机的状态。
export type ChannelHealthStateName =
  | 'active'
  | 'degraded'
  | 'cooling_down'
  | 'ramping'
  | 'disabled'
  | 'manual_paused';

// ConfidenceTier：信号置信层级。
export type ConfidenceTier = 'observed' | 'inferred' | 'operator_override';

// SignalClass 用字符串承载（后端枚举较多，前端只做展示，不强约束子集）。
export type SignalClass = string;

// ── 读响应类型 ───────────────────────────────────────────────────────

// 单条渠道健康记录（channelHealthResponse）。
// 注意：可选时间字段后端在零值/nil 时直接省略（omit），故全部 optional。
export interface ChannelHealthRecord {
  tenant_id: number;
  provider_account_id: number;
  account_credential_id: number;
  credential_version: number;
  vendor: string;
  channel_id: string; // StableChannelID()，详情/动作的稳定标识
  state: ChannelHealthStateName;
  score: number;
  reason_class: SignalClass;
  confidence_tier: ConfidenceTier;
  policy_version: string;
  ramp_stage_pct: number;
  ramp_failure_count: number;
  state_entered_at?: string;
  last_transition_at?: string;
  last_signal_class?: SignalClass;
  last_signal_at?: string;
  updated_at?: string;
  cooldown_until?: string; // 冷却结束时间（cooling_down 态才有）
  ramp_started_at?: string;
}

// 列表响应（newChannelHealthListHandler）。
export interface ChannelHealthListResponse {
  items: ChannelHealthRecord[];
  limit: number;
  offset: number;
}

// 汇总响应（channelHealthSummaryResponse）。by_state 的 key 为 HealthState 字符串。
export interface ChannelHealthSummary {
  by_state: Partial<Record<ChannelHealthStateName, number>>;
  total: number;
  oldest_cooldown_at?: string;
}

// 审计事件（channelHealthAuditEventResponse）。
export interface ChannelHealthAuditEvent {
  event_type: string;
  tenant_id: number;
  channel_id: string;
  vendor: string;
  provider_account_id: number;
  account_credential_id: number;
  credential_version: number;
  new_state: ChannelHealthStateName;
  reason_class: SignalClass;
  policy_version: string;
  payload: Record<string, unknown>;
  previous_state?: ChannelHealthStateName;
  request_id?: string;
  actor_id?: string;
  occurred_at?: string;
}

// 详情响应（newChannelHealthDetailHandler）。
export interface ChannelHealthDetailResponse {
  state: ChannelHealthRecord;
  audit_events: ChannelHealthAuditEvent[];
}

// ── 写请求类型 ───────────────────────────────────────────────────────

// 三个 override 动作共用的请求体（channelHealthOverrideRequest）。
// 仅这 5 个字段；id 走路径参数（provider_account_id），不在 body 里。
export interface ChannelHealthOverrideRequest {
  tenant_id: number;
  vendor: string;
  account_credential_id: number;
  credential_version: number;
  reason: string; // 后端强制非空，否则 400 reason_required
}

// ── 读端点 ───────────────────────────────────────────────────────────

const READ_BASE = '/v1/admin/channel-health';

// listChannelHealth — GET /v1/admin/channel-health/?tenant_id=&limit=&offset=
// 注意末尾斜杠：read 路由用 r.Get("/", ...) 挂在 /v1/admin/channel-health 下。
export function listChannelHealth(opts: {
  tenant_id: number;
  limit?: number;
  offset?: number;
}): Promise<ChannelHealthListResponse> {
  return apiGet<ChannelHealthListResponse>(`${READ_BASE}/`, {
    tenant_id: opts.tenant_id,
    limit: opts.limit,
    offset: opts.offset,
  });
}

// getChannelHealthSummary — GET /v1/admin/channel-health/summary?tenant_id=
export function getChannelHealthSummary(tenantId: number): Promise<ChannelHealthSummary> {
  return apiGet<ChannelHealthSummary>(`${READ_BASE}/summary`, { tenant_id: tenantId });
}

// getChannelHealthDetail — GET /v1/admin/channel-health/{channel_id}?tenant_id=
export function getChannelHealthDetail(
  channelId: string,
  tenantId: number,
): Promise<ChannelHealthDetailResponse> {
  return apiGet<ChannelHealthDetailResponse>(
    `${READ_BASE}/${encodeURIComponent(channelId)}`,
    { tenant_id: tenantId },
  );
}

// ── 写端点（override 动作） ──────────────────────────────────────────

// override 动作挂在 provider-accounts 路由下（routes.go mountProviderAccountAdminRoutes）。
const OVERRIDE_BASE = '/v1/admin/provider-accounts';

// pauseChannel — POST /v1/admin/provider-accounts/{id}/channel-health/pause
// 手动暂停渠道（ManualPause → manual_paused 态）。
export function pauseChannel(
  providerAccountId: number,
  body: ChannelHealthOverrideRequest,
): Promise<ChannelHealthRecord> {
  return apiPost<ChannelHealthRecord>(
    `${OVERRIDE_BASE}/${providerAccountId}/channel-health/pause`,
    body,
  );
}

// resumeChannel — POST /v1/admin/provider-accounts/{id}/channel-health/resume
// 解除手动暂停（ManualResume）。
export function resumeChannel(
  providerAccountId: number,
  body: ChannelHealthOverrideRequest,
): Promise<ChannelHealthRecord> {
  return apiPost<ChannelHealthRecord>(
    `${OVERRIDE_BASE}/${providerAccountId}/channel-health/resume`,
    body,
  );
}

// forceActiveChannel — POST /v1/admin/provider-accounts/{id}/channel-health/force-active
// 强制激活：无视冷却/降级，把渠道拉回 active（ForceActive，operator_override 置信层）。
export function forceActiveChannel(
  providerAccountId: number,
  body: ChannelHealthOverrideRequest,
): Promise<ChannelHealthRecord> {
  return apiPost<ChannelHealthRecord>(
    `${OVERRIDE_BASE}/${providerAccountId}/channel-health/force-active`,
    body,
  );
}

// ── 展示辅助（纯前端，无后端依赖） ──────────────────────────────────

// 各状态的中文标签。
export const STATE_LABEL: Record<ChannelHealthStateName, string> = {
  active: '健康',
  degraded: '降级',
  cooling_down: '冷却中',
  ramping: '恢复爬坡',
  disabled: '已熔断',
  manual_paused: '手动暂停',
};

// 状态 → 徽章 variant（design system badge）。
export function stateBadgeVariant(
  state: ChannelHealthStateName,
): 'default' | 'secondary' | 'destructive' | 'outline' {
  switch (state) {
    case 'active':
      return 'default'; // teal primary
    case 'degraded':
    case 'cooling_down':
    case 'ramping':
      return 'secondary';
    case 'disabled':
    case 'manual_paused':
      return 'destructive';
    default:
      return 'outline';
  }
}

// 把 cooldown_until 折算成剩余秒数（已过/无 → null）。
export function cooldownRemainingSeconds(
  cooldownUntil: string | undefined,
  nowMs: number,
): number | null {
  if (!cooldownUntil) return null;
  const until = new Date(cooldownUntil).getTime();
  if (Number.isNaN(until)) return null;
  const remaining = Math.floor((until - nowMs) / 1000);
  return remaining > 0 ? remaining : null;
}

// 把剩余秒数格式化为 "1h 23m 45s" 样式。
export function formatCountdown(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  const parts: string[] = [];
  if (h > 0) parts.push(`${h}h`);
  if (h > 0 || m > 0) parts.push(`${m}m`);
  parts.push(`${s}s`);
  return parts.join(' ');
}

// 时间字符串本地化展示。
export function fmtDateTime(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false });
}
