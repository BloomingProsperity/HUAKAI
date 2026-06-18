// admin per-user 通知设置写体的纯逻辑（notify_type 条件校验 + 精确 key-set 构造），零依赖 strip-types 单测。
// 后端真码:
//   - controlhttp/notify_handler.go notifySettingsRequest(10 字段, 含 extra_emails) + decodeNotifySettingsRequest(DisallowUnknownFields)
//   - notify/types.go ValidateSettings：按 notify_type 条件校验（none/email/webhook/bark/gotify）
// 复用 notifications.ts 的 NotifyType / NotifySettingsRequest 类型（与用户侧同一体形状）。
import type { NotifySettingsRequest, NotifyType } from './notifications';

export const NOTIFY_TYPES: NotifyType[] = ['none', 'email', 'webhook', 'bark', 'gotify'];

// 基础邮箱形状（非完整 RFC5322）。后端 mail.ParseAddress 为权威，前端仅 fail-fast。
const EMAIL_RE = /^[^@\s]+@[^@\s]+\.[^@\s]+$/;

// 外联 URL 形状校验：后端 validateOutboundURL → ValidateOAuthEndpointURL 强制 **https**
// 且另做 SSRF（拒 loopback/内网/元数据 IP）。前端无法解析 IP，不重复 SSRF（后端是安全边界），
// 只做 https 形状 fail-fast——http:// 会被后端 400 拒，故前端也须拒。
function looksLikeHttpsUrl(u: string | undefined): boolean {
  return !!u && /^https:\/\/\S+$/.test(u.trim());
}

// validateNotifySettings 镜像后端 ValidateSettings（extra_emails 无条件校验 + notify_type 条件规则，请求体 10 字段）。
// 返回错误串（给 UI）或 null。
export function validateNotifySettings(body: NotifySettingsRequest): string | null {
  if (!NOTIFY_TYPES.includes(body.notify_type)) return 'notify_type 非法';
  // extra_emails 与 notify_type 无关, 后端 ValidateSettings 在 type 分支前无条件校验(≤10 + 每条合法邮箱),
  // 故前端也先 fail-fast(空串过滤后计数, 与后端 normalized 一致)。后端 mail.ParseAddress 为权威。
  const extras = (body.extra_emails ?? []).map((e) => e.trim()).filter((e) => e !== '');
  if (extras.length > 10) return 'extra_emails 最多 10 条';
  for (const e of extras) {
    if (!EMAIL_RE.test(e)) return `extra_emails 含非法邮箱: ${e}`;
  }
  switch (body.notify_type) {
    case 'none':
      return null;
    case 'email':
      if (!body.notification_email || !EMAIL_RE.test(body.notification_email)) {
        return 'email 类型需合法 notification_email';
      }
      return null;
    case 'webhook':
      if (!body.webhook_secret) return 'webhook 类型需非空 webhook_secret';
      if (!looksLikeHttpsUrl(body.webhook_url)) return 'webhook 类型需合法 https webhook_url';
      return null;
    case 'bark':
      if (!looksLikeHttpsUrl(body.bark_url)) return 'bark 类型需合法 https bark_url';
      return null;
    case 'gotify':
      if (!body.gotify_token) return 'gotify 类型需非空 gotify_token';
      // gotify_priority 可省略——后端缺省默认 5；仅在**给定时**校验 1..10（与后端 ValidateSettings 一致）。
      if (
        body.gotify_priority !== undefined &&
        (!Number.isInteger(body.gotify_priority) || body.gotify_priority < 1 || body.gotify_priority > 10)
      ) {
        return 'gotify_priority 须为 1..10 的整数';
      }
      if (!looksLikeHttpsUrl(body.gotify_url)) return 'gotify 类型需合法 https gotify_url';
      return null;
    default:
      return 'notify_type 非法';
  }
}

// buildNotifySettingsBody 构造 PUT 的精确 key-set。后端 DisallowUnknownFields → 只能含这 10 个键(含 extra_emails 数组)。
// 空 optional 字符串字段一律省略（后端 omitempty）；gotify_priority 给定即带。
export function buildNotifySettingsBody(body: NotifySettingsRequest): Record<string, string | number | string[]> {
  const out: Record<string, string | number | string[]> = { notify_type: body.notify_type };
  if (body.webhook_url && body.webhook_url.trim()) out.webhook_url = body.webhook_url;
  if (body.webhook_secret && body.webhook_secret.trim()) out.webhook_secret = body.webhook_secret;
  if (body.notification_email && body.notification_email.trim()) out.notification_email = body.notification_email;
  if (body.bark_url && body.bark_url.trim()) out.bark_url = body.bark_url;
  if (body.gotify_url && body.gotify_url.trim()) out.gotify_url = body.gotify_url;
  if (body.gotify_token && body.gotify_token.trim()) out.gotify_token = body.gotify_token;
  if (body.balance_threshold && body.balance_threshold.trim()) out.balance_threshold = body.balance_threshold;
  if (body.gotify_priority !== undefined) out.gotify_priority = body.gotify_priority;
  // extra_emails: 空串过滤后非空才带(对齐后端 omitempty + normalized 去空); 全空则省略键。
  const extras = (body.extra_emails ?? []).map((e) => e.trim()).filter((e) => e !== '');
  if (extras.length > 0) out.extra_emails = extras;
  return out;
}
