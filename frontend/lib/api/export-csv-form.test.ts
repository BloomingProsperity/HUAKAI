// admin CSV 导出接线强测试。
// 纯逻辑（区间校验 + URL 构造，直接 strip-types 单测）+ adminExports.ts 接线断言（validate + buildUrl + downloadCsv）。
// 每条断言可一句话说出抓的回归；均经变异实测转红再还原（CLAUDE.md §14）。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { buildExportUrl, validateExportRange } from './export-csv-form.ts';

const ROOT = process.cwd();
const adminSrc = readFileSync(join(ROOT, 'lib/api/adminExports.ts'), 'utf8');

function bodyAfter(marker: string): string {
  const start = adminSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = adminSrc.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |async function |function |export interface |export const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : adminSrc.length;
  return adminSrc.slice(start, end);
}

// ── buildExportUrl：路径(kind) + query(from/to/status) ───────────────────────

test('TestBuildExportUrl', () => {
  const from = '2026-01-01T00:00:00Z';
  const to = '2026-02-01T00:00:00Z';
  const u = new URL(buildExportUrl('payments', { from, to, status: 'paid' }), 'http://x');
  // 判别：路径 kind 写错 → 打到错误导出端点(404)。
  assert.equal(u.pathname, '/v1/admin/payments/export.csv', 'payments 路径');
  // 判别：from/to 须正确映射(不互换、不丢)。URLSearchParams 解码后比对，避开 : 编码差异。
  assert.equal(u.searchParams.get('from'), from, 'from 映射');
  assert.equal(u.searchParams.get('to'), to, 'to 映射');
  // 判别：给定 status 时附加。
  assert.equal(u.searchParams.get('status'), 'paid', 'status 附加');
  // 判别：各 kind 路径正确（kind 直接拼进路径）。
  assert.equal(new URL(buildExportUrl('usage', { from, to }), 'http://x').pathname, '/v1/admin/usage/export.csv', 'usage 路径');
  assert.equal(new URL(buildExportUrl('refunds', { from, to }), 'http://x').pathname, '/v1/admin/refunds/export.csv', 'refunds 路径');
  // 判别：未给 status 时省略（不发 status=undefined/空字符串）。
  assert.equal(new URL(buildExportUrl('orders', { from, to }), 'http://x').searchParams.has('status'), false, '未给 status 省略');
});

// ── validateExportRange：必填 + from<=to + <=366 天 ──────────────────────────

test('TestValidateExportRange', () => {
  const ok = '2026-01-01T00:00:00Z';
  // 判别：from/to 必填。
  assert.ok(validateExportRange('', ok), '空 from 应报错');
  assert.ok(validateExportRange(ok, ''), '空 to 应报错');
  // 判别：严格 RFC3339——后端 time.Parse(RFC3339) 会拒以下，前端不能用 Date.parse 宽松放行
  //（无偏移串还会按本地 TZ 解析致窗口算偏）。变异: 改回 Date.parse → 这些放行 → 红。
  assert.ok(validateExportRange('2026-01-01T00:00:00', ok), '无时区(offset-less)应拒');
  assert.ok(validateExportRange('2026-01-01', ok), '纯日期应拒');
  assert.ok(validateExportRange('2026-01-01 00:00:00Z', ok), '空格分隔(非 T)应拒');
  // 判别：合法 RFC3339 通过。
  assert.equal(validateExportRange(ok, ok), null, '合法 RFC3339 from==to 应通过');
  // 判别：from>to 应拒（后端 from.After(to)）。
  assert.ok(validateExportRange('2026-02-01T00:00:00Z', '2026-01-01T00:00:00Z'), 'from>to 应报错');
  // 判别：边界——恰好 366 天应通过(后端 > maxExportWindow 才拒，等于不拒)。
  // 2026-01-01 → 2027-01-02 = 366 天(2026 非闰年 365 天 +1 天)。
  assert.equal(validateExportRange('2026-01-01T00:00:00Z', '2027-01-02T00:00:00Z'), null, '恰好 366 天应通过');
  // 判别：366 天 + 1 秒应拒。变异 >→>= 或 366→365 会让上面"恰好 366 天"红、此条仍红，边界被钉死。
  assert.ok(validateExportRange('2026-01-01T00:00:00Z', '2027-01-02T00:00:01Z'), '366 天+1 秒应报错');
});

// ── client.ts downloadCsv：X-Truncated 信号不丢（财务导出截断须提示）──────────

test('TestDownloadCsv_HandlesTruncation', () => {
  const clientSrc = readFileSync(join(ROOT, 'lib/api/client.ts'), 'utf8');
  const start = clientSrc.indexOf('export async function downloadCsv');
  const body = start < 0 ? '' : clientSrc.slice(start, start + 1200);
  // 判别：downloadCsv 须读 X-Truncated 头；漏读 → 截断的 payments/refunds 导出静默无警告。
  assert.match(body, /headers\.get\('X-Truncated'\)/, 'downloadCsv 读 X-Truncated 头');
  // 判别：截断时须抛 export_truncated 让页面提示（与 usage.ts 一致）。
  assert.match(body, /export_truncated/, 'downloadCsv 截断抛 export_truncated');
});

// ── adminExports.ts 接线：validate + buildUrl + downloadCsv ──────────────────

test('TestEndpoints_DownloadWiring', () => {
  const dl = bodyAfter('export function downloadAdminExport');
  // 判别：导出前须先 validateExportRange（fail-fast）；漏校验 → 发后端必拒的请求 + validator 悬空。
  assert.match(dl, /validateExportRange\(params\.from, params\.to\)/, '下载前先校验区间');
  // 判别：须经 buildExportUrl 构造 URL（漏则路径/query 可能错）。
  assert.match(dl, /buildExportUrl\(kind, params\)/, '用 buildExportUrl 构造 URL');
  // 判别：须经 downloadCsv 鉴权下载（漏则无 admin token 或不触发下载）。
  assert.match(dl, /downloadCsv\(/, '用 downloadCsv 鉴权下载');
});
