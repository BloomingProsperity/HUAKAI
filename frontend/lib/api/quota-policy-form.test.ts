// 配额策略 CRUD 切片强测试。
// 纯逻辑（校验/请求体构造，直接 strip-types 单测）+ adminQuotaPolicies.ts 接线断言（端点路径 + PUT/DELETE 方法 + tenantQuery）。
// 每条断言可一句话说出抓的回归；均经变异实测转红再还原（CLAUDE.md §14）。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  buildQuotaPolicyBody,
  validateQuotaPolicyForm,
  type QuotaPolicyFormInput,
} from './quota-policy-form.ts';

const ROOT = process.cwd();
const adminSrc = readFileSync(join(ROOT, 'lib/api/adminQuotaPolicies.ts'), 'utf8');

// 取某函数段：marker 起到【下一个顶层声明】前止。不用首个 \n}（这些函数的 opts 参数是多行对象类型字面量，
// 首个 \n} 会落在参数块而非函数体）。端点断言均锚定引号/${} 定界符，故即便圈进下一函数前置注释也保持判别性。
function bodyAfter(marker: string): string {
  const start = adminSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = adminSrc.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |async function |function |export interface |export const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : adminSrc.length;
  return adminSrc.slice(start, end);
}

// 合法基线：fixed 窗口 + cost_usd + 必填齐全。
function validBase(): QuotaPolicyFormInput {
  return {
    scope_kind: 'user',
    scope_id: '1024',
    metric: 'cost_usd',
    window_kind: 'fixed',
    window_seconds: '3600',
    limit_value: '10.5',
  };
}

// ── 校验：枚举守门 ──────────────────────────────────────────────────────

test('TestValidate_EnumGuards', () => {
  assert.equal(validateQuotaPolicyForm(validBase()), null, '合法基线应通过');
  // 判别：漏 scope_kind 白名单 → 任意串放行 → 后端 CHECK 503，应前置 400。
  assert.ok(validateQuotaPolicyForm({ ...validBase(), scope_kind: 'bogus' }), '非法 scope_kind 应报错');
  // 判别：漏 metric 白名单。
  assert.ok(validateQuotaPolicyForm({ ...validBase(), metric: 'bogus' }), '非法 metric 应报错');
  // 判别：漏 window_kind 白名单。
  assert.ok(validateQuotaPolicyForm({ ...validBase(), window_kind: 'bogus' }), '非法 window_kind 应报错');
  // 判别：漏 mode 白名单。
  assert.ok(validateQuotaPolicyForm({ ...validBase(), mode: 'bogus' }), '非法 mode 应报错');
});

// ── 校验：scope_id 必填 + ≤255 ──────────────────────────────────────────

test('TestValidate_ScopeID', () => {
  // 判别：漏必填 → 空 scope_id 放行 → 后端 400 invalid_scope_id。
  assert.ok(validateQuotaPolicyForm({ ...validBase(), scope_id: '' }), '空 scope_id 应报错');
  assert.ok(validateQuotaPolicyForm({ ...validBase(), scope_id: '   ' }), '纯空白 scope_id 应报错');
  // 判别：漏 ≤255 → 超长放行。
  assert.ok(validateQuotaPolicyForm({ ...validBase(), scope_id: 'x'.repeat(256) }), '超 255 应报错');
  assert.equal(validateQuotaPolicyForm({ ...validBase(), scope_id: '*' }), null, "global 用 '*' 应通过");
});

// ── 校验：fixed 窗口需 window_seconds>0 ────────────────────────────────

