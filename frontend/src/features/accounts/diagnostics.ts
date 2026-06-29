import type { AccountHealth, AccountTestResult } from './types'

/*
 * 账号诊断/批量调参的纯展示与校验逻辑(与 React 解耦,便于 vitest 变异测试)。
 * 对应后端三 handler:
 * - provider_account_test_handler.go(test 结果)
 * - provider_account_health_handler.go(health 字段)
 * - provider_account_bulk_handler.go(bulk-by-tag 请求约束)
 */

export interface TestSummary {
  label: string
  tone: 'ok' | 'fail'
}

// error_class → 中文短语。后端凭证试运行的 error_class 是粗粒度枚举(temporary/permanent 等),
// 未知值兜底为通用文案并附原始码,便于排错。
const TEST_ERROR_LABELS: Record<string, string> = {
  permanent: '凭证永久失效(需更换/重授权)',
  temporary: '临时不可用(上游/网络抖动)',
  rate_limited: '上游限流',
  invalid_credential: '凭证无效',
  network: '网络错误',
}

/**
 * testSummary 把 POST /{id}/test 结果转成展示用 {label, tone}。
 * ok=true → message(或"连通正常")+ ok;否则 → 中文错误短语(含 error_class)+ fail。
 * 变异:若忽略 res.ok 恒判失败,则 ok 用例的 tone 断言转红。
 */
export function testSummary(res: AccountTestResult): TestSummary {
  if (res.ok) {
    return { label: res.message?.trim() ? res.message : '连通正常', tone: 'ok' }
  }
  const cls = res.error_class ?? ''
  const reason = TEST_ERROR_LABELS[cls] ?? `校验失败${cls ? `(${cls})` : ''}`
  // 失败时若后端带了更具体的 message,拼在错误短语后面(便于一眼看到根因)。
  const detail = res.message?.trim()
  return { label: detail ? `${reason} · ${detail}` : reason, tone: 'fail' }
}

/**
 * healthRows 把 AccountHealth 拍平成详情页 Grid 用的 [标签, 值] 列表。
 * recent_requests 只在后端下发时追加(ring 为 nil 或无数据时该字段缺省)。
 */
export function healthRows(h: AccountHealth): [string, string][] {
  const rows: [string, string][] = [
    ['健康态', h.health_state || '—'],
    ['是否启用', h.enabled ? '是' : '否'],
    ['需人工处理', h.requires_action ? '是 ⚠' : '否'],
    ['失败次数', String(h.failure_count)],
    ['失败分类', h.failure_class || '—'],
    ['健康态有效至', fmtTime(h.health_state_until)],
    ['探测延迟(ms)', h.last_probe_latency_ms != null ? String(h.last_probe_latency_ms) : '—'],
    ['最近探测', fmtTime(h.last_probe_at)],
    ['模型同步检查', fmtTime(h.model_sync_last_check_at)],
    ['5h 会话窗起', fmtTime(h.session_window_5h_start)],
    ['5h 会话窗止', fmtTime(h.session_window_5h_end)],
    ['5h 会话窗态', h.session_window_5h_status || '—'],
    ['最近刷新', fmtTime(h.last_refresh_at)],
    ['刷新结果', h.last_refresh_outcome || '—'],
    ['更新于', fmtTime(h.updated_at)],
  ]
  if (h.recent_requests) {
    const r = h.recent_requests
    rows.push(['近期请求', `共 ${r.total} · 成功 ${r.success} · 失败 ${r.failure}`])
  }
  return rows
}

// fmtTime:ISO 字符串 → 本地化;空/非法 → "—"。
function fmtTime(iso: string | null | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false })
}

// ---- 按标签批量调参表单 ----

export interface BulkByTagForm {
  tag: string
  // 三个旋钮:空字符串=不改;'true'/'false'=enabled;数字串=priority/static_weight。
  enabled: '' | 'true' | 'false'
  priority: string
  staticWeight: string
}

export const EMPTY_BULK_FORM: BulkByTagForm = { tag: '', enabled: '', priority: '', staticWeight: '' }

export interface BulkByTagPayload {
  tag: string
  enabled?: boolean
  priority?: number
  static_weight?: number
}

/**
 * buildBulkPayload 把表单转成请求体或返回校验错误。
 * 后端硬约束(provider_account_bulk_handler.go):tag 非空 + 至少一项 enabled/priority/static_weight。
 * priority/static_weight 必须是合法整数才下发(空串=不改;非法=报错)。
 * 返回 { payload } 或 { error };变异(去掉"至少一项"检查 / 去掉整数校验)→ 对应用例转红。
 */
export function buildBulkPayload(f: BulkByTagForm): { payload: BulkByTagPayload } | { error: string } {
  const tag = f.tag.trim()
  if (tag === '') return { error: '标签必填' }

  const payload: BulkByTagPayload = { tag }

  if (f.enabled === 'true') payload.enabled = true
  else if (f.enabled === 'false') payload.enabled = false

  if (f.priority.trim() !== '') {
    const p = Number.parseInt(f.priority, 10)
    if (!Number.isInteger(p) || String(p) !== f.priority.trim()) return { error: '优先级须为整数' }
    payload.priority = p
  }

  if (f.staticWeight.trim() !== '') {
    const w = Number.parseInt(f.staticWeight, 10)
    if (!Number.isInteger(w) || String(w) !== f.staticWeight.trim()) return { error: '静态权重须为整数' }
    if (w < 0) return { error: '静态权重不能为负' }
    payload.static_weight = w
  }

  // 后端要求三者至少其一;否则返回 no_field_to_set。
  if (payload.enabled === undefined && payload.priority === undefined && payload.static_weight === undefined) {
    return { error: '至少改一项:启用/优先级/静态权重' }
  }
  return { payload }
}
