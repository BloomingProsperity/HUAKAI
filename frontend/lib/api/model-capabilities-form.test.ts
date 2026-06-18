// 模型能力 / 别名 admin 数据层切片测试。
// 纯逻辑(能力校验/别名逐行校验镜像后端/精确 key-set/错误码映射)+ adminModelCapabilities.ts 接线断言
// (3 端点 URL + 方法 + validate-first + builder)。每条断言一句话说清抓的回归; 均经变异实测转红。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  buildAliasBulkBody,
  buildCapabilitiesBody,
  buildCapabilityBindingBody,
  modelAdminErrorMessage,
  validateAliasBulk,
  validateAliasRow,
  validateCapabilitiesInput,
  validateCapabilityBinding,
  type AliasRow,
  type CapabilitiesInput,
  type CapabilityBindingInput,
} from './model-capabilities-form.ts';

const ROOT = process.cwd();
const clientSrc = readFileSync(join(ROOT, 'lib/api/adminModelCapabilities.ts'), 'utf8');

function bodyAfter(marker: string): string {
  const start = clientSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = clientSrc.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |async function |function |export interface |export const |const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : clientSrc.length;
  return clientSrc.slice(start, end);
}

// ── validateCapabilitiesInput ───────────────────────────────────────────
test('TestValidateCapabilitiesInput', () => {
  assert.equal(validateCapabilitiesInput({ capabilities: { vision: true } }), null, '合法输入应通过');
  // 判别: max_output_tokens<=0 须拒(镜像后端 invalid_max_output_tokens)。
  assert.ok(validateCapabilitiesInput({ capabilities: {}, max_output_tokens: 0 }), 'max_output_tokens=0 应拒');
  assert.ok(validateCapabilitiesInput({ capabilities: {}, max_output_tokens: -1 }), '负 max_output_tokens 应拒');
  // 判别: 空能力 key 须拒(镜像后端 invalid_capabilities)。
  assert.ok(validateCapabilitiesInput({ capabilities: { '  ': true } }), '空能力 key 应拒');
  // 判别: 省略 max_output_tokens 合法(交后端默认)。
  assert.equal(validateCapabilitiesInput({ capabilities: { tools: false } }), null, '省略 max_output_tokens 合法');
});

// ── buildCapabilitiesBody: 精确 key-set, 可选项条件附带 ──────────────────
test('TestBuildCapabilitiesBody', () => {
  const body = buildCapabilitiesBody({ capabilities: { vision: true }, max_output_tokens: 4096, model_mode: 'chat' });
  assert.deepEqual(Object.keys(body).sort(), ['capabilities', 'max_output_tokens', 'model_mode'], 'full key-set');
  assert.deepEqual(body.capabilities, { vision: true }, 'capabilities 透传');
  // 判别: 省略可选项 → 该键不出现(交后端默认), 而非塞 undefined。
  const min = buildCapabilitiesBody({ capabilities: { vision: true } });
  assert.deepEqual(Object.keys(min), ['capabilities'], '省略时仅 capabilities 键');
  assert.ok(!('max_output_tokens' in min) && !('model_mode' in min), '省略键不出现');
  // 判别(validate-then-build 契约): builder 原样透传 invalid 值(校验是 caller 职责, 由 validateCapabilitiesInput
  // 先拦), 不得偷偷过滤。mutation: builder 加 max_output_tokens<=0 过滤 → 此断言红, 揭示 builder 越权过滤。
  assert.equal(buildCapabilitiesBody({ capabilities: {}, max_output_tokens: 0 }).max_output_tokens, 0, 'builder 透传 invalid 值(校验归 caller)');
});

