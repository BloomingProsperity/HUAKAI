/*
 * Hermes 运营台「改动型」数据访问层(Owner 授权后接入)。
 *
 * 全部端点挂在 /v1/hermes(admin-only 中间件),与只读面板同款鉴权:复用 hermesClient 导出的
 * buildAuthQuery 组装 as_user_id/tenant_id query,并显式传 admin token 作 Bearer——绝不裸调,
 * 否则会回落 session token → 后端恒 401(#143 同款坑)。
 *
 * 安全姿态:
 *   - secret 只写不回显:本层不接收也不存任何 secret;create profile 只传 FK 引用
 *     (api_key_id/pool_group_id),后端响应本就不返机密。
 *   - mutating 工具强制两步:executeToolDryRun(confirm=false)拿 correlation_id + preview,
 *     UI 让 operator 看完 preview 后才调 executeToolConfirm(confirm=true + correlation_id)。
 *     correlation_id 5 分钟 TTL、一次性消费(后端 hermesconfirm)。
 *
 * 端点真实性:backend/internal/hermeshttp/router.go:163-181 +
 *   settings_handler.go / profiles_handler.go / tools_handler.go / tools_mutate_handler.go。
 */

import { apiGet, apiSend } from '../../lib/api'
import { buildAuthQuery, type HermesAuthQuery } from './hermesClient'
import type {
  CreateProfileRequest,
  EnableSettingsRequest,
  HermesProfile,
  HermesProfileListResponse,
  HermesSettings,
  HermesToolDescriptor,
  HermesToolsResponse,
  MutationPreview,
  MutationResult,
  ReadOnlyToolResult,
} from './hermesAdminTypes'

// ── 配置启停(per-user Hermes 配置,非全局 KNOB)──────────────────────────────────

/** 读当前操作身份的 Hermes 配置。GET /v1/hermes/settings(settings_handler.go:15)。 */
export async function getSettings(
  adminToken: string,
  auth: HermesAuthQuery,
  signal?: AbortSignal,
): Promise<HermesSettings> {
  return apiGet<HermesSettings>('/v1/hermes/settings', {
    bearer: adminToken,
    query: buildAuthQuery(auth),
    signal,
  })
}

/**
 * 启用 Hermes 配置。POST /v1/hermes/settings/enable(settings_handler.go:28)。
 * body 可带 api_source(默认 managed_huakai_api)与 profile_id;dedicated_group 必须配 profile。
 */
export async function enableSettings(
  adminToken: string,
  auth: HermesAuthQuery,
  body: EnableSettingsRequest,
): Promise<HermesSettings> {
  return apiSend<HermesSettings>('POST', '/v1/hermes/settings/enable', body, {
    bearer: adminToken,
    query: buildAuthQuery(auth),
  })
}

/** 停用 Hermes 配置。POST /v1/hermes/settings/disable(settings_handler.go:57,无 body)。 */
export async function disableSettings(
  adminToken: string,
  auth: HermesAuthQuery,
): Promise<HermesSettings> {
  return apiSend<HermesSettings>('POST', '/v1/hermes/settings/disable', undefined, {
    bearer: adminToken,
    query: buildAuthQuery(auth),
  })
}

// ── api-profile CRUD(响应只 FK 引用,无 secret)─────────────────────────────────

/** 列当前 owner 的 api-profile。GET /v1/hermes/api-profiles(profiles_handler.go:53)。 */
export async function listProfiles(
  adminToken: string,
  auth: HermesAuthQuery,
  signal?: AbortSignal,
): Promise<HermesProfile[]> {
  const resp = await apiGet<HermesProfileListResponse>('/v1/hermes/api-profiles', {
    bearer: adminToken,
    query: buildAuthQuery(auth),
    signal,
  })
  return resp.profiles ?? []
}

