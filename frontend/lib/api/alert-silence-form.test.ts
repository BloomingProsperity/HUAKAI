// 告警静默 CRUD 切片强测试。
// 纯逻辑（时间校验/跨字段 ends>starts/rule_id 正整数/请求体构造/DisallowUnknownFields 键集）
// + adminAlertSilences.ts 接线断言（list/create/delete 路径+动词+builder+tenant 位置）。
// 每条断言可一句话说出抓的回归；均经变异实测转红再还原（CLAUDE.md §14）。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  buildSilenceBody,
  coercePositiveInt,
  validateAlertSilenceForm,
  type AlertSilenceFormInput,
} from './alert-silence-form.ts';

const ROOT = process.cwd();
const adminSrc = readFileSync(join(ROOT, 'lib/api/adminAlertSilences.ts'), 'utf8');

function bodyAfter(marker: string): string {
  const start = adminSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = adminSrc.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |async function |function |export interface |export const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : adminSrc.length;
  return adminSrc.slice(start, end);
}

function validBase(): AlertSilenceFormInput {
  return { reason: '维护', starts_at_raw: '2026-01-01T08:00', ends_at_raw: '2026-01-01T09:00', rule_id_raw: '', platform: '', group_id: '', region: '' };
}

// ── coercePositiveInt：空→null；正整数→数；非法→NaN ──────────────────────

test('TestCoercePositiveInt', () => {
  assert.equal(coercePositiveInt(''), null, '空 → null（未提供）');
  assert.equal(coercePositiveInt('   '), null, '纯空白 → null');
  assert.equal(coercePositiveInt('5'), 5, '正整数 → 数');
  // 判别：逐个非法形态 → NaN（漏任一守门 → 脏 rule_id 进请求）。
  assert.ok(Number.isNaN(coercePositiveInt('0')), '0 → NaN');
  assert.ok(Number.isNaN(coercePositiveInt('-1')), '负数 → NaN');
  assert.ok(Number.isNaN(coercePositiveInt('1.5')), '小数 → NaN');
  assert.ok(Number.isNaN(coercePositiveInt('1e3')), '科学计数 → NaN');
  assert.ok(Number.isNaN(coercePositiveInt('abc')), '非数 → NaN');
});

// ── 校验：starts/ends 必填 + 跨字段 ends>starts + rule_id 正整数 ─────────

test('TestValidate_TimeRequiredAndCross', () => {
  assert.equal(validateAlertSilenceForm(validBase()), null, '合法基线应通过');
  assert.ok(validateAlertSilenceForm({ ...validBase(), starts_at_raw: '' }), '空 starts 应报错');
  assert.ok(validateAlertSilenceForm({ ...validBase(), ends_at_raw: '' }), '空 ends 应报错');
  assert.ok(validateAlertSilenceForm({ ...validBase(), starts_at_raw: 'not-a-date' }), '非法 starts 应报错');
  // 判别：漏跨字段守门 → ends 早于/等于 starts 放行 → 后端 400（EndsAt.After 失败）。
  assert.ok(
    validateAlertSilenceForm({ ...validBase(), starts_at_raw: '2026-01-01T09:00', ends_at_raw: '2026-01-01T08:00' }),
    'ends 早于 starts 应报错',
  );
  assert.ok(
    validateAlertSilenceForm({ ...validBase(), starts_at_raw: '2026-01-01T09:00', ends_at_raw: '2026-01-01T09:00' }),
    'ends == starts 应报错（严格大于）',
  );
});

test('TestValidate_RuleId', () => {
  assert.equal(validateAlertSilenceForm({ ...validBase(), rule_id_raw: '' }), null, '空 rule_id 合法（可选）');
  assert.equal(validateAlertSilenceForm({ ...validBase(), rule_id_raw: '42' }), null, '正整数 rule_id 合法');
  // 判别：漏 rule_id 守门 → 0/负/小数放行 → 后端 invalid。
  assert.ok(validateAlertSilenceForm({ ...validBase(), rule_id_raw: '0' }), 'rule_id 0 应报错');
  assert.ok(validateAlertSilenceForm({ ...validBase(), rule_id_raw: '-3' }), 'rule_id 负数应报错');
});

