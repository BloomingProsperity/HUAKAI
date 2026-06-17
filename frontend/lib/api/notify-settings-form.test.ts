// admin per-user 通知设置接线强测试。
// 纯逻辑（notify_type 条件校验 + 精确 key-set 构造）+ adminUserNotifications.ts 接线断言（路径/动词/builder/validate）。
// 每条断言可一句话说出抓的回归；均经变异实测转红再还原（CLAUDE.md §14）。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { buildNotifySettingsBody, validateNotifySettings } from './notify-settings-form.ts';

const ROOT = process.cwd();
const adminSrc = readFileSync(join(ROOT, 'lib/api/adminUserNotifications.ts'), 'utf8');

function bodyAfter(marker: string): string {
  const start = adminSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = adminSrc.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |async function |function |export interface |export const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : adminSrc.length;
  return adminSrc.slice(start, end);
}

// ── validateNotifySettings：notify_type 五路条件校验 ─────────────────────────

test('TestValidateNotifySettings_TypeConditional', () => {
  // 判别：非法 notify_type 应拒。
  assert.ok(validateNotifySettings({ notify_type: 'sms' as never }), '非法 type 应报错');
  // none：无要求。
  assert.equal(validateNotifySettings({ notify_type: 'none' }), null, 'none 无要求通过');
  // email：需合法 notification_email。
  assert.ok(validateNotifySettings({ notify_type: 'email' }), 'email 缺邮箱应报错');
  assert.ok(validateNotifySettings({ notify_type: 'email', notification_email: 'not-an-email' }), '非法邮箱应报错');
  assert.equal(validateNotifySettings({ notify_type: 'email', notification_email: 'u@x.com' }), null, '合法邮箱通过');
  // webhook：需非空 secret + 合法 https url。
  assert.ok(validateNotifySettings({ notify_type: 'webhook', webhook_url: 'https://h.x/cb' }), 'webhook 缺 secret 应报错');
  assert.ok(validateNotifySettings({ notify_type: 'webhook', webhook_secret: 's', webhook_url: 'not-a-url' }), 'webhook 非法 url 应报错');
  // 判别：后端 ValidateOAuthEndpointURL 强制 https，http:// 会被 400——前端须同样拒（变异:正则放 http?s → 此条假绿）。
  assert.ok(validateNotifySettings({ notify_type: 'webhook', webhook_secret: 's', webhook_url: 'http://h.x/cb' }), 'webhook http:// 应报错(后端只收 https)');
  assert.equal(validateNotifySettings({ notify_type: 'webhook', webhook_secret: 's', webhook_url: 'https://h.x/cb' }), null, 'webhook 合法 https 通过');
  // bark：需合法 https url。
  assert.ok(validateNotifySettings({ notify_type: 'bark', bark_url: 'ftp://x' }), 'bark 非法 url 应报错');
  assert.ok(validateNotifySettings({ notify_type: 'bark', bark_url: 'http://bark.x/k' }), 'bark http:// 应报错');
  assert.equal(validateNotifySettings({ notify_type: 'bark', bark_url: 'https://bark.x/k' }), null, 'bark 合法 https 通过');
});

test('TestValidateNotifySettings_Gotify', () => {
  const base = { notify_type: 'gotify' as const, gotify_url: 'https://g.x', gotify_token: 'tok' };
  // 判别：缺 token 应报错。
  assert.ok(validateNotifySettings({ ...base, gotify_token: '' }), 'gotify 缺 token 应报错');
  // 判别：priority **缺省合法**——后端 notifyRequestToSettings 默认 5，前端不能过严拒 undefined。
  // 变异: 若校验改成 priority===undefined 也拒 → 此条假绿(实为后端会接受)。
  assert.equal(validateNotifySettings({ ...base }), null, 'gotify 缺 priority 应通过(后端默认 5)');
  // 判别：给定时须 1..10；0 与 11 应报错。
  assert.ok(validateNotifySettings({ ...base, gotify_priority: 0 }), 'priority=0 应报错');
  assert.ok(validateNotifySettings({ ...base, gotify_priority: 11 }), 'priority=11 应报错');
  // 判别：边界 1 与 10 通过。
  assert.equal(validateNotifySettings({ ...base, gotify_priority: 1 }), null, 'priority=1 通过');
  assert.equal(validateNotifySettings({ ...base, gotify_priority: 10 }), null, 'priority=10 通过');
  // 判别：非法/非 https url 应报错。
  assert.ok(validateNotifySettings({ ...base, gotify_priority: 5, gotify_url: 'g.x' }), 'gotify 非法 url 应报错');
  assert.ok(validateNotifySettings({ ...base, gotify_priority: 5, gotify_url: 'http://g.x' }), 'gotify http:// 应报错');
});

// ── buildNotifySettingsBody：精确 key-set（DisallowUnknownFields）────────────

test('TestBuildNotifySettingsBody_ExactKeySet', () => {
  // 判别：最小体只含 notify_type；多余/错键 → 后端 DisallowUnknownFields 400。
  assert.deepEqual(Object.keys(buildNotifySettingsBody({ notify_type: 'none' })), ['notify_type'], '最小体仅 notify_type');
  // 判别：给定字段精确映射，空 optional 省略；key-set 不含未知键。
  const full = buildNotifySettingsBody({
    notify_type: 'gotify',
    gotify_url: 'https://g.x',
    gotify_token: 'tok',
    gotify_priority: 5,
    webhook_url: '', // 空应省略
    balance_threshold: '5',
  });
  assert.deepEqual(
    Object.keys(full).sort(),
    ['balance_threshold', 'gotify_priority', 'gotify_token', 'gotify_url', 'notify_type'],
    '精确 key-set：空 webhook_url 省略，无未知键',
  );
  assert.equal(full.gotify_priority, 5, 'gotify_priority 透传(数值)');
  assert.equal(full.balance_threshold, '5', 'balance_threshold 透传');
});

// ── adminUserNotifications.ts 接线：路径(user_id+tenant) + 动词 + builder + validate ──

test('TestEndpoints_Wiring', () => {
  const get = bodyAfter('export function getAdminUserNotifySettings');
  const save = bodyAfter('export function saveAdminUserNotifySettings');
  // 判别：路径须 /v1/admin/users/${userId}/notifications（user_id 在路径）。
  assert.match(get, /`\/v1\/admin\/users\/\$\{userId\}\/notifications\$\{tenantQuery\(tenantId\)\}`/, 'GET 路径含 user_id + tenantQuery');
  assert.match(save, /`\/v1\/admin\/users\/\$\{userId\}\/notifications\$\{tenantQuery\(tenantId\)\}`/, 'PUT 路径含 user_id + tenantQuery');
  // 判别：GET 用 apiGet（admin token）；PUT 用 adminPut（client.ts 无 PUT）。
  assert.match(get, /apiGet</, 'GET 用 apiGet');
  assert.match(save, /adminPut</, 'PUT 用 adminPut');
  // 判别：保存前先 validateNotifySettings（fail-fast，validator 不悬空）+ 经 builder 构造体。
  assert.match(save, /validateNotifySettings\(body\)/, 'PUT 前先校验');
  assert.match(save, /buildNotifySettingsBody\(body\)/, 'PUT 用 builder 构造精确 key-set');
});
