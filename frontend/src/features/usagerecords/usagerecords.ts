import type { UsageRecord, UsageTokens } from './types'

/*
 * 用量明细的纯逻辑(可单测):请求状态(end_class)→中文+配色、费用/token 格式化、游标分页。
 * status 镜像后端 usageStatus(handler.go:259):pending_reconciliation 待对账;
 * 成功类 end_class = stream_end_graceful / non_streaming;其余(upstream_error_5xx 等)= 失败。
 */

export type Tone = 'ok' | 'warn' | 'danger' | 'muted' | 'info'

/** 后端判定为「成功」的 end_class(与 ListUsageRecords 的 outcome=success 过滤一致)。 */
const SUCCESS_CLASSES = new Set(['stream_end_graceful', 'non_streaming'])

/** 状态配色:成功→ok;待对账→warn;空→muted;其余(各类错误)→danger。 */
export function statusTone(status: string): Tone {
  const v = status.trim()
  if (!v) return 'muted'
  if (v === 'pending_reconciliation') return 'warn'
  if (SUCCESS_CLASSES.has(v)) return 'ok'
  return 'danger'
}

/** 已知状态给中文标签;未知错误类原样回显(保留诊断信息不丢)。 */
const STATUS_LABELS: Record<string, string> = {
  pending_reconciliation: '待对账',
  stream_end_graceful: '成功',
  non_streaming: '成功',
}

export function statusLabel(status: string): string {
  const v = status.trim()
  if (!v) return '—'
  return STATUS_LABELS[v] ?? v
}

/** 是否成功状态(用于汇总成功率等场景)。 */
export function isSuccess(status: string): boolean {
  return SUCCESS_CLASSES.has(status.trim())
}

/**
 * 费用格式化:后端是定点小数串(如 "0.01000000")。转 USD 展示,去尾零但保留有效精度;
 * 极小额(<0.0001)用更多小数避免显示成 $0.00。非数字原样。
 */
export function formatCost(actualCost: string): string {
  const v = actualCost.trim()
  if (!v) return '—'
  const n = Number(v)
  if (!Number.isFinite(n)) return v
  if (n === 0) return '$0'
  // 非零但小于最小可展示精度(1e-6)时显示阈值形态,不塌成 $0(计费日志诚实性:
  // 真实发生过的微小扣费不能显示成 0 误导用户)。
  if (Math.abs(n) < 0.000001) return n > 0 ? '<$0.000001' : '>-$0.000001'
  // 选取能体现该值的小数位:大额 4 位足够,极小额放宽到 6 位。
  const decimals = Math.abs(n) >= 0.0001 ? 4 : 6
  const fixed = n.toFixed(decimals)
  // 去尾零(但保留至少 2 位小数,符合金额观感)。
  const trimmed = fixed.replace(/(\.\d*?)0+$/, '$1').replace(/\.$/, '')
  const parts = trimmed.split('.')
  if (parts.length === 2 && parts[1].length === 1) {
    return `$${parts[0]}.${parts[1]}0`
  }
  return `$${trimmed}`
}

/** token 总数(input+output,不含 cache;cache 单列展示)。 */
export function totalTokens(tokens: UsageTokens): number {
  return (tokens.input || 0) + (tokens.output || 0)
}

/** token 简明摘要:"入 10 / 出 20"(+缓存创建/读,若有)。 */
export function tokensSummary(tokens: UsageTokens): string {
  let s = `入 ${tokens.input || 0} / 出 ${tokens.output || 0}`
  if (tokens.cache_creation) s += ` / 缓存写 ${tokens.cache_creation}`
  if (tokens.cache_read) s += ` / 缓存读 ${tokens.cache_read}`
  return s
}

/** 模型展示:优先 requested,上游不同则附注。 */
export function modelDisplay(record: Pick<UsageRecord, 'requested_model' | 'upstream_model'>): string {
  const req = record.requested_model.trim()
  const up = record.upstream_model.trim()
  if (!req) return up || '—'
  if (up && up !== req) return `${req} → ${up}`
  return req
}

/** 游标分页:next_cursor 非空即还有下一页。 */
export function hasMore(nextCursor: string): boolean {
  return nextCursor.trim() !== ''
}

// ── 用量 CSV 导出(GET /v1/me/usage/export.csv)的纯逻辑 ────────────────────────
// 后端 meexporthttp 经 exporthttp.ParseExportRange 强制 from/to 两个 RFC3339 参数:
//   ① 缺任一 → 400 from_required / to_required;
//   ② 非 RFC3339 → 400 *_invalid;
//   ③ from 晚于 to → 400 invalid_date_range;
//   ④ 跨度 > 366 天 → 400 date_range_too_large。
// 这里前端先行校验,避免无谓请求;并把日期选择器的「YYYY-MM-DD」转成后端要的 RFC3339。