test('TestValidate_FixedWindowNeedsSeconds', () => {
  // 判别：漏「fixed 必填 >0」→ fixed 缺 window_seconds 放行 → 后端 400。
  assert.ok(validateQuotaPolicyForm({ ...validBase(), window_seconds: '' }), 'fixed 缺 window_seconds 应报错');
  assert.ok(validateQuotaPolicyForm({ ...validBase(), window_seconds: '0' }), 'fixed window_seconds=0 应报错');
  // 判别：漏 ≥0 → 负数放行。
  assert.ok(validateQuotaPolicyForm({ ...validBase(), window_kind: 'none', window_seconds: '-1' }), '负 window_seconds 应报错');
  // 非 fixed 窗口可省 window_seconds。
  assert.equal(validateQuotaPolicyForm({ ...validBase(), window_kind: 'calendar_day', window_seconds: '' }), null, 'calendar_day 可省 window_seconds');
});

// ── 校验：limit/burst 非负十进制 ───────────────────────────────────────

test('TestValidate_LimitAndBurst', () => {
  // 判别：漏 limit 必填 → 空放行。
  assert.ok(validateQuotaPolicyForm({ ...validBase(), limit_value: '' }), '空 limit_value 应报错');
  // 判别：漏非负十进制守门 → 负/非数放行。
  assert.ok(validateQuotaPolicyForm({ ...validBase(), limit_value: '-5' }), '负 limit 应报错');
  assert.ok(validateQuotaPolicyForm({ ...validBase(), limit_value: 'abc' }), '非数 limit 应报错');
  // 判别：burst 设了但非法 → 应报错；空 burst 合法（缺省 0）。
  assert.ok(validateQuotaPolicyForm({ ...validBase(), burst_value: '-1' }), '负 burst 应报错');
  assert.equal(validateQuotaPolicyForm({ ...validBase(), burst_value: '' }), null, '空 burst 合法');
  assert.equal(validateQuotaPolicyForm({ ...validBase(), burst_value: '2.5' }), null, '正 burst 合法');
});

// ── 校验：valid_until 必须晚于 valid_from ──────────────────────────────

test('TestValidate_ValidityRange', () => {
  const from = '2026-01-01T00:00:00Z';
  // 判别：漏「until>from」→ until≤from 放行 → 后端 400 invalid_validity_range。
  assert.ok(
    validateQuotaPolicyForm({ ...validBase(), valid_from: from, valid_until: '2025-12-31T00:00:00Z' }),
    'until 早于 from 应报错',
  );
  assert.ok(
    validateQuotaPolicyForm({ ...validBase(), valid_from: from, valid_until: from }),
    'until 等于 from 应报错',
  );
  assert.equal(
    validateQuotaPolicyForm({ ...validBase(), valid_from: from, valid_until: '2026-02-01T00:00:00Z' }),
    null,
    'until 晚于 from 应通过',
  );
});

// ── 请求体构造：必填恒带 + 可选省略语义 ────────────────────────────────

test('TestBuildBody_RequiredAlwaysOptionalOmitted', () => {
  const min = buildQuotaPolicyBody({
    scope_kind: 'global',
    scope_id: '*',
    metric: 'requests',
    window_kind: 'fixed',
    window_seconds: '60',
    limit_value: '100',
  });
  // 判别：漏任一必填 → 后端 400/写失败。
  assert.equal(min.scope_kind, 'global');
  assert.equal(min.scope_id, '*');
  assert.equal(min.metric, 'requests');
  assert.equal(min.window_kind, 'fixed');
  assert.equal(min.window_seconds, 60, 'window_seconds 转数字带上');
  assert.equal(min.limit_value, '100');
  // 判别：可选未给却被带上 → 与「省略=后端默认」语义偏离。
  assert.ok(!('burst_value' in min), '未给 burst 不应带');
  assert.ok(!('mode' in min), '未给 mode 不应带');
  assert.ok(!('priority' in min), '未给 priority 不应带');
  assert.ok(!('valid_until' in min), '未给 valid_until 不应带');
  assert.ok(!('reason' in min), '未给 reason 不应带');
});

