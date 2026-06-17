// 配额策略表单的纯逻辑层（零依赖 → 可直接 strip-types 单测）。
// 枚举常量 + 校验 + 请求体构造，逐条镜像后端 adminquotahttp/validate.go 不变式（禁止凭记忆）：
//   - scope_kind/metric/window_kind/mode 各有枚举白名单（对齐 quota_policies CHECK 约束）。
//   - scope_id 必填（trim 非空，'*' 表 global），≤255 字符。
//   - window_kind 空→默认 fixed；window_kind=fixed 时 window_seconds 必填 >0；其余 window_seconds≥0。
//   - limit_value 必填非负十进制；burst_value 可选非负十进制（缺省 0）。
//   - mode 空→默认 enforce。priority 缺省 100，enabled 缺省 true（后端 *指针字段区分省略/显式）。
//   - valid_until 若设则必须晚于 valid_from。
//
// 借鉴（CLEAN-ROOM，CLAUDE.md §11/§12/§16，仅功能/字段/枚举形态，未抄码；融合 = 升级）：
//   - sub2api(LGPL) ent/schema/user_platform_quota.go + api_key.go：配额表存在但 scope 硬编码、仅 USD、无 observe/priority。
//   - new-api(AGPL) model/token.go,user.go,channel.go：配额内嵌实体、lifetime 无窗口、仅 channel 级 priority。
//   - CLIProxyAPI@21fad9db：无持久配额策略（无等价物）。
//   HUAKAI delta：独立通用 policy 对象（6 scope × 4 metric × 5 window × 4 mode + priority + 有效期 + burst），
//   observe(dry-run) 模式两家皆无。

// ── 枚举白名单（与后端 CHECK 约束一致；表单下拉 + 校验共用）──────────────

export const SCOPE_KINDS = ['global', 'user', 'api_key', 'channel', 'pool_group', 'provider_account'] as const;
export const METRICS = ['requests', 'tokens_estimated', 'cost_usd', 'concurrency'] as const;
export const WINDOW_KINDS = ['none', 'fixed', 'calendar_day', 'calendar_week', 'manual'] as const;
export const MODES = ['enforce', 'observe', 'manual_first', 'disabled'] as const;

export const MAX_SCOPE_ID_LEN = 255;
export const DEFAULT_WINDOW_KIND = 'fixed';
export const DEFAULT_MODE = 'enforce';

export interface QuotaPolicyFormInput {
  scope_kind: string;
  scope_id: string;
  metric: string;
  window_kind: string;
  window_seconds?: string | number | null;
  limit_value: string;
  burst_value?: string | null;
  mode?: string;
  priority?: string | number | null;
  enabled?: boolean;
  valid_from?: string | null; // RFC3339 或 ''
  valid_until?: string | null; // RFC3339 或 ''
  reason?: string;
}

// 非负十进制：纯数字带可选小数（严于后端，避免发出后端会拒的科学计数/负数/非数）。
function isNonNegativeDecimal(s: string): boolean {
  return /^\d+(\.\d+)?$/.test(s.trim());
}

// 取 window_seconds 数值（空/null → null）。
function parseIntOrNull(v: string | number | null | undefined): number | null {
  if (v == null) return null;
  const s = String(v).trim();
  if (s === '') return null;
  const n = Number(s);
  return Number.isInteger(n) ? n : NaN;
}

// validateQuotaPolicyForm：逐条短路校验（与后端同序），返回首个错误文案；合法返回 null。
export function validateQuotaPolicyForm(input: QuotaPolicyFormInput): string | null {
  if (!(SCOPE_KINDS as readonly string[]).includes(input.scope_kind)) {
    return 'scope_kind 非法（global/user/api_key/channel/pool_group/provider_account）。';
  }
  const scopeID = input.scope_id.trim();
  if (scopeID === '') return "scope_id 必填（global 用 '*'）。";
  if (scopeID.length > MAX_SCOPE_ID_LEN) return `scope_id 不得超过 ${MAX_SCOPE_ID_LEN} 字符。`;

  if (!(METRICS as readonly string[]).includes(input.metric)) {
    return 'metric 非法（requests/tokens_estimated/cost_usd/concurrency）。';
  }

  const windowKind = input.window_kind.trim() === '' ? DEFAULT_WINDOW_KIND : input.window_kind.trim();
  if (!(WINDOW_KINDS as readonly string[]).includes(windowKind)) {
    return 'window_kind 非法（none/fixed/calendar_day/calendar_week/manual）。';
  }

  const ws = parseIntOrNull(input.window_seconds);
  if (Number.isNaN(ws)) return 'window_seconds 须为整数。';
  if (ws != null && ws < 0) return 'window_seconds 须 ≥ 0。';
  if (windowKind === 'fixed' && (ws == null || ws <= 0)) {
    return 'window_kind=fixed 时 window_seconds 必填且 >0。';
  }

  if (input.limit_value.trim() === '') return 'limit_value 必填。';
  if (!isNonNegativeDecimal(input.limit_value)) return 'limit_value 须为非负数。';

  if (input.burst_value != null && input.burst_value.trim() !== '' && !isNonNegativeDecimal(input.burst_value)) {
    return 'burst_value 须为非负数。';
  }

  const mode = (input.mode ?? '').trim() === '' ? DEFAULT_MODE : (input.mode ?? '').trim();
  if (!(MODES as readonly string[]).includes(mode)) {
    return 'mode 非法（enforce/observe/manual_first/disabled）。';
  }

  const pr = parseIntOrNull(input.priority);
  if (Number.isNaN(pr)) return 'priority 须为整数。';

  const from = input.valid_from?.trim() ?? '';
  const until = input.valid_until?.trim() ?? '';
  if (from !== '' && until !== '') {
    const ft = Date.parse(from);
    const ut = Date.parse(until);
    if (Number.isNaN(ft) || Number.isNaN(ut)) return 'valid_from/valid_until 须为合法时间。';
    if (ut <= ft) return 'valid_until 须晚于 valid_from。';
  }
  return null;
}

// buildQuotaPolicyBody：构造 quotaPolicyRequest。必填字段恒带；可选/指针字段按省略语义只在有值时带。
export function buildQuotaPolicyBody(input: QuotaPolicyFormInput): Record<string, unknown> {
  const body: Record<string, unknown> = {
    scope_kind: input.scope_kind,
    scope_id: input.scope_id.trim(),
    metric: input.metric,
    window_kind: input.window_kind.trim() === '' ? DEFAULT_WINDOW_KIND : input.window_kind.trim(),
    limit_value: input.limit_value.trim(),
  };
  const ws = parseIntOrNull(input.window_seconds);
  if (ws != null && !Number.isNaN(ws)) body.window_seconds = ws;
  if (input.burst_value != null && input.burst_value.trim() !== '') body.burst_value = input.burst_value.trim();
  if (input.mode != null && input.mode.trim() !== '') body.mode = input.mode.trim();
  const pr = parseIntOrNull(input.priority);
  if (pr != null && !Number.isNaN(pr)) body.priority = pr;
  if (input.enabled != null) body.enabled = input.enabled;
  if (input.valid_from != null && input.valid_from.trim() !== '') body.valid_from = input.valid_from.trim();
  if (input.valid_until != null && input.valid_until.trim() !== '') body.valid_until = input.valid_until.trim();
  if (input.reason != null && input.reason.trim() !== '') body.reason = input.reason.trim();
  return body;
}
