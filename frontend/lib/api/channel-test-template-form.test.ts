// 渠道测试模板 CRUD 切片强测试。
// 纯逻辑（校验/凭证头守门/请求体构造，直接 strip-types 单测）+ adminChannelTestTemplates.ts 接线断言（端点+方法+tenantQuery+builder）。
// 每条断言可一句话说出抓的回归；均经变异实测转红再还原（CLAUDE.md §14）。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  buildChannelTestTemplateBody,
  isCredentialHeaderName,
  parseHeadersField,
  validateChannelTestTemplateForm,
  type ChannelTestTemplateFormInput,
} from './channel-test-template-form.ts';

const ROOT = process.cwd();
const adminSrc = readFileSync(join(ROOT, 'lib/api/adminChannelTestTemplates.ts'), 'utf8');

// 取某函数段：marker 起到【下一个顶层声明】前止。端点断言锚定引号/${} 定界符，故圈进邻段注释仍判别。
function bodyAfter(marker: string): string {
  const start = adminSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = adminSrc.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |async function |function |export interface |export const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : adminSrc.length;
  return adminSrc.slice(start, end);
}

function validBase(): ChannelTestTemplateFormInput {
  return { name: '健康检查', method: 'POST', path: '/v1/models', body_template: '{}', headers_raw: '' };
}

// ── 校验：name 必填 ≤128 ────────────────────────────────────────────────

test('TestValidate_Name', () => {
  assert.equal(validateChannelTestTemplateForm(validBase()), null, '合法基线应通过');
  // 判别：漏必填 → 空 name 放行 → 后端 400 invalid_template_name。
  assert.ok(validateChannelTestTemplateForm({ ...validBase(), name: '' }), '空 name 应报错');
  assert.ok(validateChannelTestTemplateForm({ ...validBase(), name: '   ' }), '纯空白 name 应报错');
  // 判别：漏 ≤128 → 超长放行。
  assert.ok(validateChannelTestTemplateForm({ ...validBase(), name: 'x'.repeat(129) }), '超 128 应报错');
});

// ── 校验：method 白名单（小写输入合法→大写）────────────────────────────

test('TestValidate_Method', () => {
  // 判别：漏白名单 → 任意动词放行 → 后端 400 invalid_template_method。
  assert.ok(validateChannelTestTemplateForm({ ...validBase(), method: 'TRACE' }), 'TRACE 应报错');
  assert.ok(validateChannelTestTemplateForm({ ...validBase(), method: 'connect' }), 'CONNECT 应报错');
  // 判别：漏大写归一 → 小写输入被当非法（后端会 upper，故前端须容许小写）。
  assert.equal(validateChannelTestTemplateForm({ ...validBase(), method: 'get' }), null, '小写 get 应通过（后端大写归一）');
  // 判别：逐条覆盖全 5 个白名单动词，防 TEMPLATE_METHODS 缩减时 PUT/DELETE 被前端误拒（后端允许）。
  assert.equal(validateChannelTestTemplateForm({ ...validBase(), method: 'PATCH' }), null, 'PATCH 应通过');
  assert.equal(validateChannelTestTemplateForm({ ...validBase(), method: 'PUT' }), null, 'PUT 应通过');
  assert.equal(validateChannelTestTemplateForm({ ...validBase(), method: 'DELETE' }), null, 'DELETE 应通过');
});

// ── 校验：path 必须 / 开头 ≤2048 ───────────────────────────────────────

test('TestValidate_Path', () => {
  // 判别：漏前缀守门 → 无 / 前缀放行 → 后端 400 invalid_template_path。
  assert.ok(validateChannelTestTemplateForm({ ...validBase(), path: 'v1/models' }), '无 / 前缀应报错');
  assert.ok(validateChannelTestTemplateForm({ ...validBase(), path: '' }), '空 path 应报错');
  // 判别：漏 ≤2048 → 超长放行。
  assert.ok(validateChannelTestTemplateForm({ ...validBase(), path: '/' + 'a'.repeat(2048) }), '超 2048 应报错');
  assert.equal(validateChannelTestTemplateForm({ ...validBase(), path: '/v1/chat/completions' }), null, '合法 path 应通过');
});

// ── 校验：headers 凭证头守门（安全关键）──────────────────────────────────

test('TestValidate_HeadersCredentialGuard', () => {
  // 判别：漏凭证头守门 → 密钥头被存入测试模板（泄漏）→ 后端 400 credential_header_not_allowed。
  assert.ok(validateChannelTestTemplateForm({ ...validBase(), headers_raw: '{"Authorization":"Bearer x"}' }), 'Authorization 应拒');
  assert.ok(validateChannelTestTemplateForm({ ...validBase(), headers_raw: '{"x-api-key":"sk-1"}' }), 'x-api-key 应拒');
  // 判别：逐条覆盖 6 个凭证头里易被「列表缩减」漏掉的 api-key(Azure)/x-auth-token/proxy-authorization。
  assert.ok(validateChannelTestTemplateForm({ ...validBase(), headers_raw: '{"api-key":"k"}' }), 'api-key 应拒');
  assert.ok(validateChannelTestTemplateForm({ ...validBase(), headers_raw: '{"x-auth-token":"t"}' }), 'x-auth-token 应拒');
  assert.ok(validateChannelTestTemplateForm({ ...validBase(), headers_raw: '{"proxy-authorization":"Basic x"}' }), 'proxy-authorization 应拒');
  // 判别：漏大小写不敏感 → 大写凭证头漏网。
  assert.ok(validateChannelTestTemplateForm({ ...validBase(), headers_raw: '{"COOKIE":"a=b"}' }), '大写 COOKIE 应拒');
  // 判别：漏「必须 JSON 对象」→ 数组/标量放行 → 后端 400。
  assert.ok(validateChannelTestTemplateForm({ ...validBase(), headers_raw: '[1,2]' }), '数组 headers 应拒');
  assert.ok(validateChannelTestTemplateForm({ ...validBase(), headers_raw: '"str"' }), '标量 headers 应拒');
  // 合法：普通头 / 空。
  assert.equal(validateChannelTestTemplateForm({ ...validBase(), headers_raw: '{"X-Test":"1"}' }), null, '普通头应通过');
  assert.equal(validateChannelTestTemplateForm({ ...validBase(), headers_raw: '' }), null, '空 headers 应通过');
});

