import { apiGet, apiSend } from '../../lib/api'
import { getTokens } from '../../auth/store'
import type {
  BackupCodesResult,
  ChangePasswordResponse,
  DeleteSelfResponse,
  MeResponse,
  OAuthBindingsResponse,
  PasskeyListResponse,
  ProfileResponse,
  TwoFASetupResult,
  TwoFAStatus,
} from './types'

/*
 * 个人资料·安全 数据访问层。全部为已登录用户自助态端点。
 *
 * 鉴权要点(关键!):后端把 /v1/auth/me、/v1/auth/me/* 与 /v1/auth/2fa/* 挂在
 * SessionMiddleware 之下(只读 Authorization: Bearer 头,不读 cookie),但前端的
 * tokenForPath 对 /v1/auth/* 前缀刻意返回 null(把该前缀当公开认证端点)。
 * 因此这些端点必须显式把 session token 作为 bearer 传入,否则恒 401 session_token_required。
 * /v1/me/passkeys/* 与 /v1/users/me/oauth-bindings 不在该前缀下,会自动注入 session token。
 */

// sessionBearer 取当前 session token;无则返回 undefined(由后端按 401 处理,前端不打印)。
function sessionBearer(): string | undefined {
  return getTokens().sessionToken ?? undefined
}

// ---- 资料 ----

/** 读取本人资料(面板归属 + 显示名)。 */
export async function getMe(signal?: AbortSignal): Promise<MeResponse> {
  return apiGet<MeResponse>('/v1/auth/me', { bearer: sessionBearer(), signal })
}

/** 更新显示名。 */
export async function updateProfile(displayName: string): Promise<ProfileResponse> {
  return apiSend<ProfileResponse>('PUT', '/v1/auth/me/profile', { display_name: displayName }, { bearer: sessionBearer() })
}

/** 改密(校旧密 → 改新密 → 撤其它 session、保留当前)。 */
export async function changePassword(oldPassword: string, newPassword: string): Promise<ChangePasswordResponse> {
  return apiSend<ChangePasswordResponse>(
    'POST',
    '/v1/auth/me/password',
    { old_password: oldPassword, new_password: newPassword },
    { bearer: sessionBearer() },
  )
}

/** 注销本人账号(软删 + 撤全部 session)。末位 admin 由后端保护(409)。 */
export async function deleteSelf(): Promise<DeleteSelfResponse> {
  return apiSend<DeleteSelfResponse>('DELETE', '/v1/auth/me', undefined, { bearer: sessionBearer() })
}

// ---- 两步验证(2FA / TOTP) ----

/** 读取 2FA 状态(available=平台开关;enabled=本人是否已开)。 */
export async function getTwoFAStatus(signal?: AbortSignal): Promise<TwoFAStatus> {
  return apiGet<TwoFAStatus>('/v1/auth/2fa/status', { bearer: sessionBearer(), signal })
}

/** 发起 2FA 绑定:返回 secret + qr_data + 一次性备用码。secret/备用码仅一次性展示,绝不持久化。 */
export async function setupTwoFA(): Promise<TwoFASetupResult> {
  return apiSend<TwoFASetupResult>('POST', '/v1/auth/2fa/setup', {}, { bearer: sessionBearer() })
}

/** 用 TOTP 码确认开启 2FA。 */
export async function enableTwoFA(code: string): Promise<TwoFAStatus> {
  return apiSend<TwoFAStatus>('POST', '/v1/auth/2fa/enable', { code }, { bearer: sessionBearer() })
}

/** 用 TOTP / 备用码关闭 2FA。 */
export async function disableTwoFA(code: string): Promise<{ enabled: boolean }> {
  return apiSend<{ enabled: boolean }>('POST', '/v1/auth/2fa/disable', { code }, { bearer: sessionBearer() })
}

/** 用 TOTP 码重新生成备用码(旧的失效)。 */
export async function regenerateBackupCodes(code: string): Promise<BackupCodesResult> {
  return apiSend<BackupCodesResult>('POST', '/v1/auth/2fa/backup-codes/regenerate', { code }, { bearer: sessionBearer() })
}

// ---- 通行密钥(Passkey / WebAuthn) ----
// 注:路径不在 /v1/auth/* 前缀下,session token 由 tokenForPath 自动注入,无需显式 bearer。

/** 列出本人通行密钥。 */
export async function listPasskeys(signal?: AbortSignal): Promise<PasskeyListResponse> {
  return apiGet<PasskeyListResponse>('/v1/me/passkeys/', { signal })
}

/** 删除指定通行密钥。注:后端可能要求 step_up 证明(近期密码/2FA);此处不带 step_up,
 *  若后端返回 passkey_step_up_required(403),UI 提示用户走注册/重新验证流程。 */
export async function deletePasskey(id: number): Promise<{ deleted: boolean }> {
  return apiSend<{ deleted: boolean }>('DELETE', `/v1/me/passkeys/${id}`, {})
}

// ---- 社交登录绑定(OAuth) ----

/** 列出本人社交登录绑定(subject 已脱敏)。 */
export async function listOAuthBindings(signal?: AbortSignal): Promise<OAuthBindingsResponse> {
  return apiGet<OAuthBindingsResponse>('/v1/users/me/oauth-bindings', { signal })
}

/** 解绑指定 provider。末位登录方式由后端保护(409 last_login_method)。 */
export async function unlinkOAuthBinding(provider: string): Promise<{ unlinked: boolean }> {
  return apiSend<{ unlinked: boolean }>('DELETE', `/v1/users/me/oauth-bindings/${encodeURIComponent(provider)}`, {})
}
