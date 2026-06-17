// 通知 admin 接线强测试。
// 纯逻辑（校验 + 精确 key-set 构造，直接 strip-types 单测）+ adminNotifications.ts 接线断言（路径 + 动词 + builder）。
// 每条断言可一句话说出抓的回归；均经变异实测转红再还原（CLAUDE.md §14）。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { buildBroadcastBody, validateBroadcast } from './notifications-admin-form.ts';

const ROOT = process.cwd();
const adminSrc = readFileSync(join(ROOT, 'lib/api/adminNotifications.ts'), 'utf8');

function bodyAfter(marker: string): string {
  const start = adminSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = adminSrc.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |async function |function |export interface |export const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : adminSrc.length;
  return adminSrc.slice(start, end);
}

// ── validateBroadcast：title/body 必填 + severity 允许集 + tenant_id 正整数 ─────────

test('TestValidateBroadcast', () => {
  // 判别：漏 title/body 必填 → 空白广播发出 → 后端 400 或发空通知。
  assert.ok(validateBroadcast({ title: '  ', body: 'x' }), '空白 title 应报错');
  assert.ok(validateBroadcast({ title: 'x', body: '' }), '空 body 应报错');
  // 判别：漏 severity 允许集 → 任意串发出 → 后端 invalid severity。
  assert.ok(validateBroadcast({ title: 'x', body: 'y', severity: 'urgent' }), '非法 severity 应报错');
  // 判别：tenant_id 非正整数应拒。fixture 用 0 与小数。
  assert.ok(validateBroadcast({ title: 'x', body: 'y', tenant_id: 0 }), 'tenant_id=0 应报错');
  assert.ok(validateBroadcast({ title: 'x', body: 'y', tenant_id: 1.5 }), 'tenant_id 非整数应报错');
  // 合法：severity 省略（默认 info）/ 给允许值 / tenant_id 省略 均通过。
  assert.equal(validateBroadcast({ title: 'x', body: 'y' }), null, '最小合法应通过');
  assert.equal(validateBroadcast({ title: 'x', body: 'y', severity: 'critical', tenant_id: 7 }), null, '全字段合法应通过');
});

// ── buildBroadcastBody：精确 key-set（DisallowUnknownFields）─────────────────────

test('TestBuildBroadcastBody_ExactKeySet', () => {
  // 判别：最小体只含 title/body；若 builder 多塞任何键(如把 body 写成 message、或加 priority) →
  // 后端 DisallowUnknownFields 400。key-set 精确比对抓这类回归。
  const minimal = buildBroadcastBody({ title: 't', body: 'b' });
  assert.deepEqual(Object.keys(minimal).sort(), ['body', 'title'], '最小体精确含 title/body 两键');
  assert.equal(minimal.title, 't', 'title 映到 title 键');
  assert.equal(minimal.body, 'b', 'body 映到 body 键');
  // 判别：空 severity / 未给 tenant_id 必须省略（否则发 severity:"" 或 tenant_id:0 给后端）。
  assert.equal('severity' in minimal, false, '空 severity 应省略');
  assert.equal('tenant_id' in minimal, false, '未给 tenant_id 应省略');
});

test('TestBuildBroadcastBody_FullKeySet', () => {
  const full = buildBroadcastBody({ title: 't', body: 'b', severity: 'warning', tenant_id: 7 });
  // 判别：四字段都给时 key-set 恰为这 4 个，无多余；severity/tenant_id 正确映射。
  assert.deepEqual(Object.keys(full).sort(), ['body', 'severity', 'tenant_id', 'title'], '全字段恰 4 键无多余');
  assert.equal(full.severity, 'warning', 'severity 透传');
  assert.equal(full.tenant_id, 7, 'tenant_id 透传(非硬编码)');
});

// ── adminNotifications.ts 接线：路径 + 动词 + builder ───────────────────────────

test('TestEndpoints_PathsAndVerbs', () => {
  const broadcast = bodyAfter('export function broadcastNotification');
  const stats = bodyAfter('export function getNotificationWorkerStats');
  // 判别：路径写错 → 打到错误后端路由(404/405) → 红。锚定引号定界符。
  assert.match(broadcast, /'\/v1\/admin\/notifications\/broadcast'/, 'broadcast 路径');
  assert.match(stats, /'\/v1\/admin\/notifications\/worker-stats'/, 'worker-stats 路径');
  // 判别：broadcast 是写操作须 apiPost(误用 apiGet → 后端 405)；worker-stats 是读须 apiGet。
  assert.match(broadcast, /apiPost</, 'broadcast 用 POST');
  assert.match(stats, /apiGet</, 'worker-stats 用 GET');
  // 判别：broadcast 漏用 builder → 绕过精确 key-set → 可能发未知键 → 红。
  assert.match(broadcast, /buildBroadcastBody\(input\)/, 'broadcast 用 builder 构造体');
  // 判别：broadcast 写路径须发请求前先 validateBroadcast(fail-fast + 消除 validate/build 分歧)；
  // 漏校验 → validator 悬空、空白 severity 分歧重现 → 红。
  assert.match(broadcast, /validateBroadcast\(input\)/, 'broadcast 发请求前先校验');
});
