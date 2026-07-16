import type {
  BatchCreateVoucherRequest,
  CreateVoucherRequest,
  Voucher,
  VoucherStatus,
} from './types'
import type { BadgeTone } from '../../ui/StatusBadge'

/*
 * 兑换码管理纯逻辑(可单测):创建/批量请求构造 + 校验、金额换算、状态配色与中文标签、
 * 批量数量上限(后端硬约束 1..1000)。所有面额内部以「分」(amount_cents)与后端对齐,
 * 表单录入用「元」,在此换算,避免浮点误差用四舍五入到分。
 */

/** 后端 store_postgres 批量上限:单批 ≤ 1000(见 routes 说明/批量创建路径)。 */
export const MAX_BATCH_COUNT = 1000
/** 列表 limit 后端约束:1..200。 */
export const MAX_LIST_LIMIT = 200

/** 元 → 分:四舍五入到整数分(1 元 = 100 分)。非数/负数 → NaN 由调用方拦。 */
export function yuanToCents(yuan: number): number {
  return Math.round(yuan * 100)
}

/** 分 → 元字符串:固定两位小数,便于列表展示。 */
export function centsToYuan(cents: number): string {
  return (cents / 100).toFixed(2)
}

/** 券状态 → 徽章语气。active 正常(ok),revoked 危险,expired/exhausted 警告,其余中性。 */
export function statusTone(status: string): BadgeTone {
  switch (status) {
    case 'active':
      return 'ok'
    case 'revoked':
      return 'danger'
    case 'expired':
    case 'exhausted':
      return 'warn'
    default:
      return 'muted'
  }
}

/** 券状态中文标签。 */
export function statusLabel(status: string): string {
  switch (status as VoucherStatus) {
    case 'active':
      return '可用'
    case 'expired':
      return '已过期'
    case 'exhausted':
      return '已用尽'
    case 'revoked':
      return '已吊销'
    default:
      return status
  }
}

/** 批次状态中文标签。 */
export function batchStatusLabel(status: string): string {
  switch (status) {
    case 'active':
      return '进行中'
    case 'completed':
      return '已完成'
    case 'failed':
      return '失败'
    case 'revoked':
      return '已吊销'
    default:
      return status
  }
}

/** 授予种类中文标签。 */
export function grantKindLabel(kind: string): string {
  switch (kind) {
    case 'balance':
      return '余额'
    case 'subscription':
      return '订阅'
    default:
      return kind || '余额'
  }
}

export interface VoucherStatView {
  label: string
  value: string
  hint: string
  tone: 'default' | 'danger' | 'warn' | 'ok'
}

/** 已加载兑换码到四张统计卡的纯映射；无 count 端点，因此只陈述当前页口径。 */
export function mapVoucherStats(vouchers: Voucher[] | null): VoucherStatView[] {
  if (vouchers === null) {
    return [
      { label: '总数', value: '—', hint: '当前页数据加载中', tone: 'default' },
      { label: '可用', value: '—', hint: '当前页数据加载中', tone: 'ok' },
      { label: '已用尽', value: '—', hint: '当前页数据加载中', tone: 'warn' },
      { label: '已吊销', value: '—', hint: '当前页数据加载中', tone: 'danger' },
    ]
  }
  const count = (status: VoucherStatus) => vouchers.filter((voucher) => voucher.status === status).length
  const format = (value: number) => `${value.toLocaleString('zh-CN')} 张`
  return [
    { label: '总数', value: format(vouchers.length), hint: '当前页口径（最多 200 条）', tone: 'default' },
    { label: '可用', value: format(count('active')), hint: '当前页口径', tone: 'ok' },
    { label: '已用尽', value: format(count('exhausted')), hint: '当前页口径', tone: 'warn' },
    { label: '已吊销', value: format(count('revoked')), hint: '当前页口径', tone: 'danger' },
  ]
}

export interface VoucherTableRow {
  id: number
  source: Voucher
  fingerprint: string
  amount: string
  grantKind: string
  redemption: string
  statusText: string
  statusTone: BadgeTone
  batchID: number | null
  validity: string
}

/** 后端兑换码到九列表格行的纯映射；保留 source 供吊销动作使用。 */
export function mapVoucherRows(vouchers: Voucher[]): VoucherTableRow[] {
  return vouchers.map((voucher) => ({
    id: voucher.id,
    source: voucher,
    fingerprint: voucher.code_fingerprint,
    amount: `${centsToYuan(voucher.amount_cents)} ${voucher.currency_code}`,
    grantKind: grantKindLabel(voucher.grant_kind),
    redemption: `${voucher.redeemed_count}/${voucher.max_redemptions}`,
    statusText: statusLabel(voucher.status),
    statusTone: statusTone(voucher.status),
    batchID: voucher.batch_id ?? null,
    validity: `${formatVoucherDate(voucher.valid_from)} ~ ${formatVoucherDate(voucher.valid_until)}`,
  }))
}

/** 列表时间展示；非法时间统一显示占位，避免渲染 Invalid Date。 */
export function formatVoucherDate(iso: string): string {
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false })
}

/** datetime-local(无时区)→ ISO8601;空串/非法 → 空串(调用方据此判错)。 */
export function toIso(local: string): string {
  const v = local.trim()
  if (!v) return ''
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString()
}

