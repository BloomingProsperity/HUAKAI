import type { ProbeResult } from './types'

/*
 * 出口代理池纯展示逻辑(与 React 解耦,便于 vitest 变异测试)。
 */

// 单租户部署默认租户 1;顶栏可改(平台 admin 看他租户需显式指定)。
export const DEFAULT_TENANT_ID = 1

// error_class → 中文短语。覆盖后端全部枚举;未知值兜底为通用文案。
const ERROR_LABELS: Record<string, string> = {
  unsafe_proxy_host: '代理地址不安全(内网/metadata)',
  target_denied: '探测目标被策略拒绝',
  bad_proxy_url: '代理 URL 无效',
  dial_timeout: '经代理连接超时',
  tunnel_refused: '隧道建立被拒',
  tls_fail: 'TLS 握手失败',
}

export interface ProbeSummary {
  label: string
  tone: 'ok' | 'fail'
}

/**
 * probeSummary 把 probe 结果转成展示用 {label, tone}。
 * ok=true → "连通 (Nms)" + ok;否则 → 中文错误短语 + fail。
 */
export function probeSummary(res: ProbeResult): ProbeSummary {
  if (res.ok) {
    return { label: `连通 (${res.latency_ms}ms)`, tone: 'ok' }
  }
  const cls = res.error_class ?? ''
  const reason = ERROR_LABELS[cls] ?? `探测失败${cls ? `(${cls})` : ''}`
  return { label: reason, tone: 'fail' }
}

/** 代理生命周期状态 → 展示色调。active=ok,dead=fail,其余(disabled)=muted。 */
export function statusTone(status: string): 'ok' | 'fail' | 'muted' {
  switch (status) {
    case 'active':
      return 'ok'
    case 'dead':
      return 'fail'
    default:
      return 'muted'
  }
}

/** 解析顶栏租户输入:正整数则取之,否则回退默认租户。 */
export function parseTenantInput(raw: string): number {
  const v = Number.parseInt(raw, 10)
  return Number.isInteger(v) && v > 0 ? v : DEFAULT_TENANT_ID
}
