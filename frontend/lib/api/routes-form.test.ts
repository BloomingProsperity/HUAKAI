// 路由规则(routes)数据层切片测试。
// 纯逻辑(model_pattern 校验镜像后端/必填校验/精确 key-set 构造/错误码映射)+ adminRoutes.ts 接线断言
// (5 端点 URL + 方法 + validate-first + builder 使用)。每条断言一句话说清抓的回归; 均经变异实测转红。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  buildCreateRouteBody,
  buildSetEnabledBody,
  buildUpdateRouteBody,
  routeErrorMessage,
  validateModelPattern,
  validateRouteInput,
  validateRouteUpdateInput,
  type RouteInput,
  type RouteUpdateInput,
} from './routes-form.ts';

const ROOT = process.cwd();
const clientSrc = readFileSync(join(ROOT, 'lib/api/adminRoutes.ts'), 'utf8');

// 取某函数段: marker 起到【下一个顶层声明】前止。端点断言锚定引号/${}定界符保持判别性。
function bodyAfter(marker: string): string {
  const start = clientSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = clientSrc.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |async function |function |export interface |export const |const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : clientSrc.length;
  return clientSrc.slice(start, end);
}

function baseInput(): RouteInput {
  return { name: 'premium-claude', user_group_match: 'premium', model_pattern_match: 'claude-*', pool_group_id: 9, match_priority: 5 };
}

// ── validateModelPattern: 镜像后端 exact/prefix*, 拒中段多通配 ───────────
test('TestValidateModelPattern', () => {
  // 判别: 合法形态须放行(空/*/精确/prefix*)。
  for (const ok of ['', '*', 'claude-*', 'gpt-4o', 'a-b-c']) {
    assert.equal(validateModelPattern(ok), null, `合法 pattern ${ok} 应通过`);
  }
  // 判别: 中段/多通配须拒 —— 漏校验 → 'a*b' 落库被后端 gate 当精确串静默失配。
  // 每个非法值标注违反哪条规则(后端 validate.go: 仅 ''/'*'/精确/末尾单通配合法):
  const bad = [
    'a*b', // 中段通配(单 '*' 但不在末尾)
    '*x', // 前导通配(单 '*' 在开头)
    'a**', // 多通配(两个 '*')
    'a*b*c', // 多通配
    'claude-*-preview', // 中段通配
  ];
  for (const p of bad) {
    assert.ok(validateModelPattern(p), `非法 pattern ${p} 应拒`);
  }
});

// ── validateRouteUpdateInput: 强制 match_priority 存在(防 PUT 静默重置 footgun) ──
test('TestValidateRouteUpdateInput', () => {
  // 判别(footgun): update 缺 match_priority(经 cast 绕过类型)→ 必拒, 否则发请求后后端重置为 100。
  // mutation: updateRoute 改回用宽松 validateRouteInput → 缺 match_priority 放行 → 此断言红。
  const noPrio = { name: 'r', user_group_match: 'premium', model_pattern_match: 'claude-*', pool_group_id: 9 } as unknown as Parameters<typeof validateRouteUpdateInput>[0];
  assert.ok(validateRouteUpdateInput(noPrio), '缺 match_priority 的 update 应拒(防静默重置)');
  // 判别: 给了 match_priority 且其它合法 → 通过。
  assert.equal(
    validateRouteUpdateInput({ name: 'r', user_group_match: 'premium', model_pattern_match: 'claude-*', pool_group_id: 9, match_priority: 5 }),
    null,
    '带 match_priority 的合法 update 应通过',
  );
  // 判别: 仍复用 validateRouteInput 的形态校验(非法 pattern 即便带 match_priority 也拒)。
  assert.ok(
    validateRouteUpdateInput({ name: 'r', user_group_match: 'premium', model_pattern_match: 'a*b', pool_group_id: 9, match_priority: 5 }),
    'update 非法 pattern 仍拒',
  );
});

// ── validateRouteInput: 必填 + 形态 ─────────────────────────────────────
test('TestValidateRouteInput', () => {
  assert.equal(validateRouteInput(baseInput()), null, '合法输入应通过');
  // 判别: 各必填缺失/越界须拦在发请求前。
  assert.ok(validateRouteInput({ ...baseInput(), name: '  ' }), '空 name 应拒');
  assert.ok(validateRouteInput({ ...baseInput(), user_group_match: '' }), '空 user_group 应拒');
  assert.ok(validateRouteInput({ ...baseInput(), pool_group_id: 0 }), 'pool_group_id=0 应拒');
  assert.ok(validateRouteInput({ ...baseInput(), match_priority: -1 }), '负 match_priority 应拒');
  // 判别: 非法 pattern 经 validateRouteInput 也须拒(复用 validateModelPattern)。
  assert.ok(validateRouteInput({ ...baseInput(), model_pattern_match: 'a*b' }), '中段通配应拒');
  // 判别: model_pattern_match 省略(undefined)视作全匹配 → 合法。
  const noPattern: RouteInput = { name: 'r', user_group_match: 'premium', pool_group_id: 9 };
  assert.equal(validateRouteInput(noPattern), null, '省略 pattern 视作全匹配应通过');
});

