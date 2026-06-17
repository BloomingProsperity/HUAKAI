// 告警规则（alert-rule）表单的纯逻辑层（零依赖 → 可直接 strip-types 单测）。
// 逐条镜像后端 alerting/service.go validateRule + alertinghttp/rule_handlers.go 不变式（禁止凭记忆）：
//   - name：trim 非空。
//   - metric_type 或 metric：至少一个非空（metricKeyForRule：metric_type 优先，否则 metric）。
//   - comparator：∈ {gt,gte,lt,lte}（types.go:12-15）。
//   - threshold：有限数字（拒 NaN/Inf；0/负数合法，service.go math.IsNaN/IsInf）。
//   - severity：∈ {info,warning,critical}（create 空默认 info）。
//   - window_seconds：> 0 整数。  sustained_seconds / cooldown_seconds：≥ 0 整数（空=0）。
//   - filters：可选 JSON 对象，值必须全为字符串（map[string]string）。
//   - 请求体后端 **DisallowUnknownFields** → 只能含已知字段。create 的 tenant_id 在 body；list/get/update/delete 在 query。
//
// 借鉴（CLEAN-ROOM，§11/§12/§16，仅功能/字段/动作形态，未抄码；源经核实，详见 plan）：
//   - sub2api@e34ad2b(LGPL)：完整 ops 告警规则 CRUD（routes/admin.go:148-151 GET/POST/PUT/DELETE alert-rules）。最近镜像。
//   - new-api@1ac0f58(AGPL)：无告警规则系统（仅渠道健康监控）。 - CLIProxyAPI@2a050dc：纯中继，无等价物。
//   HUAKAI delta：按【租户】隔离 + 指标阈值规则（metric/comparator/threshold/window/sustained/cooldown/severity/
//   notify_email/filters）+ DisallowUnknownFields 严格请求体（生态升级：多租户可配置告警规则）。

// ── 常量（镜像后端枚举）──────────────────────────────────────────────────

export const COMPARATORS = ['gt', 'gte', 'lt', 'lte'] as const;
export const RULE_SEVERITIES = ['info', 'warning', 'critical'] as const;
// MetricType 后端当前仅一个枚举值；metric 自由串为通用路径（二者至少一个）。
export const METRIC_TYPES = ['cpu_usage_percent'] as const;
// window/sustained/cooldown 后端是 int32；超上界会在 JSON 解码时溢出 → 误导性 invalid_json，前端先拦。
export const MAX_INT32 = 2147483647;

export type FiltersParse =
  | { ok: true; value: Record<string, string>; error: null }
  | { ok: false; value: null; error: string };

// parseFilters：解析 filters JSON。空 → {}；非对象/数组拒；非字符串值拒（后端 map[string]string）。
export function parseFilters(raw: string): FiltersParse {
  const s = raw.trim();
  if (s === '') return { ok: true, value: {}, error: null };
  let parsed: unknown;
  try {
    parsed = JSON.parse(s);
  } catch {
    return { ok: false, value: null, error: 'filters 必须是合法 JSON。' };
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return { ok: false, value: null, error: 'filters 必须是 JSON 对象。' };
  }
  const obj = parsed as Record<string, unknown>;
  for (const key of Object.keys(obj)) {
    if (typeof obj[key] !== 'string') {
      return { ok: false, value: null, error: `filters「${key}」的值必须是字符串。` };
    }
  }
  return { ok: true, value: obj as Record<string, string>, error: null };
}

export interface AlertRuleFormInput {
  name: string;
  metric_type: string; // '' 或枚举
  metric: string; // 自由指标键
  comparator: string;
  threshold_raw: string;
  severity: string;
  window_seconds_raw: string;
  sustained_seconds_raw: string;
  cooldown_seconds_raw: string;
  notify_email: boolean;
  filters_raw: string; // filters 的 JSON 文本
  enabled: boolean;
}

// ── 校验（与后端同序短路）──────────────────────────────────────────────

