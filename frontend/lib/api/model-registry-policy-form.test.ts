// 租户目录继承策略数据层切片测试。纯逻辑(validate/精确 key-set/错误码映射)+ adminModelRegistryPolicy.ts 接线断言
// (2 端点 URL + 方法 + validate-first + builder)。每条断言一句话说清抓的回归; 均经变异实测转红。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  buildTenantPolicyBody,
  tenantPolicyErrorMessage,
  validateSetTenantPolicy,
} from './model-registry-policy-form.ts';

const ROOT = process.cwd();
const clientSrc = readFileSync(join(ROOT, 'lib/api/adminModelRegistryPolicy.ts'), 'utf8');

function bodyAfter(marker: string): string {
  const start = clientSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = clientSrc.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |async function |function |export interface |export const |const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : clientSrc.length;
  return clientSrc.slice(start, end);
}

// ── validateSetTenantPolicy ──────────────────────────────────────────────
test('TestValidateSetTenantPolicy', () => {
  assert.equal(validateSetTenantPolicy(7), null, '正 tenant_id 通过');
  // 判别: 非正 tenant_id 须拒(后端 tenant_id query 必填且 >0)。
  assert.ok(validateSetTenantPolicy(0), 'tenant_id=0 应拒');
  assert.ok(validateSetTenantPolicy(-1), '负 tenant_id 应拒');
});

// ── buildTenantPolicyBody: 精确 key-set, 无 tenant_id, inherit 永远带 ──────
test('TestBuildTenantPolicyBody', () => {
  // 判别(安全): body 绝不含 tenant_id —— tenant 走 query, 否则后端 DisallowUnknownFields 拒(且客户端先就不该构造)。
  // mutation: builder 加 tenant_id 键 → key-set 断言红。
  const on = buildTenantPolicyBody(true);
  assert.deepEqual(Object.keys(on), ['inherit_global_catalog'], '精确 key-set 仅 inherit_global_catalog(无 tenant_id)');
  // 判别: inherit 布尔保真, false 也显式带(后端 *bool 必填)。fixture 用 true/false 两值防硬编码假绿。
  assert.equal(on.inherit_global_catalog, true, 'inherit=true 透传为布尔 true');
  assert.equal(buildTenantPolicyBody(false).inherit_global_catalog, false, 'inherit=false 透传为布尔 false');
});

// ── tenantPolicyErrorMessage ─────────────────────────────────────────────
test('TestTenantPolicyErrorMessage', () => {
  // 判别: 已知码映射到专属文案(漏映射 → 回退泛文案带 code)。
  for (const code of ['tenant_not_found', 'invalid_tenant_policy', 'tenant_id_required', 'gateway_not_configured', 'admin_forbidden_scope', 'model_admin_store_failed', 'admin_gate_not_configured', 'admin_backend_error']) {
    assert.ok(!tenantPolicyErrorMessage(code).includes(code), `已知码 ${code} 有专属文案`);
  }
  assert.match(tenantPolicyErrorMessage('weird_code'), /weird_code/, '未知码回退带 code');
});

// ── adminModelRegistryPolicy.ts 接线: 2 端点 URL + 方法 + validate + builder ─
test('TestEndpoints_Wiring', () => {
  assert.match(bodyAfter('const BASE'), /'\/v1\/admin\/model-registry-policy'/, 'BASE = /v1/admin/model-registry-policy');
  // 判别: GET 路径 BASE + tenant_id param; 用 apiGet(admin token)。
  assert.match(bodyAfter('export function getTenantPolicy'), /apiGet<TenantPolicyResponse>\(BASE,\s*\{\s*tenant_id/, 'GET 用 apiGet + BASE + tenant_id param');
  // 判别: PUT 路径 BASE?tenant_id=(tenant 走 query 非 body); 用 adminPut; validate-first + builder。
  assert.match(bodyAfter('export function setTenantInheritGlobal'), /\$\{BASE\}\?tenant_id=\$\{tenantId\}/, 'PUT 路径 BASE?tenant_id=');
  assert.match(bodyAfter('export function setTenantInheritGlobal'), /adminPut</, 'PUT 用 adminPut');
  assert.match(bodyAfter('export function setTenantInheritGlobal'), /validateSetTenantPolicy\(tenantId\)/, 'PUT validate-first');
  assert.match(bodyAfter('export function setTenantInheritGlobal'), /buildTenantPolicyBody\(inherit\)/, 'PUT 用 builder');
  assert.match(bodyAfter('async function adminPut'), /method:\s*'PUT'/, 'adminPut 发 PUT');
});
