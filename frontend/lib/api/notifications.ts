// 通知收件箱 + 公告 + 每用户通知设置：封装 /v1/notifications/*、/v1/announcements、
// /v1/users/me/notifications。全部走 session 鉴权（userClient 自动带 token + 401 刷新）。
//
// 字段形状对齐后端真码：
//   - 收件箱：internal/usernoticehttp/handlers.go（notificationResponse / notificationListResponse / unreadCountResponse）
//   - 公告：  internal/announcementhttp/handlers.go（announcementResponse / announcementListResponse）
//   - 设置：  internal/controlhttp/notify_handler.go（notifySettingsRequest / notifySettingsResponse）
//   - severity 枚举：internal/usernotice/types.go + internal/announcement/types.go（info/warning/critical）
//   - notify_type 枚举 + 校验：internal/notify/types.go ValidateSettings（none/email/webhook/bark/gotify）
//
// 借鉴（clean-room，仅功能/字段形态，未抄码）：
//   - 收件箱「unread-only 过滤 + 标记已读 + 未读计数」概念来自 sub2api src/api/announcements.ts。
//     注意：HUAKAI 后端「标记已读」只作用于通知收件箱（POST /v1/notifications/{id}/read），
//     公告侧无 per-user 已读端点（与 sub2api 的 /announcements/{id}/read 不同）。
//   - 通知设置卡片（启用开关 + 阈值 + 渠道字段）布局来自 sub2api
//     components/user/profile/ProfileBalanceNotifyCard.vue 的字段形态。
import { userGet, userPost, userPut } from './userClient';

// ===== 公共枚举 =====

// 通知/公告共享的告警级别（后端 usernotice/types.go + announcement/types.go）。
export type Severity = 'info' | 'warning' | 'critical' | (string & {});

// 通知渠道类型（后端 notify/types.go Type）。none = 关闭推送。
export type NotifyType = 'none' | 'email' | 'webhook' | 'bark' | 'gotify';

// ===== 通知收件箱 =====

// GET /v1/notifications 列表项（handlers.go notificationResponse）。
export interface Notification {
  id: number;
  tenant_id: number;
  user_id: number;
  title: string;
  body: string;
  severity: Severity;
  // RFC3339；缺省/未读时 read_at 不下发。
  read_at?: string | null;
  created_by_admin?: number | null;
  created_at: string;
}

// GET /v1/notifications 响应信封（handlers.go notificationListResponse）。
export interface NotificationListResponse {
  object: string;
  items: Notification[];
  limit: number;
  offset: number;
}

// GET /v1/notifications/unread-count 响应（handlers.go unreadCountResponse）。
export interface UnreadCountResponse {
  object: string;
  count: number;
}

export interface ListNotificationsOptions {
  // 1..100，后端默认 50；超界后端 400（invalid_limit）。
  limit?: number;
  // >=0，后端默认 0；负数后端 400（invalid_offset）。
  offset?: number;
  // 仅返回未读。
  unreadOnly?: boolean;
}

// 拉取当前用户的通知收件箱。session 鉴权。
export function listNotifications(opts: ListNotificationsOptions = {}): Promise<NotificationListResponse> {
  return userGet<NotificationListResponse>('/v1/notifications', {
    limit: opts.limit,
    offset: opts.offset,
    unread_only: opts.unreadOnly ? true : undefined,
  });
}

// 未读数（用于 tab 角标）。session 鉴权。
export function fetchUnreadCount(): Promise<UnreadCountResponse> {
  return userGet<UnreadCountResponse>('/v1/notifications/unread-count');
}

// 标记单条通知已读，返回更新后的通知（含 read_at）。session 鉴权。
// 后端 POST /v1/notifications/{id}/read，id 必须为正整数否则 400。
export function markNotificationRead(id: number): Promise<Notification> {
  return userPost<Notification>(`/v1/notifications/${id}/read`);
}

// ===== 公告 =====

