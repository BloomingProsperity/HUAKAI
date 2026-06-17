// 告警规则 CRUD 切片强测试。
// 纯逻辑（校验/filters/请求体构造/DisallowUnknownFields 键集/数字解析）+ adminAlertRules.ts 接线断言。
// 每条断言可一句话说出抓的回归；均经变异实测转红再还原（CLAUDE.md §14）。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  buildCreateBody,
  buildUpdateBody,
  parseFilters,
  validateAlertRuleForm,
  type AlertRuleFormInput,
} from './alert-rule-form.ts';

const ROOT = process.cwd();
const adminSrc = readFileSync(join(ROOT, 'lib/api/adminAlertRules.ts'), 'utf8');

function bodyAfter(marker: string): string {
  const start = adminSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = adminSrc.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |async function |function |export interface |export const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : adminSrc.length;
  return adminSrc.slice(start, end);
}

function validBase(): AlertRuleFormInput {
  return {
    name: 'cpu 高', metric_type: '', metric: 'cpu', comparator: 'gt', threshold_raw: '80',
    severity: 'warning', window_seconds_raw: '300', sustained_seconds_raw: '', cooldown_seconds_raw: '',
    notify_email: false, filters_raw: '', enabled: true,
  };
}

// ── 校验 ────────────────────────────────────────────────────────────────

test('TestValidate_NameAndMetric', () => {
  assert.equal(validateAlertRuleForm(validBase()), null, '合法基线');
  assert.ok(validateAlertRuleForm({ ...validBase(), name: '  ' }), '空 name 应报错');
  // 判别：metric_type 与 metric 都空应报错；任一非空合法。
  assert.ok(validateAlertRuleForm({ ...validBase(), metric: '', metric_type: '' }), '二者都空应报错');
  assert.equal(validateAlertRuleForm({ ...validBase(), metric: '', metric_type: 'cpu_usage_percent' }), null, '仅 metric_type 合法');
});

test('TestValidate_Comparator', () => {
  for (const c of ['gt', 'gte', 'lt', 'lte']) {
    assert.equal(validateAlertRuleForm({ ...validBase(), comparator: c }), null, `${c} 合法`);
  }
  assert.ok(validateAlertRuleForm({ ...validBase(), comparator: 'eq' }), 'eq 应报错');
  assert.ok(validateAlertRuleForm({ ...validBase(), comparator: 'GT' }), '大写 GT 应报错（后端区分大小写）');
});

test('TestValidate_Threshold', () => {
  // 判别：有限数字才合法；0/负数合法；空/NaN/Infinity 拒。
  assert.equal(validateAlertRuleForm({ ...validBase(), threshold_raw: '0' }), null, '0 合法');
  assert.equal(validateAlertRuleForm({ ...validBase(), threshold_raw: '-5.5' }), null, '负小数合法');
  assert.ok(validateAlertRuleForm({ ...validBase(), threshold_raw: '' }), '空 threshold 应报错');
  assert.ok(validateAlertRuleForm({ ...validBase(), threshold_raw: 'abc' }), '非数字应报错');
  assert.ok(validateAlertRuleForm({ ...validBase(), threshold_raw: 'Infinity' }), 'Infinity 应报错');
});

test('TestValidate_Severity', () => {
  assert.equal(validateAlertRuleForm({ ...validBase(), severity: 'critical' }), null, 'critical 合法');
  assert.ok(validateAlertRuleForm({ ...validBase(), severity: 'fatal' }), 'fatal 应报错');
});