/**
 * 新建 api-profile。POST /v1/hermes/api-profiles(profiles_handler.go:25,返回 201)。
 * 只传 name/kind + FK 引用,绝不传任何 secret;后端响应也不返机密。
 */
export async function createProfile(
  adminToken: string,
  auth: HermesAuthQuery,
  body: CreateProfileRequest,
): Promise<HermesProfile> {
  return apiSend<HermesProfile>('POST', '/v1/hermes/api-profiles', body, {
    bearer: adminToken,
    query: buildAuthQuery(auth),
  })
}

/**
 * 删除 api-profile(破坏性,调用方须二次确认)。
 * DELETE /v1/hermes/api-profiles/{id}(profiles_handler.go:87)。
 * 若该 profile 正被某条配置引用,后端返回 409 profile_in_use(router.go:332),UI 须如实提示。
 */
export async function deleteProfile(
  adminToken: string,
  auth: HermesAuthQuery,
  id: number,
): Promise<void> {
  await apiSend<void>('DELETE', `/v1/hermes/api-profiles/${id}`, undefined, {
    bearer: adminToken,
    query: buildAuthQuery(auth),
  })
}

// ── 工具发现 + 执行 ───────────────────────────────────────────────────────────────

/** 列已注册工具(含 mutating 标记)。GET /v1/hermes/tools(tools_handler.go:29)。 */
export async function listTools(
  adminToken: string,
  auth: HermesAuthQuery,
  signal?: AbortSignal,
): Promise<HermesToolDescriptor[]> {
  const resp = await apiGet<HermesToolsResponse>('/v1/hermes/tools', {
    bearer: adminToken,
    query: buildAuthQuery(auth),
    signal,
  })
  return resp.tools ?? []
}

/**
 * 直接执行只读工具。POST /v1/hermes/tool-execute(confirm 省略)。
 * 仅用于 read_only && !mutating 的工具;mutating 工具绝不走这里(后端也会在到达只读路径前
 * 把它分流进 dry-run+confirm,tools_handler.go:104)。
 */
export async function executeReadOnlyTool(
  adminToken: string,
  auth: HermesAuthQuery,
  toolName: string,
  args: Record<string, unknown>,
): Promise<ReadOnlyToolResult> {
  return apiSend<ReadOnlyToolResult>(
    'POST',
    '/v1/hermes/tool-execute',
    { tool_name: toolName, args },
    { bearer: adminToken, query: buildAuthQuery(auth) },
  )
}

/**
 * mutating 工具 dry-run(confirm=false):只取 preview + correlation_id,绝不改任何状态。
 * 后端 previewMutation(tools_mutate_handler.go:105)签发一次性 correlation_id(5 分钟 TTL)。
 */
export async function executeToolDryRun(
  adminToken: string,
  auth: HermesAuthQuery,
  toolName: string,
  args: Record<string, unknown>,
): Promise<MutationPreview> {
  return apiSend<MutationPreview>(
    'POST',
    '/v1/hermes/tool-execute',
    { tool_name: toolName, args, confirm: false },
    { bearer: adminToken, query: buildAuthQuery(auth) },
  )
}

/**
 * mutating 工具确认执行(confirm=true + 来自 dry-run 的 correlation_id):恰好执行一次。
 * 后端 confirmMutation(tools_mutate_handler.go:139)一次性消费 correlation_id;陈旧/不匹配即 400 拒。
 * args 必须与 dry-run 时一致(否则 target 不匹配,后端拒绝、绝不改错误的行)。
 */
export async function executeToolConfirm(
  adminToken: string,
  auth: HermesAuthQuery,
  toolName: string,
  args: Record<string, unknown>,
  correlationId: string,
): Promise<MutationResult> {
  return apiSend<MutationResult>(
    'POST',
    '/v1/hermes/tool-execute',
    { tool_name: toolName, args, confirm: true, correlation_id: correlationId },
    { bearer: adminToken, query: buildAuthQuery(auth) },
  )
}
