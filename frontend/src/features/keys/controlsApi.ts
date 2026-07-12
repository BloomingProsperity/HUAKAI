import { ApiError, apiGet, apiSend } from '../../lib/api'
import type {
  KeyGroupView,
  KeyIPListView,
  KeyModelAllowlistView,
  KeyQuotaView,
  SetGroupBody,
  SetIPAllowlistBody,
  SetIPBlacklistBody,
  SetModelAllowlistBody,
  SetQuotaBody,
} from './controlsTypes'

/*
 * API Key 细粒度控制数据访问层。端点全部挂在 /v1/api-keys/{id}/...,session 鉴权
 *(经 lib/api 的 tokenForPath:/v1/api-keys 走 session token,绝不手动设 admin Bearer)。
 * 路由真实性见 backend/internal/userkeycontrolshttp/mount.go:30-39。
 *
 * 关键约定:配额 GET 与分组 GET 在「尚未配置」时返回 404
 *   - 配额:api_key_quota_not_found(errors.go:25)
 *   - 分组:api_key_group_not_found / api_key_not_found
 * 这不是错误,而是「未设置」状态,故 getQuotaOrNull / getGroupOrNull 把这两类 404 吞掉返回 null,
 * 让 UI 呈现空表单而非报错。其它错误照常抛 ApiError。
 */

function base(id: number): string {
  return `/v1/api-keys/${id}`
}

/** 「未配置」语义的 404 错误码集合——仅这些码可被吞成 null。 */
const NOT_CONFIGURED_CODES = new Set([
  'api_key_quota_not_found',
  'api_key_group_not_found',
])

function isNotConfigured(e: unknown): boolean {
  return e instanceof ApiError && e.status === 404 && NOT_CONFIGURED_CODES.has(e.code)
}

// ── 配额 ──────────────────────────────────────────────────────────────────────

/** GET 配额;未配置(404)返回 null。 */
export async function getQuotaOrNull(id: number, signal?: AbortSignal): Promise<KeyQuotaView | null> {
  try {
    return await apiGet<KeyQuotaView>(`${base(id)}/quota`, { signal })
  } catch (e) {
    if (isNotConfigured(e)) return null
    throw e
  }
}

/** PUT 配额(money 敏感:设定单 key 用量上限)。 */
export async function putQuota(id: number, body: SetQuotaBody): Promise<KeyQuotaView> {
  return apiSend<KeyQuotaView>('PUT', `${base(id)}/quota`, body)
}

// ── 分组 ──────────────────────────────────────────────────────────────────────

/** GET 分组;未配置/未绑定(404)返回 null。 */
export async function getGroupOrNull(id: number, signal?: AbortSignal): Promise<KeyGroupView | null> {
  try {
    return await apiGet<KeyGroupView>(`${base(id)}/group`, { signal })
  } catch (e) {
    if (isNotConfigured(e)) return null
    throw e
  }
}

/** PUT 分组(group_id 为正整数或 null=清除绑定)。 */
export async function putGroup(id: number, body: SetGroupBody): Promise<KeyGroupView> {
  return apiSend<KeyGroupView>('PUT', `${base(id)}/group`, body)
}

// ── IP 白名单 ──────────────────────────────────────────────────────────────────

export async function getIPAllowlist(id: number, signal?: AbortSignal): Promise<KeyIPListView> {
  return apiGet<KeyIPListView>(`${base(id)}/ip-allowlist`, { signal })
}
export async function putIPAllowlist(id: number, body: SetIPAllowlistBody): Promise<KeyIPListView> {
  return apiSend<KeyIPListView>('PUT', `${base(id)}/ip-allowlist`, body)
}

// ── IP 黑名单 ──────────────────────────────────────────────────────────────────

export async function getIPBlacklist(id: number, signal?: AbortSignal): Promise<KeyIPListView> {
  return apiGet<KeyIPListView>(`${base(id)}/ip-blacklist`, { signal })
}
export async function putIPBlacklist(id: number, body: SetIPBlacklistBody): Promise<KeyIPListView> {
  return apiSend<KeyIPListView>('PUT', `${base(id)}/ip-blacklist`, body)
}

// ── 模型白名单 ─────────────────────────────────────────────────────────────────

export async function getModelAllowlist(id: number, signal?: AbortSignal): Promise<KeyModelAllowlistView> {
  return apiGet<KeyModelAllowlistView>(`${base(id)}/model-allowlist`, { signal })
}
export async function putModelAllowlist(id: number, body: SetModelAllowlistBody): Promise<KeyModelAllowlistView> {
  return apiSend<KeyModelAllowlistView>('PUT', `${base(id)}/model-allowlist`, body)
}