test('TestValidate_WindowAndSeconds', () => {
  // window 必须正整数。
  assert.ok(validateAlertRuleForm({ ...validBase(), window_seconds_raw: '0' }), 'window 0 应报错');
  assert.ok(validateAlertRuleForm({ ...validBase(), window_seconds_raw: '-1' }), 'window 负应报错');
  assert.ok(validateAlertRuleForm({ ...validBase(), window_seconds_raw: '1.5' }), 'window 小数应报错');
  assert.ok(validateAlertRuleForm({ ...validBase(), window_seconds_raw: '' }), 'window 空应报错');
  // sustained/cooldown：空合法（=0）；给则非负整数。
  assert.equal(validateAlertRuleForm({ ...validBase(), sustained_seconds_raw: '', cooldown_seconds_raw: '' }), null, '持续/冷却空合法');
  assert.ok(validateAlertRuleForm({ ...validBase(), sustained_seconds_raw: '-1' }), '持续负应报错');
  assert.ok(validateAlertRuleForm({ ...validBase(), cooldown_seconds_raw: '1.5' }), '冷却小数应报错');
  // 判别：超 int32 上界应报错（后端 int32，溢出会 invalid_json）。
  assert.ok(validateAlertRuleForm({ ...validBase(), window_seconds_raw: '2147483648' }), 'window 超 int32 应报错');
});

test('TestParseFilters_AndValidate', () => {
  assert.deepEqual(parseFilters('').value, {}, '空→{}');
  assert.deepEqual(parseFilters('{"a":"b"}').value, { a: 'b' }, '对象解析');
  assert.equal(parseFilters('[1]').ok, false, '数组拒');
  // 判别：字符串元素数组也必须被 array guard 拒（非靠下游值类型检查）——否则删 Array.isArray 仍绿。
  assert.equal(parseFilters('["a"]').ok, false, '字符串数组也拒（array guard）');
  assert.equal(parseFilters('{"a":1}').ok, false, '非字符串值拒');
  assert.equal(parseFilters('not json').ok, false, '非 JSON 拒');
  // 判别：filters 非法 → validate 报错。
  assert.ok(validateAlertRuleForm({ ...validBase(), filters_raw: '{"a":1}' }), 'filters 非字符串值应报错');
});

// ── 请求体构造 ──────────────────────────────────────────────────────────

test('TestBuildCreateBody', () => {
  const body = buildCreateBody({ ...validBase(), name: '  cpu  ' }, 7);
  // 精确键集（DisallowUnknownFields）：metric 非空带 metric、无 metric_type、无 filters；sustained/cooldown 始终带(=0)。
  assert.deepEqual(
    Object.keys(body).sort(),
    ['comparator', 'cooldown_seconds', 'enabled', 'metric', 'name', 'notify_email', 'severity', 'sustained_seconds', 'tenant_id', 'threshold', 'window_seconds'],
    'create 精确键集（metric 路径）',
  );
  assert.equal(body.tenant_id, 7, 'tenant_id 入 body');
  // 判别：再用不同 tenant 证取自入参而非硬编码同值。
  assert.equal(buildCreateBody(validBase(), 13).tenant_id, 13, 'tenant_id 取自入参（13）');
  assert.equal(body.name, 'cpu', 'name trim');
  // 判别：数值字段是【数字】非字符串（漏 Number() → 发字符串，后端类型错）。
  assert.equal(body.threshold, 80, 'threshold 数值');
  assert.equal(typeof body.threshold, 'number', 'threshold 是 number 非 string');
  assert.equal(body.window_seconds, 300, 'window_seconds 数值');
  assert.equal(typeof body.window_seconds, 'number', 'window_seconds 是 number');
  assert.equal(body.sustained_seconds, 0, '空 sustained → 0');
  assert.equal('id' in body, false, 'create 不带 id');
  // 判别：非空 sustained/cooldown 须解析为【数字】（intOrZero 的 Number() 分支；漏则发字符串，后端 int32 类型错）。
  const body2 = buildCreateBody({ ...validBase(), sustained_seconds_raw: '30', cooldown_seconds_raw: '45' }, 1);
  assert.equal(body2.sustained_seconds, 30, '非空 sustained 解析为 30');
  assert.equal(typeof body2.sustained_seconds, 'number', 'sustained 是 number 非 string');
  assert.equal(body2.cooldown_seconds, 45, '非空 cooldown 解析为 45');
  assert.equal(typeof body2.cooldown_seconds, 'number', 'cooldown 是 number 非 string');
});