test('TestBuildBody_OptionalIncludedWhenSet', () => {
  const full = buildQuotaPolicyBody({
    scope_kind: 'user',
    scope_id: '7',
    metric: 'cost_usd',
    window_kind: 'calendar_day',
    window_seconds: '',
    limit_value: '50',
    burst_value: '5',
    mode: 'observe',
    priority: '10',
    enabled: false,
    valid_from: '2026-01-01T00:00:00Z',
    valid_until: '2026-02-01T00:00:00Z',
    reason: '季节限流',
  });
  assert.equal(full.burst_value, '5', '带 burst');
  assert.equal(full.mode, 'observe', '带 mode');
  assert.equal(full.priority, 10, 'priority 转数字带上');
  // 判别：enabled=false 须带（!= null 语义），漏带则无法显式停用。
  assert.equal(full.enabled, false, 'enabled=false 须带');
  assert.equal(full.valid_until, '2026-02-01T00:00:00Z', '带 valid_until');
  assert.equal(full.reason, '季节限流', '带 reason');
  // 非 fixed 且空 window_seconds → 不带。
  assert.ok(!('window_seconds' in full), 'calendar_day 空 window_seconds 不带');
});

// ── adminQuotaPolicies.ts 接线：端点路径 + 方法 + tenantQuery ───────────

test('TestEndpoints_PathsAndMethods', () => {
  // 判别：端点路径写错 → 打到错误后端路由 → red。路径锚定闭合定界符避免尾部追加 typo 非判别。
  assert.match(bodyAfter('export function listQuotaPolicies'), /'\/admin\/v1\/quota-policies'/, 'list 路径');
  assert.match(bodyAfter('export function getQuotaPolicy'), /\/admin\/v1\/quota-policies\/\$\{id\}`/, 'get 路径');
  assert.match(bodyAfter('export function createQuotaPolicy'), /\/admin\/v1\/quota-policies\$\{tenantQuery/, 'create 路径');
  assert.match(bodyAfter('export function updateQuotaPolicy'), /\/admin\/v1\/quota-policies\/\$\{id\}\$\{tenantQuery/, 'update 路径');
  assert.match(bodyAfter('export function deleteQuotaPolicy'), /\/admin\/v1\/quota-policies\/\$\{id\}\$\{tenantQuery/, 'delete 路径');
});

test('TestEndpoints_VerbsAndBuilderAndTenant', () => {
  // 判别：update 误用 apiPost(应 PUT)/ delete 误用别的动词 → 后端 405 → red。
  assert.match(bodyAfter('export function updateQuotaPolicy'), /adminPut/, 'update 用 PUT 助手');
  assert.match(bodyAfter('export function deleteQuotaPolicy'), /adminDelete/, 'delete 用 DELETE 助手');
  // 判别：create/update 漏用 builder → 绕过请求体校验/构造 → red。
  assert.match(bodyAfter('export function createQuotaPolicy'), /buildQuotaPolicyBody/, 'create 用 builder');
  assert.match(bodyAfter('export function updateQuotaPolicy'), /buildQuotaPolicyBody/, 'update 用 builder');
  // 判别：platform_admin 必带 tenant_id；写操作漏 tenantQuery → 后端 400 tenant_id_required → red。
  assert.match(bodyAfter('export function createQuotaPolicy'), /tenantQuery\(tenantId\)/, 'create 带 tenantQuery');
  assert.match(bodyAfter('export function updateQuotaPolicy'), /tenantQuery\(tenantId\)/, 'update 带 tenantQuery');
  assert.match(bodyAfter('export function deleteQuotaPolicy'), /tenantQuery\(tenantId\)/, 'delete 带 tenantQuery');
});

// 适配后端「PUT/DELETE 助手」方法字面量本身也守一道（防 adminPut/adminDelete 被改错动词）。
test('TestHelpers_HttpVerbs', () => {
  assert.match(bodyAfter('async function adminPut'), /method:\s*'PUT'/, 'adminPut 发 PUT');
  assert.match(bodyAfter('async function adminDelete'), /method:\s*'DELETE'/, 'adminDelete 发 DELETE');
});
