// 运维数据面（审计事件 / DLQ / 缓存 L2）的纯逻辑层（零依赖 → 可直接 strip-types 单测）。
// 枚举常量 + 查询构造 + 守门，逐条镜像后端真码不变式（understand workflow 实读，禁止凭记忆）：
//   - 审计 limit 1-200（默认 100，越界后端 400 invalid_limit）→ 前端钳制到合法区间避免 400。
//   - DLQ {handler} 必须是 EventKind 枚举之一；**非法名后端静默返 0 行不报错** → 前端白名单守门，避免迷惑空表。
//   - 缓存 key 可含 '/'、':' 等 → DELETE 路径段必须 URL 编码，否则破坏路由。
//   - DLQ replay id 必须正整数（后端 invalid_dlq_id）。replay 不幂等且无客户端 X-Request-Id → 前端不造幂等键。
//
// 借鉴（CLEAN-ROOM，§11/§12/§16，仅功能/字段/枚举形态，未抄码；融合 = 升级）：
//   - sub2api(LGPL)：indexed system-logs 富过滤为审计 tiebreaker；无 DLQ replay；无 cache purge 端点。
//   - new-api(AGPL)：audit 中间件自动记 + cache admin（stats/全清/GC）；无 DLQ。
//   - CLIProxyAPI@21fad9db：无审计、无 DLQ；部分=日志文件管理（无 cache 检视/清除）。
//   HUAKAI delta：DLQ 死信查看 + 逐条 replay（两家皆无，生态）；keyset 游标审计分页（架构）；按 key 选择性清缓存（算法）。

// ── 枚举白名单（与后端一致；过滤下拉 + 守门共用）─────────────────────────

export const EVENT_CLASSES = ['billing', 'pool_routing', 'rate_limit', 'oauth_refresh'] as const;
export const SEVERITIES = ['info', 'warning', 'error'] as const;
export const DLQ_EVENT_KINDS = [
  'usage_record',
  'billing_event_replica',
  'audit_event_replica',
  'audit_mismatch_refund',
  'audit_ledger_entry',
  'account_health',
  'metrics',
  'post_delivery_settlement',
  'cost_receipt_append',
] as const;
export const DLQ_STATUSES = ['pending', 'inflight', 'delivered', 'operator_review', 'dlq', 'quarantined'] as const;

export const AUDIT_LIMIT_MIN = 1;
export const AUDIT_LIMIT_MAX = 200;
export const AUDIT_LIMIT_DEFAULT = 100;

// clampAuditLimit：钳制到 [1,200]；非整数/缺省 → 默认 100。避免越界触发后端 400 invalid_limit。
export function clampAuditLimit(n: number | null | undefined): number {
  if (n == null || !Number.isInteger(n)) return AUDIT_LIMIT_DEFAULT;
  return Math.min(AUDIT_LIMIT_MAX, Math.max(AUDIT_LIMIT_MIN, n));
}

// isValidEventKind：DLQ {handler} 白名单守门（后端非法名静默 0 行，前端先拦以免迷惑）。
export function isValidEventKind(handler: string): boolean {
  return (DLQ_EVENT_KINDS as readonly string[]).includes(handler);
}

// validateDlqId：replay 的 id 必须正整数（镜像后端 invalid_dlq_id）。返回错误文案；合法 null。
export function validateDlqId(id: number): string | null {
  if (!Number.isInteger(id) || id <= 0) return 'DLQ 记录 ID 必须为正整数。';
  return null;
}

// encodeCacheKey：缓存 key 作 DELETE 路径段须 URL 编码（key 可含 '/'、':' 会破坏路由）。
export function encodeCacheKey(key: string): string {
  return encodeURIComponent(key);
}

export interface AuditEventsQueryInput {
  tenant_id: number;
  from?: string;
  to?: string;
  event_class?: string;
  event_type?: string;
  severity?: string;
  ledger_id?: string;
  actor_id?: string;
  limit?: number;
  cursor?: string;
}

// 把空串 / 'all' 视为「不过滤」→ 省略。
function omitEmpty(v?: string): string | undefined {
  if (v == null) return undefined;
  const s = v.trim();
  if (s === '' || s === 'all') return undefined;
  return s;
}

// buildAuditEventsQuery：构造 GET 查询参数对象（供 apiGet）。tenant_id 恒带；空/all 过滤省略；limit 钳制；cursor 仅非空带。
export function buildAuditEventsQuery(input: AuditEventsQueryInput): Record<string, string | number | undefined> {
  return {
    tenant_id: input.tenant_id,
    from: omitEmpty(input.from),
    to: omitEmpty(input.to),
    event_class: omitEmpty(input.event_class),
    event_type: omitEmpty(input.event_type),
    severity: omitEmpty(input.severity),
    ledger_id: omitEmpty(input.ledger_id),
    actor_id: omitEmpty(input.actor_id),
    limit: clampAuditLimit(input.limit),
    cursor: omitEmpty(input.cursor),
  };
}

// ── 展示标签（纯函数，页面与徽章共用）────────────────────────────────────

export function severityLabel(s: string): string {
  switch (s) {
    case 'error':
      return '错误';
    case 'warning':
      return '警告';
    case 'info':
      return '信息';
    default:
      return s || '未知';
  }
}

export function severityVariant(s: string): 'default' | 'secondary' | 'destructive' | 'outline' {
  if (s === 'error') return 'destructive';
  if (s === 'warning') return 'secondary';
  if (s === 'info') return 'outline';
  return 'outline';
}

export function dlqStatusLabel(s: string): string {
  switch (s) {
    case 'pending':
      return '待处理';
    case 'inflight':
      return '处理中';
    case 'delivered':
      return '已投递';
    case 'operator_review':
      return '待人工';
    case 'dlq':
      return '死信';
    case 'quarantined':
      return '已隔离';
    default:
      return s || '未知';
  }
}

export function dlqStatusVariant(s: string): 'default' | 'secondary' | 'destructive' | 'outline' {
  if (s === 'delivered') return 'default';
  if (s === 'dlq' || s === 'quarantined') return 'destructive';
  if (s === 'inflight' || s === 'pending') return 'secondary';
  return 'outline';
}
