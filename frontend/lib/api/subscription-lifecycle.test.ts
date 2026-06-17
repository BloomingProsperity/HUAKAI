// 订阅生命周期 admin 写操作切片强测试。
// 纯逻辑（校验/请求体构造，直接 strip-types 单测）+ adminOperations.ts 接线断言（端点 + X-Request-Id 幂等头）。
// 每条断言可一句话说出它抓的回归；均经变异实测转红再还原（CLAUDE.md §14）。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  buildBulkAssignBody,
  buildChangePlanBody,
  buildExtendBody,
  buildRevokeBody,
  buildSubscriptionVoucherBody,
  newRequestId,
  parseBulkUserIds,
  validateChangePlan,
  validateExtendInput,
  validateRevokeReason,
} from './subscription-lifecycle.ts';

const ROOT = process.cwd();
const adminSrc = readFileSync(join(ROOT, 'lib/api/adminOperations.ts'), 'utf8');

// 取某段（marker 起到首个 \n} 止）。收到闭合括号即止，避免把下一函数前置注释误圈进来 → 保持判别性。
function bodyAfter(marker: string): string {
  const start = adminSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const end = adminSrc.indexOf('\n}', start);
  return adminSrc.slice(start, end > start ? end + 2 : undefined);
}

// ── extend：days>0 XOR until（镜像后端 hasDays==hasUntil→错）────────────

test('TestValidateExtendInput_ExactlyOne', () => {
  // 判别：漏「恰好一个」语义 → both/neither 不报错 → red。
  assert.ok(validateExtendInput({ days: 5, until: '2026-01-01T00:00:00Z' }), '同时给天数与到期应报错');
  assert.ok(validateExtendInput({}), '都不给应报错');
  // 判别：漏 days>0 守门 → days=0/负 当作有效 → red（这俩应落入 neither→报错）。
  assert.ok(validateExtendInput({ days: 0 }), 'days=0 非有效天数，应报错');
  assert.ok(validateExtendInput({ days: -3 }), '负天数后端不收，应报错');
  // 合法：单边。
  assert.equal(validateExtendInput({ days: 5 }), null, '仅天数(>0)应通过');
  assert.equal(validateExtendInput({ until: '2026-01-01T00:00:00Z' }), null, '仅到期应通过');
});

test('TestBuildExtendBody_OnlyValidFields', () => {
  // 判别：漏 tenant_id → 后端 ErrInvalidInput；漏 >0 守门 → days=0 被发出。
  assert.deepEqual(buildExtendBody(7, { days: 5 }), { tenant_id: 7, days: 5 }, '天数模式带 tenant_id+days');
  assert.deepEqual(
    buildExtendBody(7, { until: '2026-01-01T00:00:00Z' }),
    { tenant_id: 7, until: '2026-01-01T00:00:00Z' },
    '到期模式带 tenant_id+until',
  );
  assert.deepEqual(buildExtendBody(7, { days: 0 }), { tenant_id: 7 }, 'days=0 不应进入请求体');
});

// ── revoke：reason 必填 ────────────────────────────────────────────────

test('TestValidateRevokeReason_Required', () => {
  // 判别：漏 trim/空校验 → 空原因放行 → 后端 ErrInvalidInput → red。
  assert.ok(validateRevokeReason(''), '空原因应报错');
  assert.ok(validateRevokeReason('   '), '纯空白原因应报错');
  assert.equal(validateRevokeReason('滥用'), null, '有原因应通过');
});

test('TestBuildRevokeBody_TrimsReason', () => {
  // 判别：漏 trim → reason 带前后空白发出。
  assert.deepEqual(buildRevokeBody(3, '  abuse  '), { tenant_id: 3, reason: 'abuse' }, 'reason 去空白');
});

// ── bulkAssign：多用户 ID 解析 ──────────────────────────────────────────

test('TestParseBulkUserIds_Discriminating', () => {
  // 合法：逗号/空白/换行混合分隔。
  assert.deepEqual(parseBulkUserIds('1, 2, 3'), { ids: [1, 2, 3], error: null }, '逗号分隔');
  assert.deepEqual(parseBulkUserIds(' 7\n8 9'), { ids: [7, 8, 9], error: null }, '空白/换行分隔');
  // 判别：漏去重 → 重复 ID 保留 → red。
  assert.deepEqual(parseBulkUserIds('1,1,2'), { ids: [1, 2], error: null }, '去重保序');
  // 判别：漏正整数守门 → 非法 token / 0 / 负数 放行 → red。
  assert.ok(parseBulkUserIds('1, abc').error, '非数字 token 应报错');
  assert.ok(parseBulkUserIds('0').error, '0 非法（n<=0）应报错');
  assert.ok(parseBulkUserIds('-3').error, '负数应报错');
  // 判别：漏空列表守门 → 空输入返回 ids:[] 无错 → red。
  assert.ok(parseBulkUserIds('   ').error, '空输入应报错');
});

test('TestBuildBulkAssignBody', () => {
  assert.deepEqual(
    buildBulkAssignBody(2, [5, 6], 9),
    { tenant_id: 2, user_ids: [5, 6], plan_id: 9 },
    'bulk 请求体三字段',
  );
});

// ── change-plan：new_plan_id>0 + allow_downgrade 省略语义 ───────────────

test('TestValidateChangePlan_Positive', () => {
  // 判别：漏 >0/整数守门 → 0/负/小数放行 → red。
  assert.ok(validateChangePlan(0), '0 应报错');
  assert.ok(validateChangePlan(-1), '负应报错');
  assert.ok(validateChangePlan(1.5), '非整数应报错');
  assert.equal(validateChangePlan(5), null, '正整数应通过');
});

