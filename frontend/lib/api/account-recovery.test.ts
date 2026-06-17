// 邮箱验证 / 找回密码切片强测试。纯逻辑(新密码校验)+ auth.ts 接线断言(端点 + 请求/完成双模式区分)。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { validateNewPassword, MIN_PASSWORD_LEN } from './password-reset.ts';

const ROOT = process.cwd();
const authSrc = readFileSync(join(ROOT, 'lib/api/auth.ts'), 'utf8');

// 取某 export function 的【函数体】切片(到首个 \n} 为止)。收到闭合括号即止,避免把下一个函数的
// 前置注释(里含 token/new_password 字样)误圈进来导致非判别。这些函数都是单 return 体。
function fnBody(name: string): string {
  const start = authSrc.indexOf('export function ' + name);
  if (start < 0) throw new Error('找不到 ' + name);
  const end = authSrc.indexOf('\n}', start);
  return authSrc.slice(start, end > start ? end + 2 : undefined);
}

// ── validateNewPassword:长度 + 一致 ────────────────────────────────

test('TestValidateNewPassword', () => {
  // 判别:漏长度检查 → 短密码返 null → red。
  assert.ok(validateNewPassword('123', '123'), '过短应报错');
  assert.match(String(validateNewPassword('123', '123')), new RegExp(`${MIN_PASSWORD_LEN}`), '错误文案含最小长度');
  // 判别:漏一致检查 → 不一致返 null → red。
  assert.ok(validateNewPassword('longenough', 'different1'), '不一致应报错');
  // 合法:≥6 且一致 → null。
  assert.equal(validateNewPassword('longenough', 'longenough'), null, '合法应通过');
});

// ── auth.ts 接线:verify-email 端点 + reset-password 双模式区分 ─────────

test('TestVerifyEmail_Wiring', () => {
  const body = fnBody('verifyEmail');
  assert.match(body, /\/v1\/auth\/verify-email/, 'verifyEmail 应打 /v1/auth/verify-email');
  assert.match(body, /tenant_id:\s*tenantId/, '带 tenant_id');
  assert.match(body, /token/, '带 token');
});

test('TestRequestPasswordReset_RequestModeNoNewPassword', () => {
  const body = fnBody('requestPasswordReset');
  assert.match(body, /\/v1\/auth\/reset-password/, '请求重置打 /v1/auth/reset-password');
  assert.match(body, /email/, '请求模式带 email');
  // 关键判别:请求模式【不得】带 token 或 new_password(那是完成模式),否则后端会误入完成分支。
  assert.doesNotMatch(body, /new_password/, '请求模式不应带 new_password');
  assert.doesNotMatch(body, /\btoken\b/, '请求模式不应带 token');
});

test('TestResetPassword_CompleteModeSendsTokenAndNewPassword', () => {
  const body = fnBody('resetPassword');
  assert.match(body, /\/v1\/auth\/reset-password/, '完成重置打 /v1/auth/reset-password');
  // 关键判别:完成模式必须带 token + new_password,漏任一 → 后端无法重置 → red。
  assert.match(body, /token/, '完成模式带 token');
  assert.match(body, /new_password:\s*newPassword/, '完成模式带 new_password');
});
