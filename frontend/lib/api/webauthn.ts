// WebAuthn(passkey)编解码纯逻辑(零依赖,可独立单测)。
// 后端用 go-webauthn,begin 返回标准 protocol.CredentialCreation JSON(challenge/user.id/
// excludeCredentials.id 为 base64url 无填充字符串);navigator.credentials.create 需要 ArrayBuffer,
// 其返回的 credential 又需编回 base64url 交 finish。base64url↔字节往返是本切片真风险点,故抽出此处。

// base64url(RFC4648 §5,URL 安全、无填充)→ 字节。容忍带/不带 '=' 填充。
export function base64urlToBytes(s: string): Uint8Array {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/');
  const pad = b64.length % 4 === 0 ? '' : '='.repeat(4 - (b64.length % 4));
  const bin = atob(b64 + pad);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

// 字节 → base64url(URL 安全、去尾部 '=' 填充),与 go-webauthn 解析端期望一致。
export function bytesToBase64url(input: ArrayBuffer | Uint8Array): string {
  const bytes = input instanceof Uint8Array ? input : new Uint8Array(input);
  let bin = '';
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

// begin 返回的 public_key 形如 { publicKey: { challenge, user:{id,...}, excludeCredentials:[{id,...}], ... } }。
// 把其中 base64url 二进制字段解成 Uint8Array,产出可直接喂 navigator.credentials.create 的对象。
// 漏解任一字段(challenge / user.id / excludeCredentials[].id)都会让浏览器 create 失败。
export function decodeCreationOptions(wrapped: { publicKey: Record<string, unknown> }): CredentialCreationOptions {
  const pk = wrapped.publicKey as Record<string, unknown>;
  const user = pk.user as Record<string, unknown>;
  const decoded: Record<string, unknown> = {
    ...pk,
    challenge: base64urlToBytes(pk.challenge as string),
    user: { ...user, id: base64urlToBytes(user.id as string) },
  };
  if (Array.isArray(pk.excludeCredentials)) {
    decoded.excludeCredentials = (pk.excludeCredentials as Array<Record<string, unknown>>).map((c) => ({
      ...c,
      id: base64urlToBytes(c.id as string),
    }));
  }
  return { publicKey: decoded as unknown as PublicKeyCredentialCreationOptions };
}

// navigator.credentials.create 返回的 PublicKeyCredential → 标准 CredentialCreationResponse JSON
// (rawId / clientDataJSON / attestationObject 编为 base64url),供 finish 提交、go-webauthn 解析。
export function encodeCredential(cred: PublicKeyCredential): Record<string, unknown> {
  const resp = cred.response as AuthenticatorAttestationResponse;
  return {
    id: cred.id,
    rawId: bytesToBase64url(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: bytesToBase64url(resp.clientDataJSON),
      attestationObject: bytesToBase64url(resp.attestationObject),
    },
    clientExtensionResults: cred.getClientExtensionResults ? cred.getClientExtensionResults() : {},
  };
}

// 免密登录:login/begin 返回的 public_key 形如 { publicKey: { challenge, allowCredentials:[{id,...}], ... } }。
// 把 challenge 与 allowCredentials[].id 由 base64url 解成字节,产出可喂 navigator.credentials.get 的对象。
export function decodeRequestOptions(wrapped: { publicKey: Record<string, unknown> }): CredentialRequestOptions {
  const pk = wrapped.publicKey as Record<string, unknown>;
  const decoded: Record<string, unknown> = { ...pk, challenge: base64urlToBytes(pk.challenge as string) };
  if (Array.isArray(pk.allowCredentials)) {
    decoded.allowCredentials = (pk.allowCredentials as Array<Record<string, unknown>>).map((c) => ({
      ...c,
      id: base64urlToBytes(c.id as string),
    }));
  }
  return { publicKey: decoded as unknown as PublicKeyCredentialRequestOptions };
}

// navigator.credentials.get 返回的 PublicKeyCredential(断言)→ 标准 CredentialRequestResponse JSON。
// 断言响应字段(authenticatorData/signature/userHandle/clientDataJSON)编 base64url;userHandle 可空。
export function encodeAssertion(cred: PublicKeyCredential): Record<string, unknown> {
  const resp = cred.response as AuthenticatorAssertionResponse;
  return {
    id: cred.id,
    rawId: bytesToBase64url(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: bytesToBase64url(resp.clientDataJSON),
      authenticatorData: bytesToBase64url(resp.authenticatorData),
      signature: bytesToBase64url(resp.signature),
      userHandle: resp.userHandle ? bytesToBase64url(resp.userHandle) : null,
    },
    clientExtensionResults: cred.getClientExtensionResults ? cred.getClientExtensionResults() : {},
  };
}

// 浏览器是否支持 WebAuthn(用于注册/登录前能力探测,不支持给友好提示而非直接报错)。
export function isWebAuthnSupported(): boolean {
  return typeof window !== 'undefined' && typeof window.PublicKeyCredential === 'function';
}
