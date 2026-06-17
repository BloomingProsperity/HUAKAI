// 用量分析 2 个零覆盖只读 GET 接线强测试。
// 纯逻辑（参数构造，直接 strip-types 单测）+ adminOps.ts 接线断言（路径 + builder + GET 动词）。
// 每条断言可一句话说出抓的回归；均经变异实测转红再还原（CLAUDE.md §14）。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { buildByBucketParams, buildCountsParams } from './usage-analytics-form.ts';

const ROOT = process.cwd();
const adminSrc = readFileSync(join(ROOT, 'lib/api/adminOps.ts'), 'utf8');

// 取某函数段：marker 起到【下一个顶层声明】前止。端点断言锚定引号定界符，保持判别性。
function bodyAfter(marker: string): string {
  const start = adminSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = adminSrc.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |async function |function |export interface |export const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : adminSrc.length;
  return adminSrc.slice(start, end);
}

// ── buildByBucketParams：window/bucket/limit 映射 ────────────────────────

test('TestBuildByBucketParams', () => {
  const q = buildByBucketParams('day', '7d', { limit: 5 });
  // 判别：bucket 必须映到 'bucket' 键、window 映到 'window' 键；若 builder 把二者写反，
  // window='day'/bucket='7d' → 后端 invalid_bucket(400) → 此处红。
  assert.equal(q.bucket, 'day', "bucket 入参映到 bucket 键");
  assert.equal(q.window, '7d', 'window 入参映到 window 键');
  // 判别：limit 须透传。
  assert.equal(q.limit, 5, 'limit 透传');
  // 判别：缺省 limit → undefined（apiGet 省略），漏处理会发出 limit=undefined 字面或 NaN。
  assert.equal(buildByBucketParams('hour', '24h').limit, undefined, '缺省 limit 为 undefined');
});

// ── buildCountsParams：from/to/tenant_id 映射（顺序不可换）────────────────

test('TestBuildCountsParams', () => {
  const from = '2026-01-01T00:00:00Z';
  const to = '2026-01-08T00:00:00Z';
  const q = buildCountsParams(from, to, { tenant_id: 7 });
  // 判别：from/to 不可互换（后端要求 to>from）；若 builder 写反，from 会拿到较晚值 → 后端 invalid_window → 红。
  assert.equal(q.from, from, 'from 入参映到 from 键(不与 to 互换)');
  assert.equal(q.to, to, 'to 入参映到 to 键');
  // 判别：tenant_id 须原样透传；fixture 用非 1 值(7)，避免硬编码 tenant_id:1 的回归绿掉(§14 假绿陷阱)。
  assert.equal(q.tenant_id, 7, 'tenant_id 原样透传');
  // 判别：缺省 tenant_id → undefined（apiGet 省略 → 平台 admin 全租户聚合）。
  assert.equal(buildCountsParams(from, to).tenant_id, undefined, '缺省 tenant_id 为 undefined');
});

// ── adminOps.ts 接线：路径 + builder + GET 动词 ──────────────────────────

test('TestEndpoints_Paths', () => {
  // 判别：端点路径写错 → 打到错误后端路由(404/405) → 红。路径锚定引号定界符避免尾部 typo 非判别。
  assert.match(
    bodyAfter('export function getPerfMetricsByBucket'),
    /'\/v1\/admin\/usage\/perf-metrics\/by-bucket'/,
    'by-bucket 路径',
  );
  assert.match(
    bodyAfter('export function getProviderAccountCounts'),
    /'\/v1\/admin\/usage\/provider-account-counts'/,
    'provider-account-counts 路径',
  );
});

test('TestEndpoints_UsesBuilderAndGet', () => {
  const byBucket = bodyAfter('export function getPerfMetricsByBucket');
  const counts = bodyAfter('export function getProviderAccountCounts');
  // 判别：漏用 builder → 绕过参数映射(可能漏键/写反) → 红。
  assert.match(byBucket, /buildByBucketParams\(bucket, window, opts\)/, 'by-bucket 用 builder 构造参数');
  assert.match(counts, /buildCountsParams\(from, to, opts\)/, 'counts 用 builder 构造参数');
  // 判别：只读 GET 误用 apiPost → 后端 405 → 红。
  assert.match(byBucket, /apiGet</, 'by-bucket 用 GET');
  assert.match(counts, /apiGet</, 'counts 用 GET');
});
