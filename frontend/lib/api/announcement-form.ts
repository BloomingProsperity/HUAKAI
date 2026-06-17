// 公告（announcement）表单的纯逻辑层（零依赖 → 可直接 strip-types 单测）。
// 逐条镜像后端 announcement/service.go validateAnnouncement + announcementhttp/handlers.go 不变式（禁止凭记忆）：
//   - title / body：trim 后非空（后端 validateAnnouncement:167）。
//   - severity：∈ {info,warning,critical}（types.go:12-14；create 留空后端默认 info，service.go:151-152）。
//   - published_at：可选 RFC3339；create 留空后端默认 now（normalizeCreateInput:145）。
//   - expires_at：可选 RFC3339；**若存在必须严格晚于 published_at**（service.go:178 ExpiresAt.After(PublishedAt)）。
//   - active：可选 bool（create 默认 true，service.go:144）。
//   - 请求体后端用 **DisallowUnknownFields**（handlers.go:423）→ 只能含已知字段，禁塞多余键。
//   - create 的 tenant_id 在 **body**；list/update/delete 的 tenant_id 在 **query**（update 体不得带 tenant_id/id）。
//
// 借鉴（CLEAN-ROOM，§11/§12/§16，仅功能/字段/动作形态，未抄码；源经 reviewer-lane 核实，详见 plan artifact）：
//   - new-api@1ac0f58(AGPL)：两套——①单条全局 Notice 串（model/option.go:67 / controller/misc.go:172）；
//     ②独立结构化 Announcements 模块（console_setting/config.go:8 JSON 数组串 + AnnouncementsEnabled，
//     GetAnnouncements controller/misc.go:129、validation.go:141-185：数组上限 100、per-record content、
//     publishDate(RFC3339)、type 枚举{default,ongoing,success,warning,error}、按 publishDate 倒序，经通用 options 端点编辑）。
//     但【无】per-row REST CRUD / 无 DB 表 / 无已读追踪 / 无租户维度 / 仅单 publishDate（无 published+expires 双时间窗）。
//   - sub2api@e34ad2b(LGPL)：结构化公告实体（title/content/status/notify_mode/targeting/starts_at/ends_at）+
//     按用户【已读追踪】（backend/ent/schema/announcement_read.go）；最全。
//   - CLIProxyAPI@2a050dc：纯中继，无公告（无等价物）。
//   HUAKAI delta：DB 表 + per-row REST CRUD + 按【租户】隔离 + **severity 分级(info/warning/critical)** +
//   active 开关 + published/expires【双时间窗】(非单 publishDate) + 后端 DisallowUnknownFields 严格请求体。
//   注：sub2api 的已读追踪 / 受众 targeting / notify_mode 后端暂无 → 见 plan roadmap（Feature-Preservation）。

// ── 常量 ────────────────────────────────────────────────────────────────

export const SEVERITIES = ['info', 'warning', 'critical'] as const;
export type Severity = (typeof SEVERITIES)[number];

// ── 时间辅助 ────────────────────────────────────────────────────────────

// isProvidedDate：raw 去空白后非空且可解析为合法时间。
export function isProvidedDate(raw: string | undefined): boolean {
  const s = (raw ?? '').trim();
  return s !== '' && !Number.isNaN(new Date(s).getTime());
}

// toRFC3339：把表单时间串（datetime-local 或 ISO）规整为后端可解析的 RFC3339（UTC）。假定已过 isProvidedDate。
function toRFC3339(raw: string): string {
  return new Date(raw.trim()).toISOString();
}

export interface AnnouncementFormInput {
  title: string;
  body: string;
  severity: string;
  active: boolean;
  published_at_raw?: string; // 空 = 不指定（create 时后端默认 now）
  expires_at_raw?: string; // 空 = 无过期（update 时显式置 null 清除）
}

// ── 校验（与后端同序短路）──────────────────────────────────────────────

export function validateAnnouncementForm(input: AnnouncementFormInput): string | null {
  if (input.title.trim() === '') return 'title 必填。';
  if (input.body.trim() === '') return 'body 必填。';
  if (!(SEVERITIES as readonly string[]).includes(input.severity)) {
    return 'severity 必须是 info / warning / critical。';
  }
  const pubGiven = (input.published_at_raw ?? '').trim() !== '';
  const expGiven = (input.expires_at_raw ?? '').trim() !== '';
  if (pubGiven && !isProvidedDate(input.published_at_raw)) return 'published_at 不是合法时间。';
  if (expGiven && !isProvidedDate(input.expires_at_raw)) return 'expires_at 不是合法时间。';
  // 跨字段：两者都给时，expires 必须严格晚于 published（镜像后端 ExpiresAt.After(PublishedAt)）。
  // published 留空（后端取 now）的情形交后端兜底，避免前端引入时钟不确定性。
  if (pubGiven && expGiven) {
    const pub = new Date((input.published_at_raw ?? '').trim()).getTime();
    const exp = new Date((input.expires_at_raw ?? '').trim()).getTime();
    if (!(exp > pub)) return 'expires_at 必须晚于 published_at。';
  }
  return null;
}

// ── 请求体构造（仅含已知字段，禁多余键；假定已过 validate）──────────────

// buildCreateBody：POST /v1/admin/announcements 请求体。tenant_id 在 body；不带 id。
export function buildCreateBody(input: AnnouncementFormInput, tenantId: number): Record<string, unknown> {
  const body: Record<string, unknown> = {
    tenant_id: tenantId,
    title: input.title.trim(),
    body: input.body.trim(),
    severity: input.severity,
    active: input.active,
  };
  if (isProvidedDate(input.published_at_raw)) body.published_at = toRFC3339(input.published_at_raw as string);
  if (isProvidedDate(input.expires_at_raw)) body.expires_at = toRFC3339(input.expires_at_raw as string);
  return body;
}

// buildUpdateBody：PUT /v1/admin/announcements/{id} 请求体（全量编辑语义）。
// 不带 tenant_id/id（tenant 在 query）。expires_at：非空→值；空→显式 null 清除（后端 optionalTime.Set）。
export function buildUpdateBody(input: AnnouncementFormInput): Record<string, unknown> {
  const body: Record<string, unknown> = {
    title: input.title.trim(),
    body: input.body.trim(),
    severity: input.severity,
    active: input.active,
  };
  if (isProvidedDate(input.published_at_raw)) body.published_at = toRFC3339(input.published_at_raw as string);
  body.expires_at = isProvidedDate(input.expires_at_raw) ? toRFC3339(input.expires_at_raw as string) : null;
  return body;
}