// ── buildCreateRouteBody: 精确 key-set, tenant_id 在 body, match_priority 条件附带 ──
test('TestBuildCreateRouteBody', () => {
  const body = buildCreateRouteBody(7, baseInput());
  // 判别: tenant_id 须原样进 body(创建语义); fixture 用非 1 值(7)避免硬编码 1 假绿。
  assert.equal(body.tenant_id, 7, 'tenant_id 进 body');
  assert.equal(body.model_pattern_match, 'claude-*', 'pattern 透传');
  assert.equal(body.match_priority, 5, '给了 match_priority 须带');
  // 判别: 精确 key-set —— 多余/缺失键都会被后端 DisallowUnknownFields/校验打回。
  assert.deepEqual(
    Object.keys(body).sort(),
    ['match_priority', 'model_pattern_match', 'name', 'pool_group_id', 'tenant_id', 'user_group_match'],
    'create key-set 精确',
  );
  // 判别: 省略 match_priority → 不出现该键(交后端默认 100), 而非塞 undefined。
  const noPrio = buildCreateRouteBody(7, { name: 'r', user_group_match: 'premium', pool_group_id: 9 });
  assert.ok(!('match_priority' in noPrio), '省略时 match_priority 键不出现');
  assert.equal(noPrio.model_pattern_match, '', '省略 pattern → 默认空串(全匹配)');
});

// ── buildUpdateRouteBody: 无 tenant_id(防走私) + match_priority 永远带(防重置) ──
test('TestBuildUpdateRouteBody', () => {
  const input: RouteUpdateInput = { name: 'r2', user_group_match: 'vip', model_pattern_match: 'gpt-*', pool_group_id: 11, match_priority: 3 };
  const body = buildUpdateRouteBody(input);
  // 判别(安全): update body 绝不含 tenant_id —— 否则可经更新跨租户搬移(后端 DisallowUnknownFields 拒,
  // 但客户端先就不该构造它)。mutation: builder 加 tenant_id → 此断言红。
  assert.ok(!('tenant_id' in body), 'update body 不含 tenant_id(防跨租户走私)');
  // 判别(footgun): match_priority 永远显式带 —— PUT 全替换省略它后端会静默重置为 100。
  // mutation: builder 改成条件附带/omit → 'match_priority' in body 为假 → 红。
  assert.ok('match_priority' in body, 'update body 永远含 match_priority(防 PUT 静默重置)');
  assert.equal(body.match_priority, 3, 'match_priority 透传原值');
  // 判别: 精确 key-set(5 键, 无 tenant_id)。
  assert.deepEqual(
    Object.keys(body).sort(),
    ['match_priority', 'model_pattern_match', 'name', 'pool_group_id', 'user_group_match'],
    'update key-set 精确(无 tenant_id)',
  );
});

// ── buildSetEnabledBody: 精确 key-set { enabled } 布尔保真, 无 tenant_id(防走私) ──
test('TestBuildSetEnabledBody', () => {
  // 判别(安全): 启停 body 绝不含 tenant_id —— 否则可经此面跨租户搬移(后端 DisallowUnknownFields 拒,
  // 但客户端先就不该构造它)。mutation: builder 加 tenant_id 等键 → key-set 断言红。
  const on = buildSetEnabledBody(true);
  assert.deepEqual(Object.keys(on), ['enabled'], 'set-enabled key-set 精确为单键 enabled(无 tenant_id)');
  // 判别: 布尔保真, 不被字符串化/强转。fixture 用 true 与 false 两值, 防硬编码某一值假绿。
  assert.equal(on.enabled, true, 'enabled=true 透传为布尔 true');
  assert.equal(buildSetEnabledBody(false).enabled, false, 'enabled=false 透传为布尔 false');
});

// ── routeErrorMessage: 错误码映射 ───────────────────────────────────────
test('TestRouteErrorMessage', () => {
  // 判别: 已知码映射到专属文案(漏映射 → 回退泛文案带 code, 断言红)。
  assert.match(routeErrorMessage('route_name_conflict'), /同名/, '重名码有专属文案');
  assert.match(routeErrorMessage('pool_group_not_found'), /pool_group/, 'pool 码有专属文案');
  assert.match(routeErrorMessage('invalid_model_pattern'), /通配/, 'pattern 码有专属文案');
  // 判别: 后端全部实际错误码都须有专属映射(删任一映射 → 该码回退含 code 的泛文案 → 红)。
  // 覆盖 routeAdminWriteRouteError + admin 门的全部码, 防漏映射时 UI 只显示码。
  for (const code of [
    'invalid_route_request', 'route_not_found', 'route_admin_backend_error', 'invalid_route_id',
    'tenant_id_required', 'admin_unauthorized', 'admin_forbidden', 'gateway_not_configured',
  ]) {
    const msg = routeErrorMessage(code);
    assert.ok(!msg.includes(code), `已知码 ${code} 须有专属文案(不含原始码)`);
  }
  // 判别: 未知码回退泛文案且带 code(不抛/不空)。
  assert.match(routeErrorMessage('weird_code'), /weird_code/, '未知码回退带 code');
});