test('TestBuildChangePlanBody_AllowDowngradeOmittedWhenFalse', () => {
  // 判别：allow_downgrade 总是带 → false 时多发字段，与「省略=不降级」语义偏离 → red。
  assert.deepEqual(buildChangePlanBody(1, 9), { tenant_id: 1, new_plan_id: 9 }, '省略 allow_downgrade');
  assert.deepEqual(buildChangePlanBody(1, 9, false), { tenant_id: 1, new_plan_id: 9 }, 'false 不带该字段');
  assert.deepEqual(
    buildChangePlanBody(1, 9, true),
    { tenant_id: 1, new_plan_id: 9, allow_downgrade: true },
    'true 才带 allow_downgrade',
  );
});

// ── 订阅券请求体 ───────────────────────────────────────────────────────

test('TestBuildSubscriptionVoucherBody_RequiredAndOptional', () => {
  const min = buildSubscriptionVoucherBody({
    tenant_id: 1,
    plan_id: 2,
    amount_cents: 999,
    valid_from: '2026-01-01T00:00:00Z',
    valid_until: '2026-02-01T00:00:00Z',
  });
  // 判别：漏任一必填 → 后端建券失败 → red。
  assert.deepEqual(
    min,
    { tenant_id: 1, plan_id: 2, amount_cents: 999, valid_from: '2026-01-01T00:00:00Z', valid_until: '2026-02-01T00:00:00Z' },
    '最小体仅五个必填字段',
  );
  const full = buildSubscriptionVoucherBody({
    tenant_id: 1,
    plan_id: 2,
    amount_cents: 999,
    valid_from: '2026-01-01T00:00:00Z',
    valid_until: '2026-02-01T00:00:00Z',
    code: 'PROMO',
    currency_code: 'USD',
    max_redemptions: 5,
    single_use_per_user: false,
    eligible_user_id: 42,
  });
  // 判别：可选字段漏带或空值未过滤 → red。single_use_per_user 即便 false 也须带（!= null 语义）。
  assert.equal(full.code, 'PROMO', '带 code');
  assert.equal(full.max_redemptions, 5, '带 max_redemptions');
  assert.equal(full.single_use_per_user, false, 'false 也须带 single_use_per_user');
  assert.equal(full.eligible_user_id, 42, '带 eligible_user_id');
});

// ── 幂等键 ─────────────────────────────────────────────────────────────

test('TestNewRequestId_NonEmptyUnique', () => {
  const a = newRequestId();
  const b = newRequestId();
  // 判别：返回常量/空串 → 幂等键无法区分两次提交 → red。
  assert.ok(a.length > 0, '幂等键非空');
  assert.notEqual(a, b, '两次生成应不同');
});

// ── adminOperations.ts 接线：端点路径 + X-Request-Id 幂等头 ──────────────

test('TestAdminPostIdem_SetsRequestIdHeader', () => {
  const body = bodyAfter('async function adminPostIdem');
  // 判别：漏设 X-Request-Id 头 → 幂等失效（后端拿不到 RequestID）→ red。
  assert.match(body, /headers\['X-Request-Id'\]\s*=\s*requestId/, '把 requestId 写入 X-Request-Id 头');
  assert.match(body, /method:\s*'POST'/, 'POST 方法');
});

test('TestLifecycleEndpoints_Wiring', () => {
  // 判别：任一端点路径写错 → 打到错误后端路由 → red。
  // 路径尾部锚定闭合定界符（${id} 路径收反引号、静态路径收单引号），否则 /cancel/ 会误配 /cancelX → 非判别。
  assert.match(bodyAfter('export function cancelSubscription'), /assignments\/\$\{id\}\/cancel`/, 'cancel 路径');
  assert.match(bodyAfter('export function extendSubscription'), /assignments\/\$\{id\}\/extend`/, 'extend 路径');
  assert.match(bodyAfter('export function resetSubscriptionQuota'), /assignments\/\$\{id\}\/reset-quota`/, 'reset-quota 路径');
  assert.match(bodyAfter('export function changeSubscriptionPlan'), /assignments\/\$\{id\}\/change-plan`/, 'change-plan 路径');
  assert.match(bodyAfter('export function revokeSubscription'), /assignments\/\$\{id\}\/revoke`/, 'revoke 路径');
  assert.match(bodyAfter('export function bulkAssignSubscription'), /assignments\/bulk'/, 'bulk 路径');
  assert.match(bodyAfter('export function createSubscriptionVoucher'), /subscriptions\/vouchers'/, '订阅券路径');
});

test('TestLifecycleEndpoints_UseIdemHelperAndBuilders', () => {
  // 判别：改用普通 apiPost（无幂等头）或漏用 builder（请求体校验绕过）→ red。
  assert.match(bodyAfter('export function cancelSubscription'), /adminPostIdem/, 'cancel 走幂等 POST');
  assert.match(bodyAfter('export function extendSubscription'), /buildExtendBody/, 'extend 用 builder');
  assert.match(bodyAfter('export function revokeSubscription'), /buildRevokeBody/, 'revoke 用 builder');
  assert.match(bodyAfter('export function changeSubscriptionPlan'), /buildChangePlanBody/, 'change-plan 用 builder');
  assert.match(bodyAfter('export function bulkAssignSubscription'), /buildBulkAssignBody/, 'bulk 用 builder');
  assert.match(bodyAfter('export function createSubscriptionVoucher'), /buildSubscriptionVoucherBody/, '订阅券用 builder');
});