/** 导出窗口最大跨度(对齐后端 maxExportWindow = 366 天)。 */
export const MAX_EXPORT_DAYS = 366

/**
 * 把 <input type="date"> 的 'YYYY-MM-DD' 转成当天 UTC 起点的 RFC3339(from)。
 * 非法/空串返回空串(由 buildExportQuery / validateExportRange 拦截)。
 */
export function dayStartRFC3339(day: string): string {
  const v = day.trim()
  if (!/^\d{4}-\d{2}-\d{2}$/.test(v)) return ''
  const d = new Date(`${v}T00:00:00.000Z`)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString()
}

/**
 * 把 'YYYY-MM-DD' 转成当天 UTC 终点(次日零点)的 RFC3339(to,半开区间右界)。
 * 这样选「同一天」也能覆盖整日数据,而非零跨度。
 */
export function dayEndRFC3339(day: string): string {
  const v = day.trim()
  if (!/^\d{4}-\d{2}-\d{2}$/.test(v)) return ''
  const d = new Date(`${v}T00:00:00.000Z`)
  if (Number.isNaN(d.getTime())) return ''
  d.setUTCDate(d.getUTCDate() + 1)
  return d.toISOString()
}

/**
 * 校验导出日期范围(输入为两个 'YYYY-MM-DD')。通过返回 null,否则返回中文错误。
 * 判别核心:from 晚于 to 必须拦下(变异成放行 → 后端 400 invalid_date_range → RED);
 *          跨度 > 366 天必须拦下(变异成放行 → 后端 400 date_range_too_large → RED)。
 */
export function validateExportRange(fromDay: string, toDay: string): string | null {
  const from = dayStartRFC3339(fromDay)
  const to = dayStartRFC3339(toDay)
  if (!from || !to) return '请选择有效的起止日期'
  const fromMs = Date.parse(from)
  const toMs = Date.parse(to)
  if (fromMs > toMs) return '开始日期不能晚于结束日期'
  // 含右界整日,跨度按天数计;> 366 天后端会拒。
  const days = Math.round((toMs - fromMs) / 86_400_000) + 1
  if (days > MAX_EXPORT_DAYS) return `导出范围不能超过 ${MAX_EXPORT_DAYS} 天`
  return null
}

/**
 * 构造导出查询参数:固定 format=csv,from/to 转成 RFC3339。
 * 判别核心:from 用当天起点、to 用次日零点(右界半开),且 format 必须为 csv
 * (变异成漏 from/to 或用错边界 → 后端 400 / 数据缺失 → RED)。
 */
export function buildExportQuery(fromDay: string, toDay: string): { format: string; from: string; to: string } {
  return { format: 'csv', from: dayStartRFC3339(fromDay), to: dayEndRFC3339(toDay) }
}

/** 默认导出范围:最近 N 天(含今天),返回 [fromDay, toDay] 的 'YYYY-MM-DD'。 */
export function defaultExportRange(days = 30, now: Date = new Date()): { fromDay: string; toDay: string } {
  const toDay = isoDay(now)
  const from = new Date(now)
  from.setUTCDate(from.getUTCDate() - (days - 1))
  return { fromDay: isoDay(from), toDay }
}

/** Date → 'YYYY-MM-DD'(UTC)。 */
function isoDay(d: Date): string {
  return d.toISOString().slice(0, 10)
}

// ── 成本下钻 / 签名收据 / 我的争议(session 只读)纯逻辑 ──────────────────────────

/**
 * 把 request_id 编码成收据路由路径段。后端 /v1/receipts 同时挂了 {request_id} 与
 * {request_id_host}/{request_id_tail} 两套路由,且校验「至多一个斜杠」(validateReceiptPathRequestID,
 * cost_receipt_handler.go:654)。因此:
 *  - 含一个斜杠(host/tail 形态)→ 按段 encodeURIComponent 后用「字面斜杠」拼回,命中双段路由;
 *  - 无斜杠 → 整体 encodeURIComponent。
 * 判别核心:斜杠必须保留为路径分隔符不能被编码成 %2F(变异成整体编码 → chi 不命中双段路由 →
 * 404 → RED);各段内的特殊字符必须编码(防路径注入 / 多余斜杠绕过)。
 */
export function encodeReceiptRequestID(requestID: string): string {
  const v = requestID.trim()
  const idx = v.indexOf('/')
  if (idx < 0) return encodeURIComponent(v)
  const host = v.slice(0, idx)
  const tail = v.slice(idx + 1)
  return `${encodeURIComponent(host)}/${encodeURIComponent(tail)}`
}