test('TestIsCredentialHeaderName', () => {
  // 逐条覆盖全部 6 个凭证头：任一从 CREDENTIAL_HEADER_NAMES 被删都会让此测试转红（防列表缩减回归）。
  assert.equal(isCredentialHeaderName('authorization'), true, 'authorization');
  assert.equal(isCredentialHeaderName('  Proxy-Authorization '), true, '大小写/空白不敏感');
  assert.equal(isCredentialHeaderName('cookie'), true, 'cookie');
  assert.equal(isCredentialHeaderName('x-api-key'), true, 'x-api-key');
  assert.equal(isCredentialHeaderName('api-key'), true, 'api-key');
  assert.equal(isCredentialHeaderName('x-auth-token'), true, 'x-auth-token');
  assert.equal(isCredentialHeaderName('X-Test'), false, '普通头放行');
});

test('TestParseHeadersField', () => {
  assert.deepEqual(parseHeadersField('').value, {}, '空→{}');
  assert.deepEqual(parseHeadersField('{"a":"b"}').value, { a: 'b' }, '对象解析');
  assert.equal(parseHeadersField('not json').ok, false, '非 JSON 拒');
  assert.equal(parseHeadersField('[1]').ok, false, '数组拒');
  assert.equal(parseHeadersField('{"cookie":"x"}').ok, false, '凭证头拒');
});

// ── 请求体构造：method 大写 + headers 对象化 ───────────────────────────

test('TestBuildBody', () => {
  const body = buildChannelTestTemplateBody({
    name: '  健康  ',
    method: 'get',
    path: '  /v1/models  ',
    body_template: '{"x":1}',
    headers_raw: '{"X-Test":"1"}',
  });
  // 判别：漏 trim/大写归一/对象化 → 发出脏数据。
  assert.equal(body.name, '健康', 'name trim');
  assert.equal(body.method, 'GET', 'method 大写');
  assert.equal(body.path, '/v1/models', 'path trim');
  assert.equal(body.body_template, '{"x":1}', 'body_template 透传');
  assert.deepEqual(body.headers, { 'X-Test': '1' }, 'headers 解析为对象');
  // 空 headers → {}。
  assert.deepEqual(buildChannelTestTemplateBody(validBase()).headers, {}, '空 headers→{}');
});

// ── adminChannelTestTemplates.ts 接线：五端点路径 + 方法 + tenantQuery + builder ──

test('TestEndpoints_Paths', () => {
  // 判别：端点路径写错 → 打到错误后端路由 → red。路径锚定引号/${}定界符避免尾部追加 typo 非判别。
  assert.match(bodyAfter('export function listChannelTestTemplates'), /'\/admin\/v1\/channel-test-templates'/, 'list 路径');
  assert.match(bodyAfter('export function getChannelTestTemplate'), /\/admin\/v1\/channel-test-templates\/\$\{id\}`/, 'get 路径');
  assert.match(bodyAfter('export function createChannelTestTemplate'), /\/admin\/v1\/channel-test-templates\$\{tenantQuery/, 'create 路径');
  assert.match(bodyAfter('export function updateChannelTestTemplate'), /\/admin\/v1\/channel-test-templates\/\$\{id\}\$\{tenantQuery/, 'update 路径');
  assert.match(bodyAfter('export function deleteChannelTestTemplate'), /\/admin\/v1\/channel-test-templates\/\$\{id\}\$\{tenantQuery/, 'delete 路径');
});

test('TestEndpoints_VerbsBuilderTenant', () => {
  // 判别：动词用错 → 405；漏 builder → 绕过校验/构造；漏 tenantQuery → platform_admin 400 tenant_id_required。
  assert.match(bodyAfter('export function updateChannelTestTemplate'), /adminPut/, 'update 用 PUT');
  assert.match(bodyAfter('export function deleteChannelTestTemplate'), /adminDelete/, 'delete 用 DELETE');
  assert.match(bodyAfter('export function createChannelTestTemplate'), /buildChannelTestTemplateBody/, 'create 用 builder');
  assert.match(bodyAfter('export function updateChannelTestTemplate'), /buildChannelTestTemplateBody/, 'update 用 builder');
  assert.match(bodyAfter('export function createChannelTestTemplate'), /tenantQuery\(tenantId\)/, 'create 带 tenantQuery');
  assert.match(bodyAfter('export function deleteChannelTestTemplate'), /tenantQuery\(tenantId\)/, 'delete 带 tenantQuery');
});

test('TestHelpers_HttpVerbs', () => {
  assert.match(bodyAfter('async function adminPut'), /method:\s*'PUT'/, 'adminPut 发 PUT');
  assert.match(bodyAfter('async function adminDelete'), /method:\s*'DELETE'/, 'adminDelete 发 DELETE');
});