// GET /v1/announcements 列表项（announcementhttp/handlers.go announcementResponse）。
export interface Announcement {
  id: number;
  tenant_id: number;
  title: string;
  body: string;
  severity: Severity;
  active: boolean;
  // RFC3339。
  published_at: string;
  expires_at?: string | null;
  created_by_admin?: number | null;
  created_at: string;
  updated_at: string;
}

// GET /v1/announcements 响应信封（announcementhttp/handlers.go announcementListResponse）。
export interface AnnouncementListResponse {
  object: string;
  items: Announcement[];
  limit: number;
  offset: number;
}

export interface ListAnnouncementsOptions {
  limit?: number;
  offset?: number;
}

// 拉取当前租户的生效公告（后端 ListActive：仅 active 且在发布/过期窗口内）。
// session 鉴权：userClient 带 Bearer token，后端 resolveUserTenant 从 session 解析 tenant。
export function listAnnouncements(opts: ListAnnouncementsOptions = {}): Promise<AnnouncementListResponse> {
  return userGet<AnnouncementListResponse>('/v1/announcements', {
    limit: opts.limit,
    offset: opts.offset,
  });
}

// ===== 每用户通知设置 =====

// GET/PUT /v1/users/me/notifications 响应（notify_handler.go notifySettingsResponse）。
// 注意 secret/token 为只写：响应只回 *_configured 布尔，不回明文。
export interface NotifySettings {
  tenant_id: number;
  user_id: number;
  notify_type: NotifyType | string;
  webhook_url?: string;
  webhook_secret_configured?: boolean;
  notification_email?: string;
  bark_url?: string;
  gotify_url?: string;
  gotify_token_configured?: boolean;
  gotify_priority?: number;
  // decimal 序列化为字符串。
  balance_threshold: string;
  updated_at?: string;
  updated_by?: string;
}

// PUT /v1/users/me/notifications 请求体（notify_handler.go notifySettingsRequest）。
// 后端 DisallowUnknownFields：只允许这些键，多余字段会 400（invalid_json）。
// secret/token 留空 = 不更新明文（后端按 type 校验，见 saveNotifySettings 注释）。
export interface NotifySettingsRequest {
  notify_type: NotifyType;
  webhook_url?: string;
  webhook_secret?: string;
  notification_email?: string;
  bark_url?: string;
  gotify_url?: string;
  gotify_token?: string;
  gotify_priority?: number;
  // decimal 字符串，如 "5" / "0.5"。
  balance_threshold?: string;
}

// 读取当前用户的通知设置。session 鉴权。
export function fetchNotifySettings(): Promise<NotifySettings> {
  return userGet<NotifySettings>('/v1/users/me/notifications');
}

// 保存通知设置（整体 upsert）。session 鉴权。
//
// 后端 ValidateSettings 按 notify_type 校验，前端需在 UI 层兜住以下硬约束以免 400：
//   - email：notification_email 必须是合法邮箱
//   - webhook：webhook_secret 非空 + webhook_url 为合法外联 URL
//   - bark：bark_url 为合法外联 URL
//   - gotify：gotify_token 非空 + gotify_priority 1..10 + gotify_url 为合法外联 URL
//   - none：无要求（安全默认）
export function saveNotifySettings(body: NotifySettingsRequest): Promise<NotifySettings> {
  return userPut<NotifySettings>('/v1/users/me/notifications', body);
}

// ===== 展示辅助 =====

// severity → 中文标签。
export function severityLabel(sev: Severity): string {
  switch (sev) {
    case 'critical':
      return '紧急';
    case 'warning':
      return '警告';
    case 'info':
      return '通知';
    default:
      return sev || '通知';
  }
}

// notify_type → 中文标签。
export function notifyTypeLabel(type: NotifyType | string): string {
  switch (type) {
    case 'email':
      return '邮件';
    case 'webhook':
      return 'Webhook';
    case 'bark':
      return 'Bark';
    case 'gotify':
      return 'Gotify';
    case 'none':
    default:
      return '关闭';
  }
}
