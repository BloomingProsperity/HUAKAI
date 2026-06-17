// admin CSV 导出的纯逻辑（区间校验 + 下载 URL 构造），零依赖 strip-types 单测。
// 后端真码: internal/exporthttp/export.go
//   - MountRoutes: GET /v1/admin/{payments,usage,orders,refunds}/export.csv
//   - parseExportRange: from/to(RFC3339)必填、from<=to、窗口<=maxExportWindow(366 天)
//   - tenant 由 admin token scope 解析(不走 query)；payments 另有可选 status 过滤
//   - GET 无 DisallowUnknownFields，多余 query 参数被后端忽略

export type ExportKind = 'payments' | 'usage' | 'orders' | 'refunds';
export const EXPORT_KINDS: ExportKind[] = ['payments', 'usage', 'orders', 'refunds'];

// 后端 maxExportWindow = 366 天。
export const MAX_EXPORT_WINDOW_MS = 366 * 24 * 60 * 60 * 1000;

// 严格 RFC3339：必须含 T 分隔符、秒、以及时区(Z 或 ±HH:MM)。
// 后端 time.Parse(time.RFC3339) 同样严格——不能用 Date.parse 的宽松解析(它接受
// 无偏移/空格分隔/纯日期串，会被后端 400 拒绝，且无偏移串按本地 TZ 解析致窗口算偏)。
const RFC3339_RE = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;

export interface ExportRangeParams {
  from: string; // RFC3339
  to: string; // RFC3339
  status?: string; // 可选状态过滤(payments/orders 等)
}

// validateExportRange 镜像后端 parseExportRange 硬约束：from/to 必填、from<=to、跨度<=366 天。
// 返回错误串（给 UI）或 null。
export function validateExportRange(from: string, to: string): string | null {
  const f0 = from.trim();
  const t0 = to.trim();
  if (!f0 || !t0) return 'from 与 to 必填';
  if (!RFC3339_RE.test(f0)) return 'from 需为合法 RFC3339 时间(含 T 与时区，如 2026-01-01T00:00:00Z)';
  if (!RFC3339_RE.test(t0)) return 'to 需为合法 RFC3339 时间(含 T 与时区，如 2026-01-01T00:00:00Z)';
  const f = Date.parse(f0);
  const t = Date.parse(t0);
  // 形状已过 RFC3339 正则，但仍可能有语义非法(如月份 99)——Date.parse 返回 NaN 时拒。
  if (Number.isNaN(f) || Number.isNaN(t)) return 'from/to 不是有效日期';
  if (f > t) return 'from 必须早于或等于 to';
  if (t - f > MAX_EXPORT_WINDOW_MS) return '导出时间跨度不得超过 366 天';
  return null;
}

// buildExportUrl 构造 GET /v1/admin/{kind}/export.csv 的下载 URL（from/to + 可选 status）。
// tenant_id 不带（后端由 admin token scope 解析）。
export function buildExportUrl(kind: ExportKind, params: ExportRangeParams): string {
  const qs = new URLSearchParams({ from: params.from, to: params.to });
  if (params.status && params.status.trim()) qs.set('status', params.status);
  return `/v1/admin/${kind}/export.csv?${qs.toString()}`;
}
