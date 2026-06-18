// model_pool_bindings 数据层接线测试(补现存 modelBindings.ts 的零测试缺口)。
// modelBindings.ts 无独立纯逻辑文件(枚举/标签内嵌且 import client), 故纯 source 断言其接线
// (5 端点 URL + 方法 + UpdateInput 身份不可变 + 枚举对齐后端 allowlist), 不 import 避免 client 链。
// 每条断言一句话说清抓的回归; 端点锚定引号/${}定界符保持判别性。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

const ROOT = process.cwd();
const src = readFileSync(join(ROOT, 'lib/api/modelBindings.ts'), 'utf8');

function bodyAfter(marker: string): string {
  const start = src.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = src.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |async function |function |export interface |export const |export type |const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : src.length;
  return src.slice(start, end);
}

// ── 5 端点路径 + 方法(对齐后端 modelbindingadminhttp 顶层 CRUD) ──────────
test('TestBindings_Endpoints', () => {
  // 判别: BASE 写错 → 打到错误后端前缀(注意是 /admin/v1/ 不是 /v1/admin/)。
  assert.match(bodyAfter('const BASE'), /'\/admin\/v1\/model-pool-bindings'/, 'BASE 前缀正确');
  // list: GET BASE + 三过滤(漏 model_id/pool_group_id 过滤 → 列表无法按维度筛)。
  assert.match(bodyAfter('export function listBindings'), /apiGet<ModelPoolBindingList>\(BASE,/, 'list GET BASE');
  // tenant_id 是租户隔离的安全边界键(platform_admin 必带) —— 漏传则列表越租户/被后端 400 拒。
  // mutation: 实现删掉 tenant_id 透传行 → 此断言红(其余两条仍绿, 故必须独立断 tenant_id)。
  assert.match(bodyAfter('export function listBindings'), /tenant_id:\s*opts\.tenant_id/, 'list 透传 tenant_id(租户隔离边界)');
  assert.match(bodyAfter('export function listBindings'), /model_id:\s*opts\.model_id/, 'list 透传 model_id 过滤');
  assert.match(bodyAfter('export function listBindings'), /pool_group_id:\s*opts\.pool_group_id/, 'list 透传 pool_group_id 过滤');
  // create: POST BASE + tenantQuery。
  assert.match(bodyAfter('export function createBinding'), /apiPost<ModelPoolBinding>\(`\$\{BASE\}\$\{tenantQuery\(tenantId\)\}`/, 'create POST BASE?tenant');
  // update: PATCH(后端 newUpdateHandler 是 Patch, 不是 PUT)BASE/{id}?tenant。
  assert.match(bodyAfter('export function updateBinding'), /apiPatch<ModelPoolBinding>\(`\$\{BASE\}\/\$\{id\}\$\{tenantQuery\(tenantId\)\}`/, 'update PATCH BASE/{id}?tenant');
  // delete: DELETE BASE/{id}?tenant。
  assert.match(bodyAfter('export function deleteBinding'), /adminDelete\(`\$\{BASE\}\/\$\{id\}\$\{tenantQuery\(tenantId\)\}`\)/, 'delete DELETE BASE/{id}?tenant');
  assert.match(bodyAfter('async function adminDelete'), /method:\s*'DELETE'/, 'adminDelete 发 DELETE');
});

// ── UpdateInput 身份不可变: 不含 model_id/pool_group_id(改身份须删+建) ──
test('TestBindings_UpdateInputExcludesIdentity', () => {
  // 判别(安全/语义): update 类型若包含 model_id/pool_group_id, 调用方可经更新偷改绑定身份。
  // mutation: Omit 去掉某身份键 → 此断言红。
  assert.match(
    bodyAfter('export type UpdateBindingInput'),
    /Omit<CreateBindingInput,\s*'model_id'\s*\|\s*'pool_group_id'>/,
    'UpdateBindingInput 排除 model_id/pool_group_id(身份不可变)',
  );
});

// ── 枚举对齐后端 allowlist(modelbindingadminhttp validSelectionModes/validFallbackClasses) ──
test('TestBindings_EnumsMatchBackend', () => {
  // 判别: 前端枚举与后端 CHECK/allowlist 漂移 → 用户选了前端有后端无的值 → 后端 400。
  assert.match(bodyAfter('export type SelectionMode'), /'strict_priority'\s*\|\s*'priority_weighted'/, 'SelectionMode 两值对齐');
  assert.match(
    bodyAfter('export type FallbackClass'),
    /'normal'\s*\|\s*'context_window'\s*\|\s*'safety'\s*\|\s*'quota'\s*\|\s*'manual'/,
    'FallbackClass 五值对齐后端',
  );
  // 标签表须覆盖每个枚举值(漏一个 → UI 显示空白/undefined)。
  for (const k of ['strict_priority', 'priority_weighted']) {
    assert.match(bodyAfter('SELECTION_MODE_LABEL'), new RegExp(k), `标签覆盖 ${k}`);
  }
  for (const k of ['normal', 'context_window', 'safety', 'quota', 'manual']) {
    assert.match(bodyAfter('FALLBACK_CLASS_LABEL'), new RegExp(k), `标签覆盖 ${k}`);
  }
});