// ── 请求体构造：tenant 在 body、精确键集、时间规整、可选字段省略 ───────

test('TestBuildSilenceBody_Minimal', () => {
  // 最小（无可选）：精确键集（DisallowUnknownFields → 多/少键即语义错或 400）。
  const body = buildSilenceBody({ ...validBase(), reason: '  维护  ' }, 7);
  assert.deepEqual(Object.keys(body).sort(), ['ends_at', 'reason', 'starts_at', 'tenant_id'], '最小精确键集（无可选字段）');
  assert.equal(body.tenant_id, 7, 'tenant_id 放入 body');
  // 判别：再用不同 tenant 证 tenant_id 取自入参而非硬编码同值。
  assert.equal(buildSilenceBody(validBase(), 13).tenant_id, 13, 'tenant_id 取自入参（13）');
  assert.equal(body.reason, '维护', 'reason trim');
  // 判别：toRFC3339 真规整——datetime-local 形（无 Z 无秒）输出须带秒+Z，恒等透传会留下无 Z → red。
  assert.match(body.starts_at as string, /T\d{2}:\d{2}:\d{2}.*Z$/, 'starts_at 规整为带秒 RFC3339 UTC');
  assert.equal(new Date(body.starts_at as string).getTime(), new Date('2026-01-01T08:00').getTime(), 'starts_at 保持同一时刻');
});

test('TestBuildSilenceBody_FullScope', () => {
  // 全可选：键集含 rule_id/platform/group_id/region，且 rule_id 为【数字】非字符串。
  const body = buildSilenceBody(
    { ...validBase(), rule_id_raw: '42', platform: '  anthropic  ', group_id: 'g1', region: 'us' },
    1,
  );
  assert.deepEqual(
    Object.keys(body).sort(),
    ['ends_at', 'group_id', 'platform', 'reason', 'region', 'rule_id', 'starts_at', 'tenant_id'],
    '全作用域精确键集',
  );
  assert.equal(body.rule_id, 42, 'rule_id 解析为数字');
  assert.equal(body.platform, 'anthropic', 'platform trim');
  // 判别：空可选字段必须【省略键】（DisallowUnknownFields + 语义）——验最小体已覆盖省略，这里验非空带上。
  assert.equal('region' in buildSilenceBody(validBase(), 1), false, '空 region 省略键');
});

// ── adminAlertSilences.ts 接线：路径 + 动词 + builder + tenant 位置 ──────

test('TestEndpoints_PathsVerbsBuilder', () => {
  // list：GET 静态路径 + tenant_id 入 query（锚定 opts.tenant_id，非类型标注）。
  assert.match(bodyAfter('export function listAlertSilences'), /apiGet<AlertSilenceListResponse>\('\/v1\/admin\/alert-silences'/, 'list GET 路径');
  assert.match(bodyAfter('export function listAlertSilences'), /tenant_id: opts\.tenant_id/, 'list tenant 入 query（锚定接线）');
  // create：POST 静态路径（无 tenantQuery）+ buildSilenceBody(input, tenantId)（tenant 在 body）。
  assert.match(bodyAfter('export function createAlertSilence'), /apiPost<AlertSilence>\('\/v1\/admin\/alert-silences'/, 'create POST 路径');
  assert.match(bodyAfter('export function createAlertSilence'), /buildSilenceBody\(input, tenantId\)/, 'create 用 builder 且传 tenantId');
  assert.doesNotMatch(bodyAfter('export function createAlertSilence'), /tenantQuery/, 'create URL 不带 tenantQuery（tenant 在 body）');
  // delete：adminDelete + ${id}${tenantQuery}。
  assert.match(bodyAfter('export function deleteAlertSilence'), /adminDelete/, 'delete 用 adminDelete');
  assert.match(bodyAfter('export function deleteAlertSilence'), /\/v1\/admin\/alert-silences\/\$\{id\}\$\{tenantQuery/, 'delete 路径带 id+tenantQuery');
});

test('TestHelpers_DeleteVerb', () => {
  assert.match(bodyAfter('async function adminDelete'), /method:\s*'DELETE'/, 'adminDelete 发 DELETE');
});
