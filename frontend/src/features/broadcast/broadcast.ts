/*
 * 站内信广播页的纯逻辑(可单测,无 DOM/网络副作用):
 *   - 广播表单校验(镜像后端 normalizeBroadcastInput 的硬约束,service.go:102-126)
 *   - 严重级别 → 中文标签 / 徽章语气
 *   - worker 失败率展示
 * 全部同步纯函数,便于 §14 变异测试打红。
 */

import type { BadgeTone } from '../../ui/StatusBadge'
import type { BroadcastRequest, Severity } from './types'

/** 单租户部署默认租户 ID;顶栏可改。与 alerting 页同约定。 */
export const DEFAULT_TENANT_ID = 1

/** 合法的严重级别集合,镜像后端 service.go:117-118 的 switch 白名单。 */
export const SEVERITIES: ReadonlyArray<{ value: Severity; label: string }> = [
  { value: 'info', label: '普通(info)' },
  { value: 'warning', label: '警告(warning)' },
  { value: 'critical', label: '严重(critical)' },
]

/** 表单校验结果:ok 时携带可提交的请求体,否则带中文错误说明。 */
export type BroadcastValidation =
  | { ok: true; value: BroadcastRequest }
  | { ok: false; error: string }

/**
 * 校验广播表单(镜像后端 normalizeBroadcastInput,service.go:102-126):
 *   - tenant_id 必须为正整数(否则后端 tenant_id_required / ErrInvalidInput)
 *   - title trim 后非空、body trim 后非空(后端先 trim 再判空,service.go:105-106/114)
 *   - severity 必须是 info/warning/critical 之一(后端 default 分支返回 ErrInvalidInput)
 * 判别核心:title/body 仅含空白时必须拒(模拟后端 trim 后判空);非法 severity 必须拒。
 * 前端先拦避免无谓 400;后端仍是权威。
 */
export function validateBroadcast(
  tenantId: number,
  form: { title: string; body: string; severity: Severity },
): BroadcastValidation {
  if (!Number.isInteger(tenantId) || tenantId <= 0) {
    return { ok: false, error: 'tenant_id 必须为正整数' }
  }
  const title = form.title.trim()
  if (title === '') {
    return { ok: false, error: '标题不能为空' }
  }
  const body = form.body.trim()
  if (body === '') {
    return { ok: false, error: '正文不能为空' }
  }
  if (!SEVERITIES.some((s) => s.value === form.severity)) {
    return { ok: false, error: '严重级别非法' }
  }
  return {
    ok: true,
    value: { tenant_id: tenantId, title, body, severity: form.severity },
  }
}

/** 严重级别 → 徽章语气。critical=danger;warning=warn;info=中性。 */
export function severityTone(severity: string): BadgeTone {
  switch (severity) {
    case 'critical':
      return 'danger'
    case 'warning':
      return 'warn'
    default:
      return 'muted'
  }
}

/** 严重级别 → 中文标签。 */
export function severityLabel(severity: string): string {
  switch (severity) {
    case 'info':
      return '普通'
    case 'warning':
      return '警告'
    case 'critical':
      return '严重'
    default:
      return severity || '—'
  }
}

/**
 * worker 失败率展示:failed/tick 的百分比。
 * 判别核心:tick=0 时回退破折号(避免 0/0=NaN%);否则四舍五入到整数百分比。
 * 变异(去掉 tick<=0 守卫)→ NaN% 会渲染出来,对应断言 RED。
 */
export function failureRate(failedTicks: number, tickCount: number): string {
  if (!Number.isFinite(tickCount) || tickCount <= 0) return '—'
  return `${Math.round((failedTicks / tickCount) * 100)}%`
}