// ── validateAliasRow: 镜像后端 normalizeModelAliasImport ─────────────────
function okRow(): AliasRow {
  return { model_id: 7, alias: 'gpt-4o-mini', scope: 'tenant', tenant_id: 5, status: 'active' };
}
test('TestValidateAliasRow', () => {
  assert.equal(validateAliasRow(okRow()), null, '合法行应通过');
  // 判别: scope 非法值须拒(后端 invalid scope)。
  assert.ok(validateAliasRow({ ...okRow(), scope: 'weird' }), '非法 scope 应拒');
  // 判别: tenant scope 缺 tenant_id 须拒(后端 tenant_id must be positive for tenant alias)。
  assert.ok(validateAliasRow({ model_id: 7, alias: 'a', scope: 'tenant' }), 'tenant scope 缺 tenant_id 应拒');
  // 判别: global scope 不需要 tenant_id → 通过。
  assert.equal(validateAliasRow({ model_id: 7, alias: 'a', scope: 'global' }), null, 'global scope 无需 tenant_id');
  // 判别: model_id<=0 / alias 空 / status 非法 各须拒。
  assert.ok(validateAliasRow({ ...okRow(), model_id: 0 }), 'model_id=0 应拒');
  assert.ok(validateAliasRow({ ...okRow(), alias: '  ' }), '空 alias 应拒');
  assert.ok(validateAliasRow({ ...okRow(), status: 'paused' }), '非法 status 应拒');
  // 判别: scope/status 省略 → 视后端默认(tenant/active), tenant 默认仍需 tenant_id。
  assert.equal(validateAliasRow({ model_id: 7, alias: 'a', tenant_id: 5 }), null, '省略 scope/status 用默认 tenant/active');
});

// ── validateAliasBulk: ≥1 行 + 逐行带行号 ───────────────────────────────
test('TestValidateAliasBulk', () => {
  // 判别: 空数组须拒(后端 invalid_aliases)。
  assert.ok(validateAliasBulk([]), '空 aliases 应拒');
  assert.equal(validateAliasBulk([okRow()]), null, '单合法行应通过');
  // 判别: 错误带行号 —— 第二行坏 → 文案含 '第 2 行'。
  const err = validateAliasBulk([okRow(), { ...okRow(), model_id: 0 }]);
  assert.ok(err && /第 2 行/.test(err), '错误须带行号(第 2 行)');
});

// ── buildAliasBulkBody: aliases 数组 + per-row key-set + reason 条件 ──────
test('TestBuildAliasBulkBody', () => {
  const body = buildAliasBulkBody([{ model_id: 7, alias: 'a', scope: 'global' }], 'ops import');
  assert.ok(Array.isArray(body.aliases), 'aliases 是数组');
  const row = (body.aliases as Record<string, unknown>[])[0];
  // 判别: 必填键 model_id/alias 在; 省略的可选键(tenant_id/display/status/source)不出现。
  assert.equal(row.model_id, 7, 'model_id 透传');
  assert.equal(row.alias, 'a', 'alias 透传');
  assert.equal(row.scope, 'global', 'scope 透传');
  assert.ok(!('status' in row) && !('display' in row) && !('tenant_id' in row), '省略可选键不出现');
  // 判别: reason 给了须带; 省略不出现。
  assert.equal(body.reason, 'ops import', 'reason 透传');
  assert.ok(!('reason' in buildAliasBulkBody([{ model_id: 7, alias: 'a', scope: 'global' }])), '省略 reason 不出现');
});

// 判别: 空白-only 的可选键须被当"未给"省略(镜像后端 TrimSpace 默认), 不得把空白值序列化给后端。
// mutation: 去掉 buildAliasRow/buildAliasBulkBody 的 .trim() !== '' 过滤 → 空白键被序列化 → 这些断言红。
test('TestBuildAliasBulk_WhitespaceOptionalsOmitted', () => {
  const body = buildAliasBulkBody(
    [{ model_id: 7, alias: 'a', scope: '  ', status: '  ', display: '  ', source: '  ' }],
    '  ',
  );
  const row = (body.aliases as Record<string, unknown>[])[0];
  assert.ok(!('scope' in row), '空白 scope 省略(交后端默认 tenant)');
  assert.ok(!('status' in row), '空白 status 省略(交后端默认 active)');
  assert.ok(!('display' in row), '空白 display 省略(交后端默认=alias)');
  assert.ok(!('source' in row), '空白 source 省略(交后端默认)');
  assert.ok(!('reason' in body), '空白 reason 省略');
  // 必填键仍在(证明不是整体丢弃)。
  assert.equal(row.model_id, 7, 'model_id 仍在');
  assert.equal(row.alias, 'a', 'alias 仍在');
});

