// 登录 2FA 切片强测试。两部分:
//  (1) 纯逻辑(otp.ts)——验证码清洗 + 满位判定,变异自检;
//  (2) 源码接线断言(auth.ts)——verifyLoginTwoFactor 打对端点、传 challenge_id+code、
//      且【用第一步带回的 user 存会话】(/login/2fa 只返 session,漏带 user 会丢用户信息)。
//      auth.ts 含 ./userClient + @/lib/auth/session 导入,node strip-types 无法直接 import,故读源码文本断言。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { sanitizeOtp, isOtpComplete, OTP_LENGTH } from './otp.ts';

const ROOT = process.cwd();
const authSrc = readFileSync(join(ROOT, 'lib/api/auth.ts'), 'utf8');

// ── (1) sanitizeOtp:只留数字 + 截断 6 位 ────────────────────────────

test('TestSanitizeOtp_StripsNonDigitsAndCaps6', () => {
  // 判别 A:若漏 replace(\D) → 空格/连字符留下 → ≠'123456' red(自动填充常带分隔符)。
  assert.equal(sanitizeOtp('12 34-56'), '123456', '应剔除空格与连字符');
  assert.equal(sanitizeOtp('abc123'), '123', '应剔除字母');
  // 判别 B:若漏 slice(0,6) → '1234567' 不截断 → 永远 ≠6 位、自动提交永不触发 → red。
  assert.equal(sanitizeOtp('1234567'), '123456', '应截断到 6 位');
  assert.equal(sanitizeOtp(''), '', '空串得空');
});

test('TestIsOtpComplete_Exactly6Digits', () => {
  assert.equal(isOtpComplete('123456'), true, '6 位数字应判完整(触发自动提交)');
  assert.equal(isOtpComplete('12345'), false, '5 位不完整');
  assert.equal(isOtpComplete('1234567'), false, '7 位不完整');
  assert.equal(isOtpComplete('12a456'), false, '含非数字不完整');
  assert.equal(OTP_LENGTH, 6, 'OTP 长度常量应为 6');
});

// ── (2) auth.ts 接线:verifyLoginTwoFactor 端点 + 请求体 + 会话存储 ────

test('TestVerifyLoginTwoFactor_PostsToCorrectEndpoint', () => {
  // 判别:端点写错(漏 /2fa)→ red。
  assert.match(authSrc, /fetch\('\/v1\/auth\/login\/2fa'/, '应 POST /v1/auth/login/2fa');
});

test('TestVerifyLoginTwoFactor_SendsChallengeIdAndCode', () => {
  // 判别:请求体漏 challenge_id 或 code → red(后端二者必填,缺则 400)。
  assert.match(authSrc, /challenge_id:\s*input\.challenge_id/, '请求体应含 challenge_id');
  assert.match(authSrc, /code:\s*input\.code/, '请求体应含 code');
});

test('TestVerifyLoginTwoFactor_StoresSessionWithCarriedUser', () => {
  // 核心判别:/login/2fa 只返 session、不返 user;必须用第一步 login 带回的 input.user 存会话。
  // 变异:写成 storeSession(session) 或漏 input.user → 此断言 red → 会话丢失用户信息。
  assert.match(authSrc, /storeSession\(session,\s*input\.user\)/, '必须用第一步带回的 user 存会话');
});
