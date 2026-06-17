// 通知 admin 数据层 —— 全部走管理 token（apiGet/apiPost from client.ts）。
// 后端: usernoticehttp /v1/admin/notifications/broadcast + subscriptionhttp
//       /v1/admin/notifications/worker-stats。形状逐字段对照后端 handler 真码。
import { apiGet, apiPost } from './client';
import { buildBroadcastBody, validateBroadcast, type BroadcastInput } from './notifications-admin-form';

// ---- 广播通知 POST /v1/admin/notifications/broadcast ----
// usernoticehttp/handlers.go broadcast: 201 + broadcastResponse{object,tenant_id,inserted}。
export interface BroadcastResponse {
  object: string;
  tenant_id: number;
  inserted: number; // 写入的通知条数
}

export function broadcastNotification(input: BroadcastInput): Promise<BroadcastResponse> {
  // 写路径 fail-fast：发请求前先按后端硬约束校验（与 buildBroadcastBody 用同一 severity 判定，
  // 消除二者对空白 severity 的分歧）。后端仍独立 re-validate，此处只为更快的客户端反馈。
  const invalid = validateBroadcast(input);
  if (invalid) return Promise.reject(new Error(invalid));
  return apiPost<BroadcastResponse>(
    '/v1/admin/notifications/broadcast',
    buildBroadcastBody(input),
  );
}

// ---- 通知 worker 统计 GET /v1/admin/notifications/worker-stats ----
// subscriptionhttp/admin_worker_stats.go: {reminder,expiry} 两组 worker tick 计数。
export interface ReminderWorkerStats {
  tick_count: number;
  sent_total: number;
  failed_ticks: number;
}

export interface ExpiryWorkerStats {
  tick_count: number;
  expired_total: number;
  failed_ticks: number;
}

export interface NotificationWorkerStatsResponse {
  reminder: ReminderWorkerStats;
  expiry: ExpiryWorkerStats;
}

export function getNotificationWorkerStats(): Promise<NotificationWorkerStatsResponse> {
  return apiGet<NotificationWorkerStatsResponse>('/v1/admin/notifications/worker-stats');
}
