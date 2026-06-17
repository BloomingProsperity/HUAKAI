// WebAuthn 编解码强测试(纯逻辑,真风险点)。base64url↔字节往返错一位 → 浏览器 create 失败或后端解析失败。
import assert from 'node:assert/strict';
import test from 'node:test';

import { base64urlToBytes, bytesToBase64url, decodeCreationOptions, encodeCredential } from './webauthn.ts';

// ── base64url 往返 + 已知向量 + URL 安全 ────────────────────────────

test('TestBase64url_RoundTrip', () => {
  const cases: number[][] = [[], [72, 101, 108, 108, 111], [0, 1, 2, 253, 254, 255], [255], [0, 0, 0, 0]];
  for (const arr of cases) {
    const bytes = new Uint8Array(arr);
    const round = base64urlToBytes(bytesToBase64url(bytes));
    // 判别:编/解任一错位 → 往返不等 → red。
    assert.deepEqual(Array.from(round), arr, `往返失真: [${arr}]`);
  }
});

test('TestBase64url_KnownVectorAndUrlSafe', () => {
  // 已知向量:[72..111]="Hello" → base64url 无填充 'SGVsbG8'。判别:误用标准 base64(带 '=')→ red。
  assert.equal(bytesToBase64url(new Uint8Array([72, 101, 108, 108, 111])), 'SGVsbG8');
  // URL 安全:高位字节产出必须不含 '+' '/' '='(否则 go-webauthn URL 解码失败)。
  const enc = bytesToBase64url(new Uint8Array([251, 255, 254, 250]));
  assert.match(enc, /^[A-Za-z0-9_-]+$/, `不应含 +/= : ${enc}`);
  assert.deepEqual(Array.from(base64urlToBytes(enc)), [251, 255, 254, 250], 'URL 安全串应能解回');
});

// ── decodeCreationOptions:challenge / user.id / excludeCredentials[].id 都要解成字节 ──

test('TestDecodeCreationOptions_DecodesAllBinaryFields', () => {
  const wrapped = {
    publicKey: {
      challenge: 'SGVsbG8', // "Hello"
      rp: { id: 'huakai.test', name: 'HUAKAI' },
      user: { id: 'AQID', name: 'u@e.test', displayName: 'U' }, // [1,2,3]
      pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
      excludeCredentials: [{ id: 'BAUG', type: 'public-key' }], // [4,5,6]
    },
  };
  const out = decodeCreationOptions(wrapped) as unknown as { publicKey: Record<string, unknown> };
  const pk = out.publicKey;
  // 判别:漏解 challenge → 仍是字符串 → 下面 instanceof 断言 red。
  assert.ok(pk.challenge instanceof Uint8Array, 'challenge 应解成字节');
  assert.equal(bytesToBase64url(pk.challenge as Uint8Array), 'SGVsbG8', 'challenge 字节应可编回原值');
  const user = pk.user as { id: unknown };
  assert.ok(user.id instanceof Uint8Array, 'user.id 应解成字节');
  assert.equal(bytesToBase64url(user.id as Uint8Array), 'AQID');
  const exclude = pk.excludeCredentials as Array<{ id: unknown }>;
  assert.ok(exclude[0].id instanceof Uint8Array, 'excludeCredentials[].id 应解成字节');
  assert.equal(bytesToBase64url(exclude[0].id as Uint8Array), 'BAUG');
  // rp / pubKeyCredParams 等非二进制字段原样保留。
  assert.deepEqual(pk.rp, { id: 'huakai.test', name: 'HUAKAI' });
});

// ── encodeCredential:rawId / clientDataJSON / attestationObject 编 base64url ──

test('TestEncodeCredential_EncodesResponseBuffers', () => {
  const mock = {
    id: 'cred-id-x',
    rawId: new Uint8Array([1, 2, 3]).buffer,
    type: 'public-key',
    response: {
      clientDataJSON: new Uint8Array([72, 101, 108, 108, 111]).buffer, // "Hello"
      attestationObject: new Uint8Array([4, 5, 6]).buffer,
    },
    getClientExtensionResults: () => ({}),
  } as unknown as PublicKeyCredential;
  const out = encodeCredential(mock) as {
    id: string;
    rawId: string;
    type: string;
    response: { clientDataJSON: string; attestationObject: string };
  };
  assert.equal(out.id, 'cred-id-x', 'id 透传');
  assert.equal(out.type, 'public-key', 'type 透传');
  assert.equal(out.rawId, 'AQID', 'rawId 编 base64url');
  // 判别:漏编 clientDataJSON 或编错 → 与已知向量不等 → red。
  assert.equal(out.response.clientDataJSON, 'SGVsbG8', 'clientDataJSON 编 base64url');
  assert.equal(out.response.attestationObject, 'BAUG', 'attestationObject 编 base64url');
});