// ── validateCapabilityBinding: 镜像后端 upsertModelCapabilityBinding 形态前置校验 ──
test('TestValidateCapabilityBinding', () => {
  const ok: CapabilityBindingInput = { scope: 'tenant', capability: 'vision', enabled: true, tenant_id: 7 };
  assert.equal(validateCapabilityBinding(ok), null, '合法绑定应通过');
  // 判别: 非法 scope 须拒。
  assert.ok(validateCapabilityBinding({ ...ok, scope: 'weird' }), '非法 scope 应拒');
  // 判别: tenant scope 缺 tenant_id 须拒。
  assert.ok(validateCapabilityBinding({ scope: 'tenant', capability: 'vision', enabled: true }), 'tenant scope 缺 tenant_id 应拒');
  // 判别: global scope 不需要 tenant_id → 通过。
  assert.equal(validateCapabilityBinding({ scope: 'global', capability: 'vision', enabled: false }), null, 'global scope 无需 tenant_id');
  // 判别: 空 capability 须拒。
  assert.ok(validateCapabilityBinding({ ...ok, capability: '  ' }), '空 capability 应拒');
  // 判别: 省略 scope → 默认 tenant, 仍需 tenant_id。
  assert.ok(validateCapabilityBinding({ scope: '', capability: 'vision', enabled: true } as CapabilityBindingInput), '省略 scope 默认 tenant 仍需 tenant_id');
});

// ── buildCapabilityBindingBody: 精确 key-set, 无 source, enabled 永远带 ────
test('TestBuildCapabilityBindingBody', () => {
  // 判别(安全): body 绝不含 source —— source 服务端强制 operator, 客户端不得构造它(防伪装 vendor-sync)。
  // mutation: builder 加 source 键 → 此断言红。
  const body = buildCapabilityBindingBody({ scope: 'global', capability: 'vision', enabled: false });
  assert.ok(!('source' in body), 'body 绝不含 source(服务端强制 operator)');
  // 判别(footgun): enabled 永远显式带 —— 即便 false(省略后端会按零值静默翻 disabled)。fixture 用 false 防硬编码 true 假绿。
  assert.ok('enabled' in body, 'enabled 永远显式带');
  assert.equal(body.enabled, false, 'enabled=false 透传(非省略)');
  assert.equal(body.scope, 'global', 'scope 透传');
  assert.equal(body.capability, 'vision', 'capability 透传');
  // 判别: 精确 key-set(无 source); 省略可选项不出现。
  assert.deepEqual(Object.keys(body).sort(), ['capability', 'enabled', 'scope'], '最小 key-set 仅 scope/capability/enabled');
  // 判别: 给定可选项精确附带。
  const full = buildCapabilityBindingBody({ scope: 'tenant', capability: 'reasoning', enabled: true, tenant_id: 7, capability_value: 'high', capability_params: { levels: ['low', 'high'] } });
  assert.deepEqual(Object.keys(full).sort(), ['capability', 'capability_params', 'capability_value', 'enabled', 'scope', 'tenant_id'], 'full key-set');
  assert.equal(full.tenant_id, 7, 'tenant_id 透传');
  assert.equal(full.capability_value, 'high', 'capability_value 透传');
  // 判别: capability_params 值也须原样透传(与 capability_value 对称, 防 builder 映射成空/错对象)。
  // mutation: builder 把 capability_params 映射成 {} → 此断言红。
  assert.deepEqual(full.capability_params, { levels: ['low', 'high'] }, 'capability_params 值透传');
});

