// 账户安全数据层:2FA (TOTP) + Passkey + 第三方 OAuth 绑定。
// 全部走用户面 session 鉴权 (userClient 注入 session_token + 401 刷新)。
// 端点形状均按 HUAKAI 后端 handler 真码确认:
//   GET  /v1/auth/2fa/status                     controlhttp.newStatusHandler
//   POST /v1/auth/2fa/setup                       controlhttp.newSetupHandler          (201)
//   POST /v1/auth/2fa/enable                       controlhttp.newEnableHandler
//   POST /v1/auth/2fa/disable                      controlhttp.newDisableHandler
//   POST /v1/auth/2fa/backup-codes/regenerate      controlhttp.newRegenerateBackupCodesHandler
//   GET  /v1/me/passkeys                            passkeyhttp.newListHandler
//   DELETE /v1/me/passkeys/{id}                     passkeyhttp.newDeleteHandler
//   GET  /v1/users/me/oauth-bindings                controlhttp.newOAuthBindingsListHandler
//   DELETE /v1/users/me/oauth-bindings/{provider}   controlhttp.newOAuthBindingsUnlinkHandler
// 借鉴 (仅功能/字段/布局形态,clean-room 非抄码):
//   - sub2api src/api/totp.ts: 安全卡片含 2FA 状态 (enabled + feature 可用性)、setup 出 secret+QR+备份码、
//     enable 校验码、disable 需验证 (sub2api 用 email/password 验证法;HUAKAI 后端改用 TOTP/备份码本身校验)。
//   - sub2api ProfileIdentityBindingsSection.vue: 第三方身份绑定列表 + 解绑按钮的布局形态。
//   - new-api 个人设置安全区: 2FA 启停 + 备份码展示的功能形态。
import { userGet, userPost, userDelete } from './userClient';

// ==================== 2FA (TOTP) ====================

// GET /v1/auth/2fa/status 响应 (newStatusHandler 组装的 map):
//   available: 平台是否开启 2FA 功能 (KeyTwoFactorEnabled)。
//   enabled:   当前用户是否已启用。
//   backup_codes_remaining: 剩余未用备份码数。
//   locked_until / last_used_at: 可选时间戳。
export interface TwoFAStatus {
  available: boolean;
  enabled: boolean;
  backup_codes_remaining: number;
  locked_until?: string | null;
  last_used_at?: string | null;
}

// POST /v1/auth/2fa/setup 响应 (twofa.SetupResult, 201):
//   secret:   Base32 密钥 (供验证器 App 手动录入)。
//   qr_data:  otpauth:// URI (供扫码 / 手动构造)。
//   backup_codes: 一次性备份码 (仅此次返回, 需用户当场保存)。
export interface TwoFASetupResult {
  secret: string;
  qr_data: string;
  backup_codes: string[];
}

// POST /v1/auth/2fa/backup-codes/regenerate 响应 (twofa.BackupCodesResult)。
export interface BackupCodesResult {
  backup_codes: string[];
}

export function fetch2FAStatus(): Promise<TwoFAStatus> {
  return userGet<TwoFAStatus>('/v1/auth/2fa/status');
}

// setup 请求体可选 account_name (出现在 otpauth label 中);后端 newSetupHandler 用 DecodeOptional 接受空体。
export function setup2FA(accountName?: string): Promise<TwoFASetupResult> {
  return userPost<TwoFASetupResult>('/v1/auth/2fa/setup', accountName ? { account_name: accountName } : {});
}

// enable 提交验证器当前 6 位码确认绑定;成功返回 twofa.Status。
export function enable2FA(code: string): Promise<TwoFAStatus> {
  return userPost<TwoFAStatus>('/v1/auth/2fa/enable', { code });
}

// disable 需提交一个有效 TOTP 码或备份码 (后端先 VerifyLogin 再 Disable)。
export function disable2FA(code: string): Promise<{ enabled: boolean }> {
  return userPost<{ enabled: boolean }>('/v1/auth/2fa/disable', { code });
}

// 重新生成备份码 (需有效码),旧码全部作废。
export function regenerateBackupCodes(code: string): Promise<BackupCodesResult> {
  return userPost<BackupCodesResult>('/v1/auth/2fa/backup-codes/regenerate', { code });
}

// ==================== Passkey ====================

// GET /v1/me/passkeys 响应 (newListHandler: { passkeys: CredentialSummary[] })。
// CredentialSummary 字段来自 passkey/types.go;敏感字节 (公钥/credential id) 不下发。
export interface PasskeySummary {
  id: number;
  name?: string;
  transports?: string[];
  attestation_type?: string;
  clone_warning: boolean;
  sign_count: number;
  created_at: string;
  last_used_at?: string | null;
}

export interface PasskeyListResponse {
  passkeys: PasskeySummary[];
}

export function fetchPasskeys(): Promise<PasskeyListResponse> {
  return userGet<PasskeyListResponse>('/v1/me/passkeys');
}

// DELETE /v1/me/passkeys/{id} (newDeleteHandler: { deleted: true })。
// 注意:后端 delete 走 step-up 校验 (StepUp);最小 dev 装配下 StepUp 依赖可能未配置 → 503。
// 本切片不带 step_up proof,删除若被 step-up 拦截会返回 403/503,UI 给出友好提示。
export function deletePasskey(id: number): Promise<{ deleted: boolean }> {
  return userDelete<{ deleted: boolean }>(`/v1/me/passkeys/${id}`, {});
}

// ==================== 第三方 OAuth 绑定 ====================

// GET /v1/users/me/oauth-bindings 响应 (newOAuthBindingsListHandler: { bindings: [...] })。
// subject 已在 service 层脱敏;linked_at 为 RFC1123 (http.TimeFormat) 字符串。
export interface OAuthBinding {
  provider: string;
  subject: string;
  linked_at: string;
}

export interface OAuthBindingsResponse {
  bindings: OAuthBinding[];
}

export function fetchOAuthBindings(): Promise<OAuthBindingsResponse> {
  return userGet<OAuthBindingsResponse>('/v1/users/me/oauth-bindings');
}

// DELETE /v1/users/me/oauth-bindings/{provider} (newOAuthBindingsUnlinkHandler: { unlinked: bool })。
// service 层保护末位登录方式:无密码且唯一绑定 → 409;not-linked → unlinked:false (200 no-op)。
export function unlinkOAuthBinding(provider: string): Promise<{ unlinked: boolean }> {
  return userDelete<{ unlinked: boolean }>(`/v1/users/me/oauth-bindings/${encodeURIComponent(provider)}`, {});
}

// ==================== 展示辅助 ====================

export function fmtDateTime(value?: string | null): string {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? value : d.toLocaleString('zh-CN');
}

// provider 标识 → 展示名 (已知映射,未知回退首字母大写)。
const PROVIDER_LABELS: Record<string, string> = {
  github: 'GitHub',
  google: 'Google',
  linuxdo: 'LINUX DO',
  oidc: 'OIDC',
  wechat: '微信',
  dingtalk: '钉钉',
};

export function providerLabel(provider: string): string {
  const key = provider.trim().toLowerCase();
  return PROVIDER_LABELS[key] ?? (provider ? provider.charAt(0).toUpperCase() + provider.slice(1) : provider);
}

// transports 数组 → 中文友好串 (passkey 卡片展示用)。
const TRANSPORT_LABELS: Record<string, string> = {
  usb: 'USB',
  nfc: 'NFC',
  ble: '蓝牙',
  internal: '本机',
  hybrid: '混合',
};

export function transportLabel(t: string): string {
  return TRANSPORT_LABELS[t.trim().toLowerCase()] ?? t;
}