test('TestBuildCreateBody_MetricTypeAndFilters', () => {
  const body = buildCreateBody({ ...validBase(), metric: '', metric_type: 'cpu_usage_percent', filters_raw: '{"platform":"anthropic"}' }, 1);
  // 判别：metric 空→省略 metric 键、带 metric_type；filters 非空→带对象。
  assert.equal('metric' in body, false, '空 metric 省略键');
  assert.equal(body.metric_type, 'cpu_usage_percent', 'metric_type 带上');
  assert.deepEqual(body.filters, { platform: 'anthropic' }, 'filters 解析为对象');
  // 空 filters 省略键。
  assert.equal('filters' in buildCreateBody(validBase(), 1), false, '空 filters 省略键');
});

test('TestBuildUpdateBody', () => {
  const body = buildUpdateBody({ ...validBase(), metric: 'cpu', metric_type: '' });
  // 判别：update 禁 tenant_id/id；metric 与 metric_type 都发（空串可清除）；filters 始终发（{}）。
  assert.equal('tenant_id' in body, false, 'update 不带 tenant_id');
  assert.equal('id' in body, false, 'update 不带 id');
  assert.equal(body.metric, 'cpu', 'metric 发');
  assert.equal(body.metric_type, '', 'metric_type 空串也发（清除）');
  assert.deepEqual(body.filters, {}, '空 filters → {} 始终发');
  assert.equal(typeof body.threshold, 'number', 'threshold 数值');
});

// ── adminAlertRules.ts 接线 ─────────────────────────────────────────────

test('TestEndpoints_PathsVerbsBuilder', () => {
  assert.match(bodyAfter('export function listAlertRules'), /apiGet<AlertRuleListResponse>\('\/v1\/admin\/alert-rules'/, 'list GET 路径');
  assert.match(bodyAfter('export function listAlertRules'), /tenant_id: opts\.tenant_id/, 'list tenant 入 query（锚定接线）');
  assert.match(bodyAfter('export function getAlertRule'), /apiGet<AlertRule>\(`\/v1\/admin\/alert-rules\/\$\{id\}`/, 'get 路径');
  assert.match(bodyAfter('export function createAlertRule'), /apiPost<AlertRule>\('\/v1\/admin\/alert-rules'/, 'create POST 路径');
  assert.match(bodyAfter('export function createAlertRule'), /buildCreateBody\(input, tenantId\)/, 'create 用 builder 传 tenantId');
  assert.doesNotMatch(bodyAfter('export function createAlertRule'), /tenantQuery/, 'create URL 不带 tenantQuery（tenant 在 body）');
  assert.match(bodyAfter('export function updateAlertRule'), /adminPut/, 'update 用 PUT');
  assert.match(bodyAfter('export function updateAlertRule'), /\/v1\/admin\/alert-rules\/\$\{id\}\$\{tenantQuery/, 'update 路径带 id+tenantQuery');
  assert.match(bodyAfter('export function updateAlertRule'), /buildUpdateBody\(input\)/, 'update 用 builder');
  assert.match(bodyAfter('export function deleteAlertRule'), /adminDelete/, 'delete 用 DELETE');
  assert.match(bodyAfter('export function deleteAlertRule'), /\/v1\/admin\/alert-rules\/\$\{id\}\$\{tenantQuery/, 'delete 路径带 id+tenantQuery');
});

test('TestHelpers_HttpVerbs', () => {
  assert.match(bodyAfter('async function adminPut'), /method:\s*'PUT'/, 'adminPut 发 PUT');
  assert.match(bodyAfter('async function adminDelete'), /method:\s*'DELETE'/, 'adminDelete 发 DELETE');
});
