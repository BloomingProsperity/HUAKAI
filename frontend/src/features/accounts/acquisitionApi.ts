import { apiGet, apiSend } from '../../lib/api'
import { helperPathForMethod } from './acquisition'
import type {
  AcquisitionFlow,
  FinalizeResponse,
  ImportHelperResponse,
  StartFlowResponse,
  WizardMethod,
} from './acquisition'

/*
 * 凭证获取/导入向导的数据访问层。
 *
 * 端点真实性(backend file:line):
 *   账号级获取流 — admin_credential_acquisition_handler.go:82-88(MountAdminCredentialAcquisitionRoutes),
 *     挂在 provider-accounts 组下,前缀 /admin/v1/provider-accounts/{id}(routes.go:960 + 975)。
 *   导入/OAuth helper — admin_credential_acquisition_handler.go:90-97(MountAdminCredentialAcquisitionHelperRoutes),
 *     挂在 /admin/v1/credentials 组(routes.go:993 + 999)。
 *
 * 两组前缀都是 /admin/v1,tokenForPath 自动注入 admin Bearer。
 *
 * SECRET-MASK:所有写端点的请求体由 acquisition.ts 的 build* 纯函数构造,secret 材料
 * (content / client_secret / code / state)只在「请求体 → POST」方向流动;返回类型一律
 * 是 flow 状态 / metadata,无任何 secret 字段。
 */

/** provider-accounts 组的 admin 前缀(与 AccountFingerprintBind/api.ts 既有一致)。 */
const ACCOUNT_BASE = '/admin/v1/provider-accounts'
/** 导入 helper 组前缀。 */
const CREDENTIALS_BASE = '/admin/v1/credentials'

/**
 * 发起账号级获取流。POST /{id}/credential-acquisitions。
 * body 由 buildOAuthStartBody 构造(含 tenant_id)。返回 flow + 可选 authorize_url/state。
 */
export async function startAcquisition(
  accountId: number,
  body: Record<string, unknown>,
): Promise<StartFlowResponse> {
  return apiSend<StartFlowResponse>('POST', `${ACCOUNT_BASE}/${accountId}/credential-acquisitions`, body)
}

/**
 * 轮询流状态。GET /{id}/credential-acquisitions/{flowID}。响应 {flow}。
 * tenant_id 经路径账号归属校验,不需带 query;但平台管理员单租户默认 1 已隐含在 body 发起里。
 */
export async function getAcquisitionFlow(
  accountId: number,
  flowId: string,
  signal?: AbortSignal,
): Promise<{ flow: AcquisitionFlow }> {
  return apiGet<{ flow: AcquisitionFlow }>(
    `${ACCOUNT_BASE}/${accountId}/credential-acquisitions/${flowId}`,
    { signal },
  )
}

/**
 * 投递 OAuth 回调。POST /{id}/credential-acquisitions/{flowID}/callback。
 * body {state, code}(buildCallbackBody)。后端验码 + 交换 + finalize,返回 FinalizeResult。
 */
export async function deliverCallback(
  accountId: number,
  flowId: string,
  body: Record<string, unknown>,
): Promise<FinalizeResponse> {
  return apiSend<FinalizeResponse>(
    'POST',
    `${ACCOUNT_BASE}/${accountId}/credential-acquisitions/${flowId}/callback`,
    body,
  )
}

/**
 * 取消流(破坏性,UI 须二次确认)。POST /{id}/credential-acquisitions/{flowID}/cancel。响应 {flow}。
 */
export async function cancelAcquisition(
  accountId: number,
  flowId: string,
): Promise<{ flow: AcquisitionFlow }> {
  return apiSend<{ flow: AcquisitionFlow }>(
    'POST',
    `${ACCOUNT_BASE}/${accountId}/credential-acquisitions/${flowId}/cancel`,
  )
}

// 注:手动 finalize 端点(POST /{id}/credential-acquisitions/{flowID}/finalize)未接前端——
// OAuth 回调经 deliverCallback 自动落库、导入经 finalize=true 一步落库,无手工 finalize 入口;
// 刻意不导出 finalizeAcquisition,收窄直接下发原始 credentials secret 的 API 面(若将来加手工
// finalize UI,再补回并确保其 credentials 输入同样只写、提交后清空)。

/**
 * 导入 helper(粘贴 / CLI / CSV / JSON)。POST /admin/v1/credentials/{paste,cli-import,csv-import,json-import}。
 * body credentialAcqHelperRequest(buildImportBody),content 是 secret 材料只写。
 * method 不能是 oauth(无 helper 路径,helperPathForMethod 返回 null 时抛错防误用)。
 */
export async function importCredentials(
  method: WizardMethod,
  body: Record<string, unknown>,
): Promise<ImportHelperResponse> {
  const sub = helperPathForMethod(method)
  if (!sub) {
    throw new Error('importCredentials 不支持 oauth 方式(请走 startAcquisition)')
  }
  return apiSend<ImportHelperResponse>('POST', `${CREDENTIALS_BASE}/${sub}`, body)
}
