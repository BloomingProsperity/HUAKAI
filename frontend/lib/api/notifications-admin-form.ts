// 通知广播 admin 写体的纯逻辑（校验 + 精确 key-set 构造），零依赖 strip-types 单测。
// 后端真码:
//   - usernoticehttp/handlers.go broadcast handler + broadcastRequest{tenant_id?,title,body,severity?}
//     (decodeRequest 用 DisallowUnknownFields → 请求体只能含这些键，多余键 → 400)
//   - usernotice/types.go Severity ∈ {info,warning,critical}（空默认 info，service.go 校验）

export type NotifySeverity = 'info' | 'warning' | 'critical';
export const NOTIFY_SEVERITIES: NotifySeverity[] = ['info', 'warning', 'critical'];

export interface BroadcastInput {
  title: string;
  body: string;
  severity?: string; // 空 = 后端默认 info
  tenant_id?: number; // 平台 admin 可指定；省略 = 用自身 scope
}

// validateBroadcast 镜像后端硬约束：title/body 必填（非空白）；severity 非空时须在允许集；
// tenant_id 若给须正整数。返回错误串（给 UI）或 null。
export function validateBroadcast(input: BroadcastInput): string | null {
  if (!input.title.trim()) return '标题必填';
  if (!input.body.trim()) return '正文必填';
  if (input.severity && !NOTIFY_SEVERITIES.includes(input.severity as NotifySeverity)) {
    return 'severity 必须是 info / warning / critical';
  }
  if (input.tenant_id !== undefined && (!Number.isInteger(input.tenant_id) || input.tenant_id <= 0)) {
    return 'tenant_id 必须为正整数';
  }
  return null;
}

// buildBroadcastBody 构造 POST /v1/admin/notifications/broadcast 的精确 key-set。
// 后端 DisallowUnknownFields → 请求体只能含 {tenant_id?, title, body, severity?}；任何多余键 → 400。
// 空 severity / 未给 tenant_id 一律省略（后端 omitempty + severity 空默认 info）。
export function buildBroadcastBody(input: BroadcastInput): Record<string, string | number> {
  const out: Record<string, string | number> = {
    title: input.title,
    body: input.body,
  };
  if (input.severity && input.severity.trim()) out.severity = input.severity;
  if (input.tenant_id !== undefined) out.tenant_id = input.tenant_id;
  return out;
}
