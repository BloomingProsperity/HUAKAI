// 公告 CRUD 切片强测试。
// 纯逻辑（校验/跨字段 expires>published/请求体构造/DisallowUnknownFields 键集，直接 strip-types 单测）
// + adminAnnouncements.ts 接线断言（4 端点路径+动词+builder+create tenant 在 body / 其余在 query）。
// 每条断言可一句话说出抓的回归；均经变异实测转红再还原（CLAUDE.md §14）。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  buildCreateBody,
  buildUpdateBody,
  validateAnnouncementForm,
  type AnnouncementFormInput,
} from './announcement-form.ts';

const ROOT = process.cwd();
const adminSrc = readFileSync(join(ROOT, 'lib/api/adminAnnouncements.ts'), 'utf8');

// 取某函数段：marker 起到【下一个顶层声明】前止。端点断言锚定引号/${}定界符。
function bodyAfter(marker: string): string {
  const start = adminSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = adminSrc.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |async function |function |export interface |export const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : adminSrc.length;
  return adminSrc.slice(start, end);
}

function validBase(): AnnouncementFormInput {
  return { title: '维护通知', body: '今晚维护', severity: 'info', active: true, published_at_raw: '', expires_at_raw: '' };
}

// ── 校验：title/body 必填 ──────────────────────────────────────────────

test('TestValidate_TitleBodyRequired', () => {
  assert.equal(validateAnnouncementForm(validBase()), null, '合法基线应通过');
  assert.ok(validateAnnouncementForm({ ...validBase(), title: '' }), '空 title 应报错');
  assert.ok(validateAnnouncementForm({ ...validBase(), title: '   ' }), '纯空白 title 应报错');
  assert.ok(validateAnnouncementForm({ ...validBase(), body: '' }), '空 body 应报错');
  assert.ok(validateAnnouncementForm({ ...validBase(), body: '  ' }), '纯空白 body 应报错');
});

// ── 校验：severity 白名单 ──────────────────────────────────────────────

test('TestValidate_Severity', () => {
  // 判别：逐条覆盖三档合法值（任一从 SEVERITIES 缩减→该例 red）。
  assert.equal(validateAnnouncementForm({ ...validBase(), severity: 'info' }), null, 'info 合法');
  assert.equal(validateAnnouncementForm({ ...validBase(), severity: 'warning' }), null, 'warning 合法');
  assert.equal(validateAnnouncementForm({ ...validBase(), severity: 'critical' }), null, 'critical 合法');
  // 判别：漏白名单 → 任意值放行 → 后端 400 invalid。
  assert.ok(validateAnnouncementForm({ ...validBase(), severity: 'urgent' }), 'urgent 应报错');
  assert.ok(validateAnnouncementForm({ ...validBase(), severity: 'INFO' }), '大写 INFO 应报错（后端区分大小写）');
});

// ── 校验：时间合法性 + 跨字段 expires>published ────────────────────────

test('TestValidate_TimeCrossField', () => {
  const pub = '2026-01-01T00:00:00Z';
  const expAfter = '2026-01-02T00:00:00Z';
  const expBefore = '2025-12-31T00:00:00Z';
  // 合法：两者都给且 expires 晚于 published。
  assert.equal(
    validateAnnouncementForm({ ...validBase(), published_at_raw: pub, expires_at_raw: expAfter }),
    null,
    'expires 晚于 published 应通过',
  );
  // 判别：漏跨字段守门 → expires 早于 published 放行 → 后端 400（ExpiresAt.After 失败）。
  assert.ok(
    validateAnnouncementForm({ ...validBase(), published_at_raw: pub, expires_at_raw: expBefore }),
    'expires 早于 published 应报错',
  );
  // 判别：严格大于 → 相等也应报错。
  assert.ok(
    validateAnnouncementForm({ ...validBase(), published_at_raw: pub, expires_at_raw: pub }),
    'expires == published 应报错',
  );
  // 判别：非法时间串应报错。
  assert.ok(validateAnnouncementForm({ ...validBase(), published_at_raw: 'not-a-date' }), '非法 published 应报错');
  assert.ok(validateAnnouncementForm({ ...validBase(), expires_at_raw: 'not-a-date' }), '非法 expires 应报错');
  // 仅 expires（published 留空，后端取 now）→ 前端不拦，交后端兜底。
  assert.equal(validateAnnouncementForm({ ...validBase(), expires_at_raw: expAfter }), null, '仅 expires 前端不拦');
});

// ── 请求体构造：create（tenant 在 body、含已知字段、禁多余键）─────────

