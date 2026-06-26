import { apiGet, apiSend } from '../../lib/api'
import type { ProbeResult, ProxyListResponse } from './types'

/*
 * 出口代理池数据访问层。端点 /admin/v1/proxies(admin 鉴权,apiGet/apiSend 经 authHeaders
 * 按 /admin/ 前缀自动注入 admin token,避 #143 裸 fetch 漏鉴权)。
 * 真码:backend/internal/proxyadminhttp(列表/质检),routes_proxy_probe.go(probe 接线)。
 * 平台 admin 须传 tenant_id;租户运营者后端回退自身 scope。
 */

const PATH = '/admin/v1/proxies'

/** 列出某租户的出口代理(只读,secret-free)。 */
export async function listProxies(tenantId: number, signal?: AbortSignal): Promise<ProxyListResponse> {
  return apiGet<ProxyListResponse>(PATH, { query: { tenant_id: tenantId }, signal })
}

/**
 * testProxy 主动质检:经该代理建隧道到服务端固定 canary,测连通 + 延迟。
 * 探测目标由后端常量决定,前端不传(也无法影响)目标——杜绝双跳 SSRF。
 */
export async function testProxy(tenantId: number, id: number): Promise<ProbeResult> {
  return apiSend<ProbeResult>('POST', `${PATH}/${id}/test`, undefined, { query: { tenant_id: tenantId } })
}
