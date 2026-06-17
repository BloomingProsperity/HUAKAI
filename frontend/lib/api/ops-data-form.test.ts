// 运维数据面切片强测试。
// 纯逻辑（查询构造/守门/编码，直接 strip-types 单测）+ adminOpsData.ts 接线断言（六端点路径 + 方法 + key 编码）。
// 每条断言可一句话说出抓的回归；均经变异实测转红再还原（CLAUDE.md §14）。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  buildAuditEventsQuery,
  clampAuditLimit,
  encodeCacheKey,
  isValidEventKind,
  validateDlqId,
} from './ops-data-form.ts';

const ROOT = process.cwd();
const adminSrc = readFileSync(join(ROOT, 'lib/api/adminOpsData.ts'), 'utf8');

// 取某函数段：marker 起到【下一个顶层声明】前止（不用首个 \n}，函数/接口混排时不可靠）。
// 端点断言均锚定引号/${} 定界符，故即便圈进下一段前置注释也保持判别性。
function bodyAfter(marker: string): string {
  const start = adminSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = adminSrc.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |async function |function |export interface |export const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : adminSrc.length;
  return adminSrc.slice(start, end);
}

// ── clampAuditLimit：[1,200]，非整数/缺省→100 ───────────────────────────

test('TestClampAuditLimit', () => {
  // 判别：漏钳制 → 250 原样发出 → 后端 400 invalid_limit。
  assert.equal(clampAuditLimit(250), 200, '>200 钳到 200');
  assert.equal(clampAuditLimit(50), 50, '区间内原样');
  assert.equal(clampAuditLimit(0), 1, '0 钳到下界 1');
  // 判别：漏「非整数→默认」→ NaN/null 漏网。
  assert.equal(clampAuditLimit(null), 100, '缺省→默认 100');
  assert.equal(clampAuditLimit(1.5), 100, '非整数→默认 100');
});

// ── isValidEventKind：DLQ handler 白名单 ────────────────────────────────

test('TestIsValidEventKind', () => {
  // 判别：漏白名单 → 任意串当合法 → 打到后端静默返 0 行（迷惑空表）。
  assert.equal(isValidEventKind('usage_record'), true, '合法 EventKind');
  assert.equal(isValidEventKind('billing_event_replica'), true, '合法 EventKind');
  assert.equal(isValidEventKind('bogus'), false, '非法名应拒');
  assert.equal(isValidEventKind(''), false, '空名应拒');
});

// ── validateDlqId：正整数 ────────────────────────────────────────────────

test('TestValidateDlqId', () => {
  // 判别：漏正整数守门 → 0/负/小数放行 → 后端 400 invalid_dlq_id。
  assert.ok(validateDlqId(0), '0 应报错');
  assert.ok(validateDlqId(-1), '负应报错');
  assert.ok(validateDlqId(1.5), '非整数应报错');
  assert.equal(validateDlqId(5), null, '正整数应通过');
});

// ── encodeCacheKey：URL 编码 DELETE 路径段 ──────────────────────────────

test('TestEncodeCacheKey', () => {
  // 判别：漏编码 → key 内 '/'、':' 破坏 DELETE 路由 → 删错条目/404。
  assert.equal(encodeCacheKey('a/b:c'), 'a%2Fb%3Ac', "含 '/' ':' 的 key 须编码");
  assert.equal(encodeCacheKey('plain'), 'plain', '普通 key 不变');
});

// ── buildAuditEventsQuery：省略空/all + 钳制 limit ──────────────────────

test('TestBuildAuditEventsQuery', () => {
  const q = buildAuditEventsQuery({
    tenant_id: 7,
    event_class: 'all',
    severity: 'error',
    from: '',
    cursor: 'abc',
    limit: 250,
  });
  // 判别：调用方 tenant_id 须原样透传；fixture 用非 1 值（7），否则硬编码 tenant_id:1 的回归也会绿（§14 假绿陷阱）。
  assert.equal(q.tenant_id, 7, 'tenant_id 原样透传');
  // 判别：'all'/'' 视为不过滤 → 省略；漏处理 → 'all' 当真值发出 → 后端按字面过滤返空。
  assert.equal(q.event_class, undefined, "event_class='all' 应省略");
  assert.equal(q.from, undefined, '空 from 应省略');
  // 判别：已设过滤须保留。
  assert.equal(q.severity, 'error', 'severity 保留');
  assert.equal(q.cursor, 'abc', 'cursor 保留');
  // 判别：漏钳制 → 250 原样 → 后端 400。
  assert.equal(q.limit, 200, 'limit 钳到 200');
});

// ── adminOpsData.ts 接线：六端点路径 + 方法 + key 编码 ──────────────────

test('TestEndpoints_Paths', () => {
  // 判别：端点路径写错 → 打到错误后端路由 → red。路径锚定引号/${}定界符避免尾部追加 typo 非判别。
  assert.match(bodyAfter('export function listAuditEvents'), /'\/admin\/v1\/audit-events'/, 'audit-events 路径');
  assert.match(bodyAfter('export function listDLQ'), /\/admin\/v1\/dlq\/\$\{handler\}`/, 'dlq list 路径');
  assert.match(bodyAfter('export function replayDLQ'), /\/admin\/v1\/dlq\/\$\{id\}\/replay`/, 'dlq replay 路径');
  assert.match(bodyAfter('export function replayUsageRecordDLQ'), /\/admin\/v1\/usage-record-dlq\/\$\{id\}\/replay`/, 'usage-record-dlq replay 路径');
  assert.match(bodyAfter('export function getL2CacheStats'), /'\/admin\/v1\/cache\/l2\/stats'/, 'cache stats 路径');
  assert.match(bodyAfter('export function evictL2CacheKey'), /\/admin\/v1\/cache\/l2\/\$\{encodeCacheKey\(key\)\}`/, 'cache delete 路径(含 encodeCacheKey)');
});

test('TestEndpoints_VerbsAndEncoding', () => {
  // 判别：方法用错 → 后端 405 → red。
  assert.match(bodyAfter('export function listAuditEvents'), /apiGet/, 'audit list 用 GET');
  assert.match(bodyAfter('export function listDLQ'), /apiGet/, 'dlq list 用 GET');
  assert.match(bodyAfter('export function getL2CacheStats'), /apiGet/, 'cache stats 用 GET');
  assert.match(bodyAfter('export function replayDLQ'), /apiPost/, 'dlq replay 用 POST');
  assert.match(bodyAfter('export function replayUsageRecordDLQ'), /apiPost/, 'usage replay 用 POST');
  // 判别：evict 漏用 adminDelete(DELETE 动词)或漏 encodeCacheKey(不编码破坏路由) → red。
  assert.match(bodyAfter('export function evictL2CacheKey'), /adminDelete/, 'cache evict 用 DELETE');
  assert.match(bodyAfter('export function evictL2CacheKey'), /encodeCacheKey\(key\)/, 'cache evict 对 key 编码');
  // 判别：audit list 漏用 builder → 绕过省略/钳制 → red。
  assert.match(bodyAfter('export function listAuditEvents'), /buildAuditEventsQuery\(input\)/, 'audit list 用 builder');
});

test('TestAdminDelete_HttpVerb', () => {
  // 判别：adminDelete 助手动词被改错 → red。
  assert.match(bodyAfter('async function adminDelete'), /method:\s*'DELETE'/, 'adminDelete 发 DELETE');
});
