// passkey 免密登录切片强测试。纯逻辑(webauthn 请求选项解码 + 断言编码,真风险点)+ auth.ts 接线断言。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { bytesToBase64url, decodeRequestOptions, encodeAssertion } from './webauthn.ts';

const ROOT = process.cwd();
const authSrc = readFileSync(join(ROOT, 'lib/api/auth.ts'), 'utf8');

// ── decodeRequestOptions:challenge + allowCredentials[].id 解成字节 ──────

test('TestDecodeRequestOptions_DecodesChallengeAndAllowCredentials', () => {
  const wrapped = {
    publicKey: {
      challenge: 'SGVsbG8', // "Hello"
      allowCredentials: [{ id: 'AQID', type: 'public-key', transports: ['internal'] }], // [1,2,3]
      userVerification: 'required',
      rpId: 'huakai.test',
    },
  };
  const out = decodeRequestOptions(wrapped) as unknown as { publicKey: Record<string, unknown> };
  const pk = out.publicKey;
  // 判别:漏解 challenge → 仍是字符串 → instanceof 断言 red(浏览器 get 会失败)。
  assert.ok(pk.challenge instanceof Uint8Array, 'challenge 应解成字节');
  assert.equal(bytesToBase64url(pk.challenge as Uint8Array), 'SGVsbG8');
  const allow = pk.allowCredentials as Array<{ id: unknown; transports: unknown }>;
  assert.ok(allow[0].id instanceof Uint8Array, 'allowCredentials[].id 应解成字节');
  assert.equal(bytesToBase64url(allow[0].id as Uint8Array), 'AQID');
  assert.deepEqual(allow[0].transports, ['internal'], '非二进制字段原样保留');
  assert.equal(pk.userVerification, 'required', 'userVerification 透传');
});

// ── encodeAssertion:authenticatorData / signature / userHandle / clientDataJSON 编 base64url ──

test('TestEncodeAssertion_EncodesAllResponseFields', () => {
  const mock = {
    id: 'cred-x',
    rawId: new Uint8Array([1, 2, 3]).buffer,
    type: 'public-key',
    response: {
      clientDataJSON: new Uint8Array([72, 101, 108, 108, 111]).buffer, // "Hello"
      authenticatorData: new Uint8Array([4, 5, 6]).buffer, // BAUG
      signature: new Uint8Array([7, 8, 9]).buffer, // BwgJ
      userHandle: null,
    },
    getClientExtensionResults: () => ({}),
  } as unknown as PublicKeyCredential;
  const out = encodeAssertion(mock) as {
    rawId: string;
    response: { clientDataJSON: string; authenticatorData: string; signature: string; userHandle: string | null };
  };
  assert.equal(out.rawId, 'AQID');
  assert.equal(out.response.clientDataJSON, 'SGVsbG8', 'clientDataJSON 编 base64url');
  assert.equal(out.response.authenticatorData, 'BAUG', 'authenticatorData 编 base64url');
  // 判别:漏编 signature → 与已知向量不等 → red(签名错则后端验签必败)。
  assert.equal(out.response.signature, 'BwgJ', 'signature 编 base64url');
  assert.equal(out.response.userHandle, null, '无 userHandle 时应为 null');
});

test('TestEncodeAssertion_EncodesUserHandleWhenPresent', () => {
  const mock = {
    id: 'c',
    rawId: new Uint8Array([1]).buffer,
    type: 'public-key',
    response: {
      clientDataJSON: new Uint8Array([1]).buffer,
      authenticatorData: new Uint8Array([1]).buffer,
      signature: new Uint8Array([1]).buffer,
      userHandle: new Uint8Array([1, 2, 3]).buffer, // [1,2,3] → AQID
    },
    getClientExtensionResults: () => ({}),
  } as unknown as PublicKeyCredential;
  const out = encodeAssertion(mock) as { response: { userHandle: string | null } };
  // 判别:有 userHandle 却没编 → red(部分认证器靠 userHandle 定位用户)。
  assert.equal(out.response.userHandle, 'AQID', '有 userHandle 应编 base64url');
});

// ── auth.ts 接线:passkey login begin/finish 端点 + credentials + 会话存储 ──

test('TestPasskeyLogin_WiringEndpointsAndSession', () => {
  assert.match(authSrc, /fetch\('\/v1\/auth\/passkey\/login\/begin'/, 'begin 应 POST /v1/auth/passkey/login/begin');
  assert.match(authSrc, /fetch\('\/v1\/auth\/passkey\/login\/finish'/, 'finish 应 POST /v1/auth/passkey/login/finish');
  // 免密登录两端都需 credentials(begin 设 state/挑战上下文,finish 回传)。
  assert.match(authSrc, /passkey\/login\/begin'[\s\S]{0,200}credentials:\s*'same-origin'/, 'begin 须带 credentials');
  // finish 返 {user, session},必须用返回 user 存会话(漏则会话丢用户)。
  // 关键:把断言【限定在 passkeyLoginFinish 函数体切片内】——否则会误匹配 completeOAuth / login 里的
  // storeSession,导致变异 passkeyLoginFinish 那条仍绿(非判别)。切片到下一个 export 为止。
  const start = authSrc.indexOf('export async function passkeyLoginFinish');
  assert.ok(start >= 0, '应存在 passkeyLoginFinish');
  const after = authSrc.indexOf('export async function login', start);
  const finishBody = authSrc.slice(start, after > start ? after : undefined);
  assert.match(finishBody, /storeSession\(r\.session,\s*r\.user\)/, 'passkeyLoginFinish 必须用返回 user 存会话');
});