/** 创建表单(单张)。面额以元录入,有效期用 datetime-local。 */
export interface CreateForm {
  tenantId: string
  amountYuan: string
  currencyCode: string
  code: string
  validFrom: string
  validUntil: string
  maxRedemptions: string
  singleUsePerUser: boolean
}

export const EMPTY_CREATE_FORM: CreateForm = {
  tenantId: '1',
  amountYuan: '',
  currencyCode: 'USD',
  code: '',
  validFrom: '',
  validUntil: '',
  maxRedemptions: '1',
  singleUsePerUser: true,
}

/** 批量表单。在单张基础上加数量 count,去掉自定义 code(批量由后端随机生成)。 */
export interface BatchForm {
  tenantId: string
  count: string
  amountYuan: string
  currencyCode: string
  validFrom: string
  validUntil: string
  maxRedemptions: string
  singleUsePerUser: boolean
}

export const EMPTY_BATCH_FORM: BatchForm = {
  tenantId: '1',
  count: '10',
  amountYuan: '',
  currencyCode: 'USD',
  validFrom: '',
  validUntil: '',
  maxRedemptions: '1',
  singleUsePerUser: true,
}

/** 解析正整数;失败返回 null。用于 tenant_id / max_redemptions / count 这些非负字段。 */
function parsePositiveInt(raw: string): number | null {
  const v = raw.trim()
  if (!/^\d+$/.test(v)) return null
  const n = Number(v)
  return n > 0 ? n : null
}

/** 校验有效期窗口:两端都填、都合法、且 from < until。返回 ISO 对或错误。 */
function buildWindow(from: string, until: string): { from: string; until: string } | { error: string } {
  const f = toIso(from)
  const u = toIso(until)
  if (!f || !u) return { error: '请填写完整且合法的有效期起止时间' }
  if (new Date(f).getTime() >= new Date(u).getTime()) return { error: '生效时间必须早于失效时间' }
  return { from: f, until: u }
}

/**
 * 构造单张创建请求。校验:tenant_id 正整数、面额>0、有效期窗口合法、max_redemptions 正整数。
 * code 留空表示由后端随机生成(不下发空串字段)。
 */
export function buildCreateRequest(form: CreateForm): CreateVoucherRequest | { error: string } {
  const tenantId = parsePositiveInt(form.tenantId)
  if (tenantId === null) return { error: '租户 ID 必须为正整数' }
  const amount = Number(form.amountYuan)
  if (!form.amountYuan.trim() || Number.isNaN(amount) || amount <= 0) return { error: '面额必须为正数' }
  const win = buildWindow(form.validFrom, form.validUntil)
  if ('error' in win) return win
  const maxR = parsePositiveInt(form.maxRedemptions)
  if (maxR === null) return { error: '最大兑换次数必须为正整数' }
  const req: CreateVoucherRequest = {
    tenant_id: tenantId,
    amount_cents: yuanToCents(amount),
    currency_code: form.currencyCode.trim() || 'USD',
    valid_from: win.from,
    valid_until: win.until,
    max_redemptions: maxR,
    single_use_per_user: form.singleUsePerUser,
  }
  const code = form.code.trim()
  if (code) req.code = code
  return req
}

/**
 * 构造批量创建请求。除单张校验外,数量 count 必须 1..MAX_BATCH_COUNT(后端硬上限 1000),
 * 客户端先挡住超限,给清晰提示而非让后端 400。
 */
export function buildBatchRequest(form: BatchForm): BatchCreateVoucherRequest | { error: string } {
  const tenantId = parsePositiveInt(form.tenantId)
  if (tenantId === null) return { error: '租户 ID 必须为正整数' }
  const count = parsePositiveInt(form.count)
  if (count === null) return { error: '生成数量必须为正整数' }
  // 判别核心:数量必须 ≤ MAX_BATCH_COUNT。变异(去掉此上限校验)→ count=1001 通过 → RED。
  if (count > MAX_BATCH_COUNT) return { error: `单批最多生成 ${MAX_BATCH_COUNT} 张` }
  const amount = Number(form.amountYuan)
  if (!form.amountYuan.trim() || Number.isNaN(amount) || amount <= 0) return { error: '面额必须为正数' }
  const win = buildWindow(form.validFrom, form.validUntil)
  if ('error' in win) return win
  const maxR = parsePositiveInt(form.maxRedemptions)
  if (maxR === null) return { error: '最大兑换次数必须为正整数' }
  return {
    tenant_id: tenantId,
    count,
    amount_cents: yuanToCents(amount),
    currency_code: form.currencyCode.trim() || 'USD',
    valid_from: win.from,
    valid_until: win.until,
    max_redemptions: maxR,
    single_use_per_user: form.singleUsePerUser,
  }
}

/** 校验列表筛选的 tenant_id(后端列表必填正整数)。limit 在 UI 层固定 ≤ MAX_LIST_LIMIT。 */
export function parseListTenantId(raw: string): number | null {
  return parsePositiveInt(raw)
}

/** 客户端二次过滤:按状态筛已加载列表(后端列表不按状态过滤,故在前端做)。 */
export function filterByStatus<T extends { status: string }>(vouchers: T[], status: string): T[] {
  if (!status) return vouchers
  return vouchers.filter((v) => v.status === status)
}
