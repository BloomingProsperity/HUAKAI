// 社交/OAuth 登录切片强测试。两部分:
//  (1) 纯逻辑(siteConfig.parseEnabledProviders)——后端逗号清单解析,变异自检;
//  (2) 源码接线断言(auth.ts)——startOAuth/completeOAuth 打对端点、带 credentials(传/收 state cookie)、
//      且 completeOAuth【用回调返回的 user 存会话】。auth.ts 含 ./userClient + @/lib/auth/session 导入,
//      node strip-types 无法直接 import,故读源码文本断言。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { parseEnabledProviders } from './siteConfig.ts';

const ROOT = process.cwd();
const authSrc = readFileSync(join(ROOT, 'lib/api/auth.ts'), 'utf8');

// ── (1) parseEnabledProviders:去空白 + 小写 + 去重保序 + 剔空段 ───────────

test('TestParseEnabledProviders_TrimLowerDedupDropEmpty', () => {
  const out = parseEnabledProviders('github, google ,GitHub,,linuxdo');
  // 判别:漏 trim→' google ';漏 toLowerCase→'GitHub' 成第 4 项;漏去重→'github' 两次;
  //       漏剔空段→含 ''。任一变异都让此精确相等 red。
  assert.deepEqual(out, ['github', 'google', 'linuxdo'], '应去空白/小写/去重/剔空,且保序');
});

test('TestParseEnabledProviders_EmptyInputs', () => {
  assert.deepEqual(parseEnabledProviders(undefined), [], 'undefined → 空数组(不渲染按钮)');
  assert.deepEqual(parseEnabledProviders(''), [], '空串 → 空数组');
  assert.deepEqual(parseEnabledProviders('  ,  '), [], '全空白逗号 → 空数组');
});

// ── (2) auth.ts 接线:OAuth init/callback 端点 + credentials + 会话存储 ───

test('TestStartOAuth_PostsInitWithCredentials', () => {
  // 判别:端点写错 → red;漏 credentials → state cookie 设不上、回调校验必失败 → red。
  assert.match(authSrc, /fetch\('\/v1\/auth\/oauth-init'/, 'startOAuth 应 POST /v1/auth/oauth-init');
  assert.match(authSrc, /oauth-init'[\s\S]{0,200}credentials:\s*'same-origin'/, 'oauth-init 必须带 credentials 以设 state cookie');
});

test('TestCompleteOAuth_PostsCallbackWithCredentials', () => {
  assert.match(authSrc, /fetch\('\/v1\/auth\/oauth-callback'/, 'completeOAuth 应 POST /v1/auth/oauth-callback');
  assert.match(authSrc, /oauth-callback'[\s\S]{0,200}credentials:\s*'same-origin'/, 'oauth-callback 必须带 credentials 以回传 state cookie');
});

test('TestCompleteOAuth_StoresSessionWithReturnedUser', () => {
  // 核心判别:回调返 {user, session},必须用返回的 user 存会话。
  // 变异:storeSession(r.session) 漏 user 或写错 → red → 会话丢用户信息。
  assert.match(authSrc, /storeSession\(r\.session,\s*r\.user\)/, 'completeOAuth 必须用回调返回的 user 存会话');
});