// ── modelAdminErrorMessage ──────────────────────────────────────────────
test('TestModelAdminErrorMessage', () => {
  for (const code of ['invalid_model_id', 'model_not_found', 'invalid_aliases', 'invalid_capability', 'gateway_not_configured', 'model_admin_store_failed']) {
    assert.ok(!modelAdminErrorMessage(code).includes(code), `已知码 ${code} 有专属文案`);
  }
  assert.match(modelAdminErrorMessage('weird_code'), /weird_code/, '未知码回退带 code');
});

// ── adminModelCapabilities.ts 接线: 3 端点 URL + 方法 + validate + builder ─
test('TestEndpoints_Paths', () => {
  assert.match(bodyAfter('const BASE'), /'\/v1\/admin\/models'/, 'BASE = /v1/admin/models');
  assert.match(bodyAfter('export function updateModelCapabilities'), /\$\{BASE\}\/\$\{modelId\}\/capabilities`/, 'capabilities 路径 BASE/{id}/capabilities');
  assert.match(bodyAfter('export function bulkImportModelAliases'), /\$\{BASE\}\/aliases\/bulk-import`/, 'bulk-import 路径 BASE/aliases/bulk-import');
  assert.match(bodyAfter('export function getModelCapabilityBindings'), /\$\{BASE\}\/\$\{modelId\}\/capability-bindings`/, 'bindings GET 路径 BASE/{id}/capability-bindings');
  // 判别: upsert 写端点与 GET 同路径(后端同路径不同方法), 路径写错 → 命中错端点。
  assert.match(bodyAfter('export function upsertModelCapabilityBinding'), /\$\{BASE\}\/\$\{modelId\}\/capability-bindings`/, 'binding upsert 路径 BASE/{id}/capability-bindings');
});

test('TestEndpoints_VerbsAndBuilders', () => {
  // 判别: 方法用错 → 后端 405。capabilities 必 PUT, bulk-import 必 POST, bindings 必 GET。
  assert.match(bodyAfter('export function updateModelCapabilities'), /adminPut</, 'capabilities 用 PUT');
  assert.match(bodyAfter('export function bulkImportModelAliases'), /apiPost</, 'bulk-import 用 POST');
  assert.match(bodyAfter('export function getModelCapabilityBindings'), /apiGet</, 'bindings 用 GET');
  assert.match(bodyAfter('async function adminPut'), /method:\s*'PUT'/, 'adminPut 发 PUT');
  // 判别: 写端点 validate-first(漏则非法直发后端) + 用 builder(漏则 key-set 失控)。
  assert.match(bodyAfter('export function updateModelCapabilities'), /validateCapabilitiesInput\(input\)/, 'capabilities validate-first');
  assert.match(bodyAfter('export function updateModelCapabilities'), /buildCapabilitiesBody\(input\)/, 'capabilities 用 builder');
  assert.match(bodyAfter('export function bulkImportModelAliases'), /validateAliasBulk\(rows\)/, 'bulk-import validate-first');
  assert.match(bodyAfter('export function bulkImportModelAliases'), /buildAliasBulkBody\(rows, reason\)/, 'bulk-import 用 builder');
  // 判别: binding upsert 用 PUT(adminPut) + validate-first + builder(漏 builder 则可能漏掉 source 守卫/key-set 失控)。
  assert.match(bodyAfter('export function upsertModelCapabilityBinding'), /adminPut</, 'binding upsert 用 PUT');
  assert.match(bodyAfter('export function upsertModelCapabilityBinding'), /validateCapabilityBinding\(input\)/, 'binding upsert validate-first');
  assert.match(bodyAfter('export function upsertModelCapabilityBinding'), /buildCapabilityBindingBody\(input\)/, 'binding upsert 用 builder');
});