export function validateAlertRuleForm(input: AlertRuleFormInput): string | null {
  if (input.name.trim() === '') return 'name 必填。';
  if (input.metric_type.trim() === '' && input.metric.trim() === '') {
    return 'metric_type 或 metric 至少填一个。';
  }
  if (!(COMPARATORS as readonly string[]).includes(input.comparator)) {
    return 'comparator 必须是 gt / gte / lt / lte。';
  }
  const t = input.threshold_raw.trim();
  if (t === '' || !Number.isFinite(Number(t))) return 'threshold 必填且为有限数字。';
  if (!(RULE_SEVERITIES as readonly string[]).includes(input.severity)) {
    return 'severity 必须是 info / warning / critical。';
  }
  const w = input.window_seconds_raw.trim();
  if (!/^\d+$/.test(w) || Number(w) <= 0 || Number(w) > MAX_INT32) return `window_seconds 必须是 1..${MAX_INT32} 的整数。`;
  const sust = input.sustained_seconds_raw.trim();
  if (sust !== '' && (!/^\d+$/.test(sust) || Number(sust) > MAX_INT32)) return `sustained_seconds 必须是 0..${MAX_INT32} 的整数。`;
  const cool = input.cooldown_seconds_raw.trim();
  if (cool !== '' && (!/^\d+$/.test(cool) || Number(cool) > MAX_INT32)) return `cooldown_seconds 必须是 0..${MAX_INT32} 的整数。`;
  const f = parseFilters(input.filters_raw);
  if (!f.ok) return f.error;
  return null;
}

// ── 请求体构造（仅含已知字段；假定已过 validate）────────────────────────

// 共享数值/字段组装（create 与 update 主体一致，差异：create 带 tenant_id + 可选 metric/metric_type 省略；
// update 不带 tenant_id 且 metric/metric_type 都发（空串可清除，全量编辑）。filters 始终带（{} 表清空）。
function intOrZero(raw: string): number {
  return raw.trim() === '' ? 0 : Number(raw.trim());
}

// buildCreateBody：POST /v1/admin/alert-rules。tenant_id 在 body；metric/metric_type 仅非空时带。
export function buildCreateBody(input: AlertRuleFormInput, tenantId: number): Record<string, unknown> {
  const body: Record<string, unknown> = {
    tenant_id: tenantId,
    name: input.name.trim(),
    comparator: input.comparator,
    threshold: Number(input.threshold_raw.trim()),
    severity: input.severity,
    window_seconds: Number(input.window_seconds_raw.trim()),
    sustained_seconds: intOrZero(input.sustained_seconds_raw),
    cooldown_seconds: intOrZero(input.cooldown_seconds_raw),
    notify_email: input.notify_email,
    enabled: input.enabled,
  };
  const mt = input.metric_type.trim();
  if (mt !== '') body.metric_type = mt;
  const m = input.metric.trim();
  if (m !== '') body.metric = m;
  const f = parseFilters(input.filters_raw);
  if (f.ok && Object.keys(f.value).length > 0) body.filters = f.value;
  return body;
}

// buildUpdateBody：PUT /v1/admin/alert-rules/{id}（全量编辑，tenant 在 query，体不带 tenant_id/id）。
// metric 与 metric_type 都发（空串清除对应字段）；filters 始终发（{} 表清空）。
export function buildUpdateBody(input: AlertRuleFormInput): Record<string, unknown> {
  const f = parseFilters(input.filters_raw);
  return {
    name: input.name.trim(),
    metric: input.metric.trim(),
    metric_type: input.metric_type.trim(),
    comparator: input.comparator,
    threshold: Number(input.threshold_raw.trim()),
    severity: input.severity,
    window_seconds: Number(input.window_seconds_raw.trim()),
    sustained_seconds: intOrZero(input.sustained_seconds_raw),
    cooldown_seconds: intOrZero(input.cooldown_seconds_raw),
    notify_email: input.notify_email,
    enabled: input.enabled,
    filters: f.ok ? f.value : {},
  };
}