test('TestBuildCreateBody', () => {
  // 无时间：精确键集（DisallowUnknownFields → 多一键即后端 400 invalid_json；少 tenant_id → 语义错）。
  const noTime = buildCreateBody({ ...validBase(), title: '  T  ', body: '  B  ' }, 7);
  assert.deepEqual(Object.keys(noTime).sort(), ['active', 'body', 'severity', 'tenant_id', 'title'], 'create 精确键集');
  assert.equal(noTime.tenant_id, 7, 'tenant_id 放入 body');
  // 判别：再用不同 tenant 证 tenant_id 取自【入参】而非硬编码——单值断言会被「硬编码成同值」骗过。
  assert.equal(buildCreateBody(validBase(), 13).tenant_id, 13, 'tenant_id 取自入参（13）');
  assert.equal(noTime.title, 'T', 'title trim');
  assert.equal(noTime.body, 'B', 'body trim');
  assert.equal('id' in noTime, false, 'create 不得带 id');
  // 给时间：用 datetime-local 形（无 Z 无秒）证 toRFC3339 真做规整——恒等透传会留下无 Z/秒 → red。
  const withTime = buildCreateBody({ ...validBase(), published_at_raw: '2026-01-01T08:30', expires_at_raw: '2026-01-02T09:45' }, 1);
  assert.deepEqual(
    Object.keys(withTime).sort(),
    ['active', 'body', 'expires_at', 'published_at', 'severity', 'tenant_id', 'title'],
    'create 带时间键集',
  );
  assert.match(withTime.published_at as string, /T\d{2}:\d{2}:\d{2}.*Z$/, 'published_at 规整为带秒的 RFC3339 UTC（非恒等透传）');
  assert.equal(new Date(withTime.published_at as string).getTime(), new Date('2026-01-01T08:30').getTime(), 'published_at 保持同一时刻');
});

// ── 请求体构造：update（无 tenant_id/id；expires 空→显式 null 清除）───

test('TestBuildUpdateBody', () => {
  const body = buildUpdateBody({ ...validBase(), title: '  T  ', expires_at_raw: '' });
  // 判别：update 体禁带 tenant_id（tenant 在 query）/ id。
  assert.equal('tenant_id' in body, false, 'update 不得带 tenant_id');
  assert.equal('id' in body, false, 'update 不得带 id');
  assert.equal(body.title, 'T', 'title trim');
  // 判别：expires 留空 → 显式 null（清除），不能省略也不能空串。
  assert.equal(body.expires_at, null, '空 expires → 显式 null 清除');
  assert.equal('published_at' in body, false, '空 published 省略（保持不变）');
  // 给 expires → 值；给 published → 含键。
  const body2 = buildUpdateBody({ ...validBase(), published_at_raw: '2026-01-01T08:30', expires_at_raw: '2026-01-02T09:45' });
  assert.match(body2.expires_at as string, /T\d{2}:\d{2}:\d{2}.*Z$/, 'expires 规整为带秒 RFC3339 UTC（非恒等透传）');
  assert.equal(new Date(body2.expires_at as string).getTime(), new Date('2026-01-02T09:45').getTime(), 'expires 保持同一时刻');
  assert.equal('published_at' in body2, true, '给 published 则含键');
});

// ── adminAnnouncements.ts 接线：4 端点路径 + 动词 + builder + tenant 位置 ──

test('TestEndpoints_PathsVerbsBuilder', () => {
  // list：GET 静态路径 + tenant_id 入 query。
  assert.match(bodyAfter('export function listAnnouncements'), /apiGet<AnnouncementListResponse>\('\/v1\/admin\/announcements'/, 'list GET 路径');
  // 锚定到调用点接线 `tenant_id: opts.tenant_id`，而非 opts 类型标注里的 `tenant_id: number`（否则删查询接线仍绿）。
  assert.match(bodyAfter('export function listAnnouncements'), /tenant_id: opts\.tenant_id/, 'list tenant 入 query（锚定接线）');
  // create：POST 静态路径（无 tenantQuery）+ buildCreateBody(input, tenantId)（tenant 在 body）。
  assert.match(bodyAfter('export function createAnnouncement'), /apiPost<Announcement>\('\/v1\/admin\/announcements'/, 'create POST 路径');
  assert.match(bodyAfter('export function createAnnouncement'), /buildCreateBody\(input, tenantId\)/, 'create 用 builder 且传 tenantId');
  assert.doesNotMatch(bodyAfter('export function createAnnouncement'), /tenantQuery/, 'create URL 不带 tenantQuery（tenant 在 body）');
  // update：PUT + ${id}${tenantQuery} + buildUpdateBody。
  assert.match(bodyAfter('export function updateAnnouncement'), /adminPut/, 'update 用 PUT');
  assert.match(bodyAfter('export function updateAnnouncement'), /\/v1\/admin\/announcements\/\$\{id\}\$\{tenantQuery/, 'update 路径带 id+tenantQuery');
  assert.match(bodyAfter('export function updateAnnouncement'), /buildUpdateBody\(input\)/, 'update 用 builder');
  // delete：DELETE + ${id}${tenantQuery}。
  assert.match(bodyAfter('export function deleteAnnouncement'), /adminDelete/, 'delete 用 DELETE');
  assert.match(bodyAfter('export function deleteAnnouncement'), /\/v1\/admin\/announcements\/\$\{id\}\$\{tenantQuery/, 'delete 路径带 id+tenantQuery');
});

test('TestHelpers_HttpVerbs', () => {
  assert.match(bodyAfter('async function adminPut'), /method:\s*'PUT'/, 'adminPut 发 PUT');
  assert.match(bodyAfter('async function adminDelete'), /method:\s*'DELETE'/, 'adminDelete 发 DELETE');
});
