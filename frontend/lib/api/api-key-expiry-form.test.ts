// API-key expiry 更新数据层切片测试。纯逻辑（三态 builder / future 校验 / 错误码映射）
// + apiKeys.ts updateApiKey 接线断言（PATCH 方法 + 路径）。每条断言一句话说清抓的回归；均经变异实测转红。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  apiKeyPatchErrorMessage,
  buildApiKeyExpiryPatch,
  validateApiKeyExpiry,
} from './api-key-expiry-form.ts';

const ROOT = process.cwd();
const clientSrc = readFileSync(join(ROOT, 'lib/api/apiKeys.ts'), 'utf8');

function bodyAfter(marker: string): string {
  const start = clientSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = clientSrc.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |export interface |export const |function |const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : clientSrc.length;
  return clientSrc.slice(start, end);
}

// ── buildApiKeyExpiryPatch: 三态精确编码 ──────────────────────────────────
test('TestBuildApiKeyExpiryPatch_TriState', () => {
  // 判别(安全核心): 'unchanged' 绝不含 expires_at 键 —— 否则后端把它当 clear/set 静默改有效期。
  // mutation: builder 给 unchanged 也带 expires_at → key-set 断言红。
  assert.deepEqual(Object.keys(buildApiKeyExpiryPatch('unchanged')), [], 'unchanged 不带 expires_at 键');
  // 判别: 'clear' 编码为空串(后端空串=清成永不过期; 非空串/缺失都不行)。
  assert.deepEqual(buildApiKeyExpiryPatch('clear'), { expires_at: '' }, 'clear 编码为空串');
  // 判别: 'set' 透传 RFC3339 串(后端按该时刻设有效期)。
  assert.deepEqual(
    buildApiKeyExpiryPatch('set', '2027-01-02T03:04:05Z'),
    { expires_at: '2027-01-02T03:04:05Z' },
    'set 透传 RFC3339',
  );
  // 判别: 'set' 缺时刻 → 抛错(防构造出空 set 把有效期设成空串误清除)。
  assert.throws(() => buildApiKeyExpiryPatch('set'), /set/, 'set 缺时刻应抛错');
  assert.throws(() => buildApiKeyExpiryPatch('set', '   '), /set/, 'set 空白时刻应抛错');
});

// ── validateApiKeyExpiry: 仅将来时刻(对齐后端 reject-past delta) ───────────
test('TestValidateApiKeyExpiry_FutureOnly', () => {
  const now = Date.parse('2026-06-18T00:00:00Z');
  // 判别: 将来时刻通过。
  assert.equal(validateApiKeyExpiry('2027-01-01T00:00:00Z', now), null, '将来时刻通过');
  // 判别: 过去时刻拒(对齐后端 ErrInvalidExpiry; mutation: validate 漏掉 t<=now 检查 → 返回 null → 红)。
  assert.ok(validateApiKeyExpiry('2025-01-01T00:00:00Z', now), '过去时刻应拒');
  // 判别: 恰好 now(非严格将来)应拒(后端用 !After(now), 等于也算过期)。
  assert.ok(validateApiKeyExpiry('2026-06-18T00:00:00Z', now), '等于 now 应拒');
  // 判别: 非法格式拒。
  assert.ok(validateApiKeyExpiry('not-a-date', now), '非法格式应拒');
});

// ── apiKeyPatchErrorMessage ──────────────────────────────────────────────
test('TestApiKeyPatchErrorMessage', () => {
  // 判别: 已知码映射到专属文案(漏映射 → 回退泛文案带 code)。
  for (const code of ['invalid_expires_at', 'api_key_not_found', 'invalid_name', 'session_required', 'userkey_backend_error']) {
    assert.ok(!apiKeyPatchErrorMessage(code).includes(code), `已知码 ${code} 有专属文案`);
  }
  assert.match(apiKeyPatchErrorMessage('weird_code'), /weird_code/, '未知码回退带 code');
});

// ── apiKeys.ts updateApiKey 接线: PATCH 方法 + 路径 ───────────────────────
test('TestUpdateApiKey_Wiring', () => {
  const fn = bodyAfter('export function updateApiKey');
  // 判别: 用 userPatch(发 PATCH); mutation: 误用 userPost/userPut → 红。
  assert.match(fn, /userPatch</, 'updateApiKey 用 userPatch');
  // 判别: 路径 BASE_PATH/{id}(单 key); mutation: 漏 /${id} 打到集合端点 → 红。
  assert.match(fn, /\$\{BASE_PATH\}\/\$\{id\}/, 'PATCH 路径 BASE_PATH/{id}');
  // 判别: 透传整个 patch body(含三态 expires_at)。
  assert.match(fn, /\(\s*`\$\{BASE_PATH\}\/\$\{id\}`\s*,\s*patch\s*\)/, 'updateApiKey 透传 patch body');
});
