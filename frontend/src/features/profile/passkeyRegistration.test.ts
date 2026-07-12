import { describe, expect, it } from 'vitest'
import { base64urlToBuffer, bufferToBase64url } from '../../auth/loginEnhance'
import {
  buildPasskeyStepUp,
  serializeAttestation,
  toPublicKeyCreationOptions,
} from './passkeyRegistration'

describe('Passkey base64url 与 WebAuthn 注册转换', () => {
  it('复用 base64url 原语可无损往返二进制数据', () => {
    const source = new Uint8Array([0xfb, 0xff, 0xbf, 0, 16])
    const encoded = bufferToBase64url(source.buffer)
    expect(encoded).not.toMatch(/[+/=]/)
    expect(Array.from(new Uint8Array(base64urlToBuffer(encoded)))).toEqual(Array.from(source))
  })

  it('creation options 同时解码 challenge、user.id 与排除凭据 id', () => {
    const options = toPublicKeyCreationOptions({
      publicKey: {
        challenge: 'AQID',
        rp: { id: 'example.test', name: 'HUAKAI' },
        user: { id: 'BAUG', name: 'user@example.test', displayName: '用户' },
        pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
        excludeCredentials: [{ type: 'public-key', id: '-_8', transports: ['usb'] }],
      },
    })

    expect(Array.from(new Uint8Array(options.challenge as ArrayBuffer))).toEqual([1, 2, 3])
    expect(Array.from(new Uint8Array(options.user.id as ArrayBuffer))).toEqual([4, 5, 6])
    expect(Array.from(new Uint8Array(options.excludeCredentials?.[0].id as ArrayBuffer))).toEqual([0xfb, 0xff])
    expect(options.rp.id).toBe('example.test')
  })

  it('attestation 的全部二进制字段编码回无 padding base64url', () => {
    const credential = {
      id: 'cred-id',
      rawId: new Uint8Array([0xfb, 0xff]).buffer,
      type: 'public-key',
      authenticatorAttachment: 'cross-platform',
      response: {
        clientDataJSON: new Uint8Array([4, 5, 6]).buffer,
        attestationObject: new Uint8Array([1, 2, 3]).buffer,
        getTransports: () => ['usb', 'nfc'],
      },
      getClientExtensionResults: () => ({ credProps: { rk: true } }),
    } as unknown as PublicKeyCredential

    expect(serializeAttestation(credential)).toEqual({
      id: 'cred-id',
      rawId: '-_8',
      type: 'public-key',
      response: {
        clientDataJSON: 'BAUG',
        attestationObject: 'AQID',
        transports: ['usb', 'nfc'],
      },
      clientExtensionResults: { credProps: { rk: true } },
      authenticatorAttachment: 'cross-platform',
    })
  })
})

describe('Passkey step_up 构造', () => {
  it('密码保留原值，验证码去首尾空白并使用真实字段名', () => {
    expect(buildPasskeyStepUp('password', ' p@ss word ')).toEqual({
      ok: true,
      proof: { password: ' p@ss word ' },
    })
    expect(buildPasskeyStepUp('two_factor_code', ' 123456 ')).toEqual({
      ok: true,
      proof: { two_factor_code: '123456' },
    })
  })

  it('空证明必须阻断，防止 begin 必然返回 step_up_required', () => {
    expect(buildPasskeyStepUp('password', '   ')).toEqual({ ok: false, error: '请输入当前密码' })
    expect(buildPasskeyStepUp('two_factor_code', '')).toEqual({
      ok: false,
      error: '请输入两步验证码或备用码',
    })
  })
})
