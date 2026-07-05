import { apiGet, ApiError, ensureFreshSessionForPath } from '../../lib/api'
import { getTokens } from '../../auth/store'
import { tokenForPath } from '../../auth/tokenForPath'
import { appendQuery, buildAuditQuery, buildExportQuery, type AuditExportFilter } from './audit'
import type { AuditFilters, AuditListResponse } from './types'

/*
 * 审计查看器数据访问层。端点 GET /admin/v1/audit-events(admin token 鉴权,只读)。
 * 游标分页:首页不带 cursor,后续传上一页 next_cursor。
 */
export async function listAuditEvents(
  filters: AuditFilters,
  cursor?: string,
  limit = 100,
  signal?: AbortSignal,
): Promise<AuditListResponse> {
  return apiGet<AuditListResponse>('/admin/v1/audit-events', {
    query: { ...buildAuditQuery(filters, cursor), limit },
    signal,
  })
}

/*
 * 审计导出 / 单条签名证明。两端点都在 /v1/audit 前缀下、走 session 鉴权(routes.go:153-159
 * 的 SessionMiddleware 组),返回的是带 Content-Disposition: attachment 的 JSON 文件,而非
 * 业务 JSON,故不能走 apiGet(它按 JSON.parse 解析正文)。这里用同源 fetch 取 blob,并【手动
 * 注入正确的 Authorization】——/v1/audit 不是 /v1/auth、也不是 admin 前缀,tokenForPath 回落
 * 到 session token(若漏带,后端 authorizedTenantScope 恒 401)。租户范围由后端从会话上下文
 * 派生(handler.go:105 authorizedTenantScope),前端不传任何租户标识,杜绝跨租户 IDOR。
 */

/** 取一次同源 blob 并另存为文件,失败时把后端 JSON 错误体归一化成 ApiError。 */
async function downloadSessionBlob(path: string, filename: string, signal?: AbortSignal): Promise<void> {
  await ensureFreshSessionForPath(path)
  // /v1/audit/* → session token(非 /v1/auth、非 admin)。
  const token = tokenForPath(path, getTokens())
  const resp = await fetch(path, {
    method: 'GET',
    credentials: 'include',
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    signal,
  })
  if (!resp.ok) {
    // 错误体为 JSON({error:{code,message}});读出归一化成 ApiError 交上层提示。
    const text = await resp.text().catch(() => '')
    let code = `http_${resp.status}`
    let message = resp.statusText || '下载失败'
    try {
      const body = text ? JSON.parse(text) : undefined
      if (body?.error) {
        code = body.error.code ?? code
        message = body.error.message ?? message
      }
    } catch {
      /* 非 JSON 错误体,沿用状态文案 */
    }
    throw new ApiError(resp.status, code, message)
  }
  const blob = await resp.blob()
  const objectUrl = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = objectUrl
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(objectUrl)
}

/**
 * 导出整条审计链为 JSON 文件并触发下载。GET /v1/audit/export(handler.go:161 NewExportHandler)。
 * 过滤【互斥】:request_ids 或 from/to(buildExportQuery 在前端先行收敛,非法组合抛错)。
 */
export async function exportAuditChain(filter: AuditExportFilter, signal?: AbortSignal): Promise<void> {
  const query = buildExportQuery(filter) // 非法组合在此抛错,由上层 catch 提示
  const path = appendQuery('/v1/audit/export', query)
  await downloadSessionBlob(path, 'audit-export.json', signal)
}

/**
 * 下载单条审计事件的签名证明 JSON。GET /v1/audit/proof/{request_id}.json
 * (handler.go:123 NewProofDownloadHandler)。无查询参数,request_id 走路径段。
 */
export async function downloadAuditProof(requestId: string, signal?: AbortSignal): Promise<void> {
  const id = requestId.trim()
  if (!id) throw new ApiError(400, 'missing_request_id', '该事件无 request_id,无法出具证明')
  const path = `/v1/audit/proof/${encodeURIComponent(id)}.json`
  await downloadSessionBlob(path, `audit-proof-${id}.json`, signal)
}