// ── adminRoutes.ts 接线: 5 端点路径 + 方法 + validate-first + builder ──
test('TestEndpoints_Paths', () => {
  // 判别: 路径写错 → 打到错误后端路由。锚定引号/${}定界符避免尾部 typo 非判别。
  assert.match(bodyAfter('const BASE'), /'\/v1\/admin\/routes'/, "BASE = /v1/admin/routes");
  assert.match(bodyAfter('export function createRoute'), /apiPost<RouteResponse>\(BASE,/, 'create POST 到 BASE');
  assert.match(bodyAfter('export function listRoutes'), /apiGet<RouteListResponse>\(BASE,\s*\{\s*tenant_id/, 'list GET BASE + tenant_id');
  assert.match(bodyAfter('export function getRoute'), /\$\{BASE\}\/\$\{id\}\$\{tenantQuery\(tenantId\)\}/, 'get 路径 BASE/{id}?tenant');
  assert.match(bodyAfter('export function updateRoute'), /\$\{BASE\}\/\$\{id\}\$\{tenantQuery\(tenantId\)\}/, 'update 路径 BASE/{id}?tenant');
  assert.match(bodyAfter('export function deleteRoute'), /\$\{BASE\}\/\$\{id\}\$\{tenantQuery\(tenantId\)\}/, 'delete 路径 BASE/{id}?tenant');
  // 判别: 启停打到子资源 /{id}/enabled?tenant —— 路径少了 /enabled 段就会命中 PUT /{id} 全替换端点(语义错且会
  // 因缺其它必填字段失败)。锚定 /enabled${tenantQuery 定界, 防尾部 typo 非判别。
  assert.match(bodyAfter('export function setRouteEnabled'), /\$\{BASE\}\/\$\{id\}\/enabled\$\{tenantQuery\(tenantId\)\}/, 'set-enabled 路径 BASE/{id}/enabled?tenant');
});

test('TestEndpoints_VerbsAndBuilders', () => {
  // 判别: 方法用错 → 后端 405/路由错。read 必 GET, update 必 PUT, delete 必 DELETE。
  // get/list 显式断 apiGet —— 否则 getRoute 被误改 adminPut/adminDelete 路径测试仍绿(路径不变), 漏网。
  assert.match(bodyAfter('export function getRoute'), /apiGet<RouteResponse>/, 'get 用 GET');
  assert.match(bodyAfter('export function listRoutes'), /apiGet<RouteListResponse>/, 'list 用 GET');
  assert.match(bodyAfter('export function updateRoute'), /adminPut</, 'update 用 PUT');
  assert.match(bodyAfter('export function deleteRoute'), /adminDelete</, 'delete 用 DELETE');
  assert.match(bodyAfter('async function adminPut'), /method:\s*'PUT'/, 'adminPut 发 PUT');
  assert.match(bodyAfter('async function adminDelete'), /method:\s*'DELETE'/, 'adminDelete 发 DELETE');
  // 判别: create/update 须 validate-first(漏则非法输入直发后端) + 用 builder(漏则 key-set 失控)。
  // update 必须用 validateRouteUpdateInput(强制 match_priority 存在), 不是宽松的 validateRouteInput。
  assert.match(bodyAfter('export function createRoute'), /validateRouteInput\(input\)/, 'create validate-first');
  assert.match(bodyAfter('export function createRoute'), /buildCreateRouteBody\(tenantId, input\)/, 'create 用 builder');
  assert.match(bodyAfter('export function updateRoute'), /validateRouteUpdateInput\(input\)/, 'update 用 validateRouteUpdateInput(强制 match_priority)');
  assert.match(bodyAfter('export function updateRoute'), /buildUpdateRouteBody\(input\)/, 'update 用 builder');
  // 判别: 启停用 PUT(adminPut) + buildSetEnabledBody(漏 builder 则 key-set 失控可能走私 tenant)。
  assert.match(bodyAfter('export function setRouteEnabled'), /adminPut</, 'set-enabled 用 PUT');
  assert.match(bodyAfter('export function setRouteEnabled'), /buildSetEnabledBody\(enabled\)/, 'set-enabled 用 builder');
  // 判别: 便捷封装 enable/disable 委托 setRouteEnabled 且方向正确(true/false 写反 → 启停颠倒)。
  assert.match(bodyAfter('export function enableRoute'), /setRouteEnabled\(id, tenantId, true\)/, 'enableRoute 委托 setRouteEnabled(…true)');
  assert.match(bodyAfter('export function disableRoute'), /setRouteEnabled\(id, tenantId, false\)/, 'disableRoute 委托 setRouteEnabled(…false)');
});