/** 微美分(整数)→ USD 展示。复用 formatCost 的诚实性策略(微小额不塌成 $0)。 */
export function formatMicroUSD(microUSD: number): string {
  if (!Number.isFinite(microUSD)) return '—'
  // 1 USD = 1_000_000 micro-USD;转成定点小数串再交 formatCost 统一格式化。
  return formatCost((microUSD / 1_000_000).toFixed(8))
}

/**
 * 验签整体结论 → 中文标签 + 配色。镜像后端 receiptVerifyResponse:
 *  - valid=true                          → 已验签可信(ok)
 *  - signature_valid=true 但 valid=false → 签名有效但收据不被采信(撤销/窗口外/哈希不符)(warn)
 *  - 其余(签名无效 / 未签名 / 不匹配)   → 验签失败(danger)
 * 判别核心:valid 与 signature_valid 必须分别判定(变异成只看 signature_valid → 把
 * key_revoked/窗口外这类「签名有效但不可信」误标成可信 → RED)。
 */
export function verifyTone(resp: { valid: boolean; signature_valid: boolean }): Tone {
  if (resp.valid) return 'ok'
  if (resp.signature_valid) return 'warn'
  return 'danger'
}

export function verifyLabel(resp: { valid: boolean; signature_valid: boolean }): string {
  if (resp.valid) return '已验签 · 可信'
  if (resp.signature_valid) return '签名有效但不被采信'
  return '验签失败'
}

/** 验签 status 机器码 → 中文(取证用);未知码原样保留诊断信息。 */
const VERIFY_STATUS_LABELS: Record<string, string> = {
  'signed-only': '已签名',
  unverified: '未采信',
  mismatch: '不匹配',
  missing: '缺失',
}

export function verifyStatusLabel(status: string | undefined): string {
  const v = (status ?? '').trim()
  if (!v) return '—'
  return VERIFY_STATUS_LABELS[v] ?? v
}

/** 争议状态 → 中文 + 配色。镜像后端 status(open/resolved/rejected 等);未知码原样。 */
const DISPUTE_STATUS_LABELS: Record<string, string> = {
  open: '待处理',
  pending: '待处理',
  resolved: '已解决',
  approved: '已批准',
  rejected: '已驳回',
  closed: '已关闭',
}

export function disputeStatusLabel(status: string): string {
  const v = status.trim()
  if (!v) return '—'
  return DISPUTE_STATUS_LABELS[v.toLowerCase()] ?? v
}

export function disputeStatusTone(status: string): Tone {
  const v = status.trim().toLowerCase()
  if (!v) return 'muted'
  if (v === 'resolved' || v === 'approved' || v === 'closed') return 'ok'
  if (v === 'rejected') return 'danger'
  // open / pending / 其它进行中态
  return 'warn'
}

// ── 发起争议(POST /v1/receipts/{request_id}/disputes)的纯逻辑 ──────────────────
// 后端 validateCreateDispute(dispute_store.go:189)对 reason 的约束:
//   ① 去空白后为空 → 400 invalid_dispute_request(ErrDisputeInvalid: reason required);
//   ② 去空白后长度 > 4000 字节 → 400(ErrDisputeInvalid: reason too long)。
// 语义:本端点只建 pending 争议记录,裁决/退款由 admin 侧 /v1/admin/disputes/{id}/resolve 处理,
// 不立即动钱。前端先行校验避免无谓请求,并给二次确认文案明示「待运营审核、不会立即退款」。

/** 争议原因最大长度(对齐后端 dispute_store.go:199 的 4000)。 */
export const MAX_DISPUTE_REASON_LEN = 4000

/**
 * 校验争议原因(输入为用户填写的 reason)。通过返回 null,否则返回中文错误。
 * 判别核心:去空白后为空必须拦下(变异成放行 → 后端 400 reason required → RED);
 *          去空白后超 4000 字必须拦下(变异成放行 → 后端 400 reason too long → RED)。
 * 注意:长度按「去空白后」计,与后端 len(strings.TrimSpace(in.Reason)) 一致——
 * 仅靠首尾空白凑长度不能绕过(变异成按原始长度判 → 与后端不一致 → RED)。
 */
export function validateDisputeReason(reason: string): string | null {
  const v = reason.trim()
  if (!v) return '请填写争议原因'
  if (v.length > MAX_DISPUTE_REASON_LEN) return `争议原因不能超过 ${MAX_DISPUTE_REASON_LEN} 字`
  return null
}
