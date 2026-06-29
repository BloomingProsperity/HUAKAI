/*
 * 费率版本 / 快照(公开只读)前端类型 + 纯逻辑(可单测)。
 *
 * 镜像后端 internal/billing/rate_table_source.go 的 JSON 形态:
 *  - RateTableSnapshot(列表轻量行):{id, version, effective_from, effective_to?, created_at}
 *    端点 GET /v1/pricing/snapshots(backend/internal/gatewayhttp/cost_receipt_handler.go:572),
 *    响应包裹为 {snapshots: [...]}。
 *  - RateTable(单版本/单快照详情,含 pricing_data 原始 JSON):
 *    {id, version, pricing_data, effective_from, effective_to?, created_at}
 *    端点 GET /v1/pricing/rate-table?version=X(handler:548)
 *    与 GET /v1/pricing/snapshots/{snapshot_id}(handler:587)。
 *
 * pricing_data 是后端 jsonb 原样透传(billing_pricing_versions.pricing_data),前端不预设其内部结构,
 * 只做「能解析成 model→price 行就表格化、否则原样 JSON 展示」的兜底渲染。所有逻辑纯只读。
 */

/** 列表行:历史 version 的轻量快照。 */
export interface RateTableSnapshot {
  id: number
  version: string
  effective_from: string
  effective_to?: string | null
  created_at: string
}

/** snapshots 列表端点响应包裹。 */
export interface SnapshotsResponse {
  snapshots: RateTableSnapshot[] | null
}

/** 单版本/单快照详情(含原始定价数据)。 */
export interface RateTable {
  id: number
  version: string
  pricing_data: unknown
  effective_from: string
  effective_to?: string | null
  created_at: string
}

/** 从可能为 null 的响应里取出 snapshots 数组(后端无数据时 snapshots 可能为 null)。 */
export function snapshotList(resp: SnapshotsResponse | null | undefined): RateTableSnapshot[] {
  if (!resp || !resp.snapshots) return []
  return resp.snapshots
}

/** 该快照是否仍生效(effective_to 为空 = 当前生效)。 */
export function isActiveSnapshot(s: { effective_to?: string | null }): boolean {
  return s.effective_to == null || s.effective_to === ''
}

/**
 * 格式化生效区间为可读串。
 *  - 有 effective_to:`<from> → <to>`
 *  - 无 effective_to:`<from> → 至今`
 * 时间走本地化短格式;非法/空时间显示 "—"。
 */
export function formatEffectiveRange(s: { effective_from: string; effective_to?: string | null }): string {
  const from = formatTime(s.effective_from)
  if (isActiveSnapshot(s)) return `${from} → 至今`
  return `${from} → ${formatTime(s.effective_to as string)}`
}

/** ISO 时间串 → 本地化「YYYY-MM-DD HH:mm」;空/非法 → "—"。 */
export function formatTime(iso?: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** 定价行:从 pricing_data 解析出的一行(model + 任意键值)。 */
export interface PricingDataRow {
  model: string
  /** 该 model 对应的原始值(可能是对象/数字/字符串)。 */
  value: unknown
}

/**
 * 尽力把 pricing_data 解析成「model → 值」的表格行,供详情面板表格化展示。
 * 兼容两种常见形态:
 *  ① 对象 map:{ "<model>": {...} } → 每个键一行;
 *  ② 数组:[{model|name|id: "<model>", ...}] → 每项一行(取 model/name/id 作 model 名)。
 * 解析不出结构(原始标量 / 形态不识别)→ 返回空数组,上层退回原始 JSON 文本展示。
 */
export function parsePricingRows(data: unknown): PricingDataRow[] {
  if (data == null) return []
  if (Array.isArray(data)) {
    const rows: PricingDataRow[] = []
    for (const entry of data) {
      if (entry && typeof entry === 'object') {
        const rec = entry as Record<string, unknown>
        const name = rec.model ?? rec.name ?? rec.id
        if (typeof name === 'string' && name !== '') {
          rows.push({ model: name, value: entry })
        }
      }
    }
    return rows
  }
  if (typeof data === 'object') {
    const rec = data as Record<string, unknown>
    return Object.keys(rec).map((model) => ({ model, value: rec[model] }))
  }
  return []
}

/** 把一行的值压成单行可读串(对象 → 紧凑 JSON,标量 → 字符串)。 */
export function summarizeValue(value: unknown): string {
  if (value == null) return '—'
  if (typeof value === 'object') {
    try {
      return JSON.stringify(value)
    } catch {
      return '[object]'
    }
  }
  return String(value)
}

/** 原始 JSON 的美化文本(详情面板「原始数据」区);失败兜底为 "{}"。 */
export function prettyJSON(data: unknown): string {
  try {
    return JSON.stringify(data ?? {}, null, 2)
  } catch {
    return '{}'
  }
}
