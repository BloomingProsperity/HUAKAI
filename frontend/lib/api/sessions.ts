// 会话/设备管理 API：对接 HUAKAI /v1/sessions/*（session 鉴权，走 userClient）。
//   - POST /v1/sessions/list   列出当前用户的活跃会话族（每个 family = 一台设备/一次登录）
//   - POST /v1/sessions/revoke 撤销某会话：按 family_id（其它设备）或 session_token（本机=登出）
// 真后端核对（backend/internal/gatewayhttp/session_handler.go + usersession/{types,invalidation,store}.go）：
//   * list/revoke 均为 POST、session bearer 鉴权（routes.go: r.Use(auth.SessionMiddleware)）；list 不是 GET。
//   * list 响应封装为 { families: SessionFamily[] }，按 last_active_at DESC 排序。
//   * SessionFamily 字段：id / status / generation / created_at / last_active_at / device_info / ip_baseline /
//     revoked_at? / revoked_reason?（device_info 含 ua_class：firefox|edge|chrome|safari|<token>|unknown）。
//   * revoke 请求体 { family_id?, session_token?, refresh_token?, reason? }；给 family_id 时后端校验归属。响应 { revoked: number }。
//   * 后端无「撤销其它全部」单端点，且当前 family_id 不在前端持久化 → 「登出全部设备」= 遍历 family 逐个按 family_id 撤（含本机）。
// 借鉴（功能/字段/布局形态，非抄码）：new-api profile/passkey-card.tsx 的「安全条目列表 + 状态徽章 +
//   最近活跃时间 + 行内危险操作（AlertDialog 二次确认）」布局；sub2api/new-api 均无独立活跃会话页，本页为 HUAKAI 自有面。
import { userPost } from './userClient';

export type FamilyStatus = 'active' | 'revoked' | 'expired' | 'suspicious' | 'replaced';

export interface SessionFamily {
  id: string;
  user_id: number;
  tenant_id: number;
  status: FamilyStatus;
  generation: number;
  created_at: string;
  last_active_at: string;
  device_info: Record<string, unknown> | null;
  ip_baseline: string;
  revoked_at?: string;
  revoked_reason?: string;
}

interface SessionListResponse {
  families: SessionFamily[] | null;
}

interface RevokeResponse {
  revoked: number;
}

export async function fetchSessions(): Promise<SessionFamily[]> {
  const r = await userPost<SessionListResponse>('/v1/sessions/list', {});
  return r.families ?? [];
}

// 撤销指定会话族（其它设备）。后端按 family_id 校验归属，非本人 → 403。
export async function revokeSessionFamily(familyID: string): Promise<number> {
  const r = await userPost<RevokeResponse>('/v1/sessions/revoke', {
    family_id: familyID,
    reason: 'user_requested',
  });
  return r.revoked;
}

// device_info.ua_class → 友好设备/浏览器名。
const UA_LABEL: Record<string, string> = {
  chrome: 'Chrome',
  firefox: 'Firefox',
  edge: 'Edge',
  safari: 'Safari',
  unknown: '未知客户端',
};

export function deviceLabel(family: SessionFamily): string {
  const info = family.device_info;
  const raw = info && typeof info.ua_class === 'string' ? (info.ua_class as string) : '';
  if (!raw) return '未知客户端';
  return UA_LABEL[raw] ?? raw.charAt(0).toUpperCase() + raw.slice(1);
}

const STATUS_LABEL: Record<FamilyStatus, string> = {
  active: '活跃',
  revoked: '已撤销',
  expired: '已过期',
  suspicious: '可疑',
  replaced: '已替换',
};

export function familyStatusLabel(status: FamilyStatus): string {
  return STATUS_LABEL[status] ?? status;
}

export type StatusTone = 'emerald' | 'red' | 'amber' | 'slate';

export function familyStatusTone(status: FamilyStatus): StatusTone {
  switch (status) {
    case 'active':
      return 'emerald';
    case 'suspicious':
      return 'amber';
    case 'revoked':
    case 'expired':
    case 'replaced':
      return 'red';
    default:
      return 'slate';
  }
}

// 仅 active / suspicious 的会话族可被撤销（与后端 RevokeOthers 的可撤判定一致）。
export function isRevocable(status: FamilyStatus): boolean {
  return status === 'active' || status === 'suspicious';
}
