import { base64urlToBuffer, bufferToBase64url } from '../../auth/loginEnhance'
import type { PasskeyStepUp } from './types'

export type PasskeyStepUpMethod = 'password' | 'two_factor_code'

/** 构造后端 step_up；敏感值只由调用方内存态持有。 */
export function buildPasskeyStepUp(
  method: PasskeyStepUpMethod,
  value: string,
): { ok: true; proof: PasskeyStepUp } | { ok: false; error: string } {
  const secret = value.trim()
  if (!secret) {
    return {
      ok: false,
      error: method === 'password' ? '请输入当前密码' : '请输入两步验证码或备用码',
    }
  }
  return method === 'password'
    ? { ok: true, proof: { password: value } }
    : { ok: true, proof: { two_factor_code: secret } }
}

function objectValue(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object') throw new Error('WebAuthn 选项格式无效')
  return value as Record<string, unknown>
}

/** 将后端 creation options 的二进制字段转换成浏览器需要的 ArrayBuffer。 */
export function toPublicKeyCreationOptions(raw: unknown): PublicKeyCredentialCreationOptions {
  const root = objectValue(raw)
  const pk = objectValue('publicKey' in root ? root.publicKey : root)
  const user = objectValue(pk.user)
  const options = {
    ...pk,
    challenge: base64urlToBuffer(String(pk.challenge ?? '')),
    user: {
      ...user,
      id: base64urlToBuffer(String(user.id ?? '')),
    },
  } as unknown as PublicKeyCredentialCreationOptions

  if (Array.isArray(pk.excludeCredentials)) {
    options.excludeCredentials = pk.excludeCredentials.map((item) => {
      const credential = objectValue(item)
      return {
        ...credential,
        id: base64urlToBuffer(String(credential.id ?? '')),
      } as PublicKeyCredentialDescriptor
    })
  }
  return options
}

/** 将浏览器 attestation 转成后端 WebAuthn finish 可解析的 JSON。 */
export function serializeAttestation(credential: PublicKeyCredential): Record<string, unknown> {
  const response = credential.response as AuthenticatorAttestationResponse
  const transports =
    typeof response.getTransports === 'function' ? response.getTransports() : undefined
  return {
    id: credential.id,
    rawId: bufferToBase64url(credential.rawId),
    type: credential.type,
    response: {
      clientDataJSON: bufferToBase64url(response.clientDataJSON),
      attestationObject: bufferToBase64url(response.attestationObject),
      ...(transports ? { transports } : {}),
    },
    clientExtensionResults: credential.getClientExtensionResults(),
    ...(credential.authenticatorAttachment
      ? { authenticatorAttachment: credential.authenticatorAttachment }
      : {}),
  }
}

/** 注册要求 create 能力；只有登录 get 能力仍不足以点亮按钮。 */
export function passkeyRegistrationSupported(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.PublicKeyCredential !== 'undefined' &&
    typeof navigator !== 'undefined' &&
    !!navigator.credentials &&
    typeof navigator.credentials.create === 'function'
  )
}
