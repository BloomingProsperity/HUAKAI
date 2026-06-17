// 告警静默（alert-silence）表单的纯逻辑层（零依赖 → 可直接 strip-types 单测）。
// 逐条镜像后端 alerting/service.go validateSilence + alertinghttp/silence_handlers.go 不变式（禁止凭记忆）：
//   - starts_at / ends_at：必填且合法；**ends 必须严格晚于 starts**（service.go:418 EndsAt.After(StartsAt)）。
//   - rule_id：可选；若给则必须为正整数（service.go RuleID!=nil && *RuleID<=0 → invalid）。
//   - reason / platform / group_id / region：可选自由串（后端 trim，不强制非空）。
//   - 请求体后端用 **DisallowUnknownFields**（helpers.go decodeRequest）→ 只能含已知字段，禁多余键。
//   - create 的 tenant_id 在 **body**；list/delete 的 tenant_id 在 **query**。
//
// 借鉴（CLEAN-ROOM，§11/§12/§16，仅功能/字段/动作形态，未抄码；源经核实，详见 plan artifact）：
//   - sub2api@e34ad2b(LGPL)：完整 ops 告警系统含【作用域告警静默】——CreateAlertSilence
//     (service/ops_alerts.go:127) 按 rule/platform/group/region 作用域 + IsAlertSilenced 抑制检查
//     (ops_alerts.go:154)，POST /admin/ops/alert-silences (routes/admin.go:155)。最近镜像。
//     注：sub2api 【强制】rule_id>0 + 非空 platform（ops_alerts.go:137-141 INVALID_RULE_ID/INVALID_PLATFORM）。
//   - new-api@1ac0f58(AGPL)：无告警静默系统（无 alert-rule/silence；仅渠道健康监控）。
//   - CLIProxyAPI@2a050dc：纯中继，无等价物。
//   HUAKAI delta：作用域维度同 sub2api（rule/platform/group/region）但【全部可选】——后端 validateSilence
//   仅强制 tenant>0 + ends>starts（service.go:418-429），允许「租户级全静默」（sub2api 禁止）；
//   叠加 **按租户隔离** + starts/ends【时间窗】+ DisallowUnknownFields 严格请求体（生态升级：多租户运维静默）。

// ── 时间辅助 ────────────────────────────────────────────────────────────

export function isProvidedDate(raw: string | undefined): boolean {
  const s = (raw ?? '').trim();
  return s !== '' && !Number.isNaN(new Date(s).getTime());
}

// toRFC3339：表单时间串（datetime-local 或 ISO）→ 后端可解析的 RFC3339（UTC）。假定已过 isProvidedDate。
function toRFC3339(raw: string): string {
  return new Date(raw.trim()).toISOString();
}

// coercePositiveInt：非空串解析为正整数；空串视为「未提供」返回 null；非法返回 NaN（供校验区分）。
export function coercePositiveInt(raw: string | undefined): number | null | typeof NaN {
  const s = (raw ?? '').trim();
  if (s === '') return null;
  // 仅接受纯十进制整数串，拒绝 '1.5' / '1e3' / 'abc' / '-1'。
  if (!/^\d+$/.test(s)) return NaN;
  const n = Number(s);
  return n > 0 ? n : NaN;
}

export interface AlertSilenceFormInput {
  reason: string;
  starts_at_raw: string;
  ends_at_raw: string;
  rule_id_raw?: string; // 可选；正整数
  platform?: string;
  group_id?: string;
  region?: string;
}

// ── 校验（与后端同序短路）──────────────────────────────────────────────

export function validateAlertSilenceForm(input: AlertSilenceFormInput): string | null {
  if (!isProvidedDate(input.starts_at_raw)) return 'starts_at 必填且为合法时间。';
  if (!isProvidedDate(input.ends_at_raw)) return 'ends_at 必填且为合法时间。';
  // 跨字段：ends 必须严格晚于 starts（镜像后端 EndsAt.After(StartsAt)）。
  const starts = new Date(input.starts_at_raw.trim()).getTime();
  const ends = new Date(input.ends_at_raw.trim()).getTime();
  if (!(ends > starts)) return 'ends_at 必须晚于 starts_at。';
  // rule_id 可选；若给则必须正整数。
  if (Number.isNaN(coercePositiveInt(input.rule_id_raw))) return 'rule_id 必须是正整数。';
  return null;
}

// ── 请求体构造（仅含已知字段，禁多余键；假定已过 validate）──────────────

// buildSilenceBody：POST /v1/admin/alert-silences 请求体。tenant_id 在 body；时间→RFC3339；
// rule_id/platform/group_id/region 仅在提供时带上（DisallowUnknownFields → 不塞多余键）。
export function buildSilenceBody(input: AlertSilenceFormInput, tenantId: number): Record<string, unknown> {
  const body: Record<string, unknown> = {
    tenant_id: tenantId,
    reason: input.reason.trim(),
    starts_at: toRFC3339(input.starts_at_raw),
    ends_at: toRFC3339(input.ends_at_raw),
  };
  const ruleId = coercePositiveInt(input.rule_id_raw);
  if (typeof ruleId === 'number') body.rule_id = ruleId;
  const platform = (input.platform ?? '').trim();
  if (platform !== '') body.platform = platform;
  const groupId = (input.group_id ?? '').trim();
  if (groupId !== '') body.group_id = groupId;
  const region = (input.region ?? '').trim();
  if (region !== '') body.region = region;
  return body;
}
