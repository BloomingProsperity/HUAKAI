import type { ProbeResult, UpdateProxyInput } from './types'

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

// 后端支持的代理协议(transport/mimicry proxyDialerFromURL:http/https CONNECT + socks5)。
export const PROTOCOLS = ['http', 'https', 'socks5', 'socks5h'] as const
// 后端生命周期状态(proxyadmin validStatus:active/disabled/dead)。
export const STATUSES = ['active', 'disabled', 'dead'] as const

export interface CreateProxyForm {
  name: string
  protocol: string
  host: string
  port: string // 表单里是字符串,提交前转 int
  auth_username: string
  auth_secret: string
  status: string
}

/**
 * validateCreateForm 校验新建表单,返回首个错误文案或 null(通过)。
 * 必填:name/host 非空、protocol 合法、port 是 1-65535 整数;auth 可选;status 合法或空。
 * 变异:任一校验缺失会让对应非法输入漏过 → 测试转红。
 */
export function validateCreateForm(f: CreateProxyForm): string | null {
  if (f.name.trim() === '') return '名称必填'
  if (!PROTOCOLS.includes(f.protocol as (typeof PROTOCOLS)[number])) return '协议非法'
  if (f.host.trim() === '') return '主机必填'
  const port = Number.parseInt(f.port, 10)
  if (!Number.isInteger(port) || port < 1 || port > 65535) return '端口须为 1-65535 的整数'
  if (f.status !== '' && !STATUSES.includes(f.status as (typeof STATUSES)[number])) return '状态非法'
  return null
}

/*
 * 编辑代理表单。无 status 字段(后端 PATCH 用 DisallowUnknownFields,不接受 status;状态走独立的
 * PUT /{id}/status)。⚠️ auth_secret 留空 = 清除认证密钥:后端 PATCH 缺省即写 SQL NULL
 * (无 COALESCE),密钥又不回显无法 round-trip,故无法「只改其它字段而保留密钥」。
 * 留空保存的二次确认在 EditProxyForm 里;要保留认证就重填密钥。
 */
export interface EditProxyForm {
  name: string
  protocol: string
  host: string
  port: string
  auth_username: string
  // 留空 = 清除密钥(见上;非「保留」)。
  auth_secret: string
}

/**
 * validateEditForm 校验编辑表单,返回首个错误文案或 null(通过)。
 * 与新建同样必填 name/host/protocol/port,但不含 status。
 * 变异:删任一校验则对应非法输入漏过 → 测试转红。
 */
export function validateEditForm(f: EditProxyForm): string | null {
  if (f.name.trim() === '') return '名称必填'
  if (!PROTOCOLS.includes(f.protocol as (typeof PROTOCOLS)[number])) return '协议非法'
  if (f.host.trim() === '') return '主机必填'
  const port = Number.parseInt(f.port, 10)
  if (!Number.isInteger(port) || port < 1 || port > 65535) return '端口须为 1-65535 的整数'
  return null
}

/**
 * buildUpdateInput 把编辑表单转成 PATCH 请求体。
 * 关键约束(对齐后端 updateProxyRequest + DisallowUnknownFields):
 * - 必带 name/protocol/host/port;
 * - auth_username 总是下发(可清空成空串);
 * - auth_secret 仅在非空时下发 —— 留空则不带该字段;**注意后端缺省即写 NULL,语义=清除**
 *   (非保留),清除前的二次确认在 EditProxyForm。
 * 变异:若把 auth_secret 无条件下发(留空也带),则"留空时不下发该字段"用例转红。
 */
export function buildUpdateInput(f: EditProxyForm): UpdateProxyInput {
  const out: UpdateProxyInput = {
    name: f.name.trim(),
    protocol: f.protocol,
    host: f.host.trim(),
    port: Number.parseInt(f.port, 10),
    auth_username: f.auth_username.trim() || undefined,
  }
  if (f.auth_secret !== '') {
    out.auth_secret = f.auth_secret
  }
  return out
}
