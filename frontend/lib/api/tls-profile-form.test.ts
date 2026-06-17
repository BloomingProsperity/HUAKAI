// TLS 指纹画像 admin 写/详情接线强测试。
// 纯逻辑（状态 allowlist + name 校验 + 精确 key-set）+ adminTLSProfiles.ts 接线断言（路径/动词/builder/validate）。
// 每条断言可一句话说出抓的回归；均经变异实测转红再还原（CLAUDE.md §14）。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  buildTLSProfileCreateBody,
  buildTLSProfileUpdateBody,
  isValidTLSProfileStatus,
  validateTLSProfileInput,
  type TLSProfileInput,
} from './tls-profile-form.ts';

const ROOT = process.cwd();
const adminSrc = readFileSync(join(ROOT, 'lib/api/adminTLSProfiles.ts'), 'utf8');

function bodyAfter(marker: string): string {
  const start = adminSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = adminSrc.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |async function |function |export interface |export const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : adminSrc.length;
  return adminSrc.slice(start, end);
}

const sample: TLSProfileInput = {
  name: 'chrome-120',
  description: 'desc',
  grease_enabled: true,
  cipher_suites: [4865, 4866],
  supported_curves: [29, 23],
  ec_point_formats: [0],
  signature_algorithms: [1027],
  alpn_protocols: ['h2', 'http/1.1'],
  tls_supported_versions: [772, 771],
  key_share_groups: [29],
  psk_modes: [1],
  extensions_order: [0, 11, 10],
  expected_ja3_hash: 'abc123',
};

const PROFILE_FIELDS = [
  'alpn_protocols', 'cipher_suites', 'description', 'ec_point_formats', 'expected_ja3_hash',
  'extensions_order', 'grease_enabled', 'key_share_groups', 'name', 'psk_modes',
  'signature_algorithms', 'supported_curves', 'tls_supported_versions',
];

// ── isValidTLSProfileStatus：后端 adminSettableStatuses = {active, disabled} ───

test('TestIsValidTLSProfileStatus', () => {
  // 判别：漏 allowlist → 任意串当合法 → 后端 ErrInvalidStatus(400)。
  assert.equal(isValidTLSProfileStatus('active'), true, 'active 合法');
  assert.equal(isValidTLSProfileStatus('disabled'), true, 'disabled 合法');
  assert.equal(isValidTLSProfileStatus('drift_detected'), false, 'drift_detected 不可 admin 设');
  assert.equal(isValidTLSProfileStatus(''), false, '空应拒');
});

// ── validateTLSProfileInput：name 非空必填（后端 service ErrInvalidInput）─────

test('TestValidateTLSProfileInput', () => {
  assert.ok(validateTLSProfileInput({ ...sample, name: '  ' }), '空白 name 应报错');
  assert.equal(validateTLSProfileInput(sample), null, '有 name 应通过');
});

// ── buildTLSProfileCreateBody / UpdateBody：精确 key-set（DisallowUnknownFields）──

test('TestBuildCreateBody_KeySet', () => {
  const body = buildTLSProfileCreateBody(7, sample);
  // 判别：create 体须 = tenant_id + 13 字段，无多余/错键（否则后端 DisallowUnknownFields 400）。
  assert.deepEqual(Object.keys(body).sort(), ['tenant_id', ...PROFILE_FIELDS].sort(), 'create key-set = tenant_id + 13 字段');
  // 判别：tenant_id 在 body（create 契约）且原样透传(非 1)。
  assert.equal(body.tenant_id, 7, 'create tenant_id 在 body');
});

test('TestBuildUpdateBody_NoTenantNoStatus', () => {
  const body = buildTLSProfileUpdateBody(sample);
  // 判别：update 体恰 13 字段。
  assert.deepEqual(Object.keys(body).sort(), [...PROFILE_FIELDS].sort(), 'update key-set = 13 字段');
  // 判别（安全相关）：update **不得**含 status——后端 PUT 经 DisallowUnknownFields 拒 status，
  // 状态只能经 POST /{id}/status 改；若 builder 漏带 status 进来 → 400 或越权改状态路径混淆。
  assert.equal('status' in body, false, 'update 不含 status（状态走 setStatus）');
  // 判别（安全相关）：update 不得含 tenant_id（PUT 的 tenant 在 query 非 body）。
  assert.equal('tenant_id' in body, false, 'update 不含 tenant_id（在 query）');
});

// ── adminTLSProfiles.ts 接线：6 端点路径 + 动词 + builder + validate ───────────

test('TestEndpoints_PathsVerbs', () => {
  const create = bodyAfter('export function createTLSProfile');
  const get = bodyAfter('export function getTLSProfile');
  const update = bodyAfter('export function updateTLSProfile');
  const status = bodyAfter('export function setTLSProfileStatus');
  const del = bodyAfter('export function deleteTLSProfile');
  // 判别：路径写错 → 打到错误后端路由。BASE = '/v1/admin/tls-fingerprint-profiles'。
  assert.match(create, /apiPost<TLSProfileResponse>\(BASE,/, 'create POST BASE（tenant_id 在 body）');
  assert.match(get, /`\$\{BASE\}\/\$\{id\}\$\{tenantQuery\(tenantId\)\}`/, 'get 路径 {id}+tenant query');
  assert.match(update, /`\$\{BASE\}\/\$\{id\}\$\{tenantQuery\(tenantId\)\}`/, 'update 路径 {id}+tenant query');
  assert.match(status, /`\$\{BASE\}\/\$\{id\}\/status\$\{tenantQuery\(tenantId\)\}`/, 'status 路径 {id}/status');
  assert.match(del, /`\$\{BASE\}\/\$\{id\}\$\{tenantQuery\(tenantId\)\}`/, 'delete 路径 {id}+tenant query');
  // 判别：动词错 → 405。create/status 用 POST，get 用 GET，update 用 PUT，delete 用 DELETE。
  assert.match(get, /apiGet</, 'get 用 GET');
  assert.match(update, /adminPut</, 'update 用 PUT');
  assert.match(status, /apiPost</, 'status 用 POST');
  assert.match(del, /adminDelete</, 'delete 用 DELETE');
});

test('TestEndpoints_ValidateAndBuilders', () => {
  const create = bodyAfter('export function createTLSProfile');
  const update = bodyAfter('export function updateTLSProfile');
  const status = bodyAfter('export function setTLSProfileStatus');
  // 判别：create/update 发请求前先 validate（name 必填，validator 不悬空）+ 经 builder 构造精确体。
  assert.match(create, /validateTLSProfileInput\(input\)/, 'create 先校验');
  assert.match(create, /buildTLSProfileCreateBody\(tenantId, input\)/, 'create 用 builder');
  assert.match(update, /validateTLSProfileInput\(input\)/, 'update 先校验');
  assert.match(update, /buildTLSProfileUpdateBody\(input\)/, 'update 用 builder');
  // 判别：status 发请求前先校验 allowlist（漏则非法 status 打到后端）。
  assert.match(status, /isValidTLSProfileStatus\(status\)/, 'status 先校验 allowlist');
});
