/*
 * 出口代理池类型。对应后端 proxyadminhttp 的只读 DTO(proxyResponse)与 probe 结果(testResponse)。
 * auth_secret 是 write-only,后端从不返回,故此处无该字段。
 */

export interface Proxy {
  id: number
  name: string
  protocol: string
  host: string
  port: number
  auth_username: string | null
  status: string
  last_check_at: string | null
  created_at: string
  updated_at: string
}

export interface ProxyListResponse {
  items: Proxy[]
}

/** 新建代理的请求体(auth_secret write-only,仅创建时发,后端加密存、从不回显)。 */
export interface CreateProxyInput {
  name: string
  protocol: string
  host: string
  port: number
  auth_username?: string
  auth_secret?: string
  status?: string
}

/** error_class 枚举(与后端 proxyhealth 一致;ok 时缺省)。 */
export type ProbeErrorClass =
  | 'unsafe_proxy_host'
  | 'target_denied'
  | 'bad_proxy_url'
  | 'dial_timeout'
  | 'tunnel_refused'
  | 'tls_fail'

export interface ProbeResult {
  object: string
  ok: boolean
  latency_ms: number
  error_class?: ProbeErrorClass | string
  probed_at: string
}
