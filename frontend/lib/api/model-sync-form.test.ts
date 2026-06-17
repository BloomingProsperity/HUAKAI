// 模型目录同步触发切片强测试。
// 纯逻辑（reason 码点校验 / 请求体构造 / 结果汇总，直接 strip-types 单测）+ adminModelSync.ts 接线断言（端点+动词+builder+无 tenant_id）。
// 每条断言可一句话说出抓的回归；均经变异实测转红再还原（CLAUDE.md §14）。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  MAX_SYNC_REASON_LEN,
  buildModelSyncBody,
  syncHadChanges,
  validateModelSyncReason,
  vendorChangeCount,
  type ModelSyncResultItemShape,
} from './model-sync-form.ts';

const ROOT = process.cwd();
const adminSrc = readFileSync(join(ROOT, 'lib/api/adminModelSync.ts'), 'utf8');

// 取某函数段：marker 起到【下一个顶层声明】前止（最后一个声明则到 EOF）。端点断言锚定引号定界符。
function bodyAfter(marker: string): string {
  const start = adminSrc.indexOf(marker);
  if (start < 0) throw new Error('找不到 ' + marker);
  const rest = adminSrc.slice(start + marker.length);
  const m = rest.match(/\n(?:export function |async function |function |export interface |export const )/);
  const end = m ? start + marker.length + (m.index ?? rest.length) : adminSrc.length;
  return adminSrc.slice(start, end);
}

function item(over: Partial<ModelSyncResultItemShape>): ModelSyncResultItemShape {
  return { vendor: 'v', added: 0, updated: 0, reactivated: 0, disabled: 0, unchanged: 0, snapshot_bumps: 0, ...over };
}

// ── 校验：reason 可选、trim、按码点 ≤200 ────────────────────────────────

test('TestValidateReason_LengthAndOptional', () => {
  // 钉死常量到后端字面量：边界 fixture 都引用 MAX_SYNC_REASON_LEN，若常量 up-drift（如 300）前端会放行
  // 后端 model_sync_handler.go:86 仍 400 invalid_reason 的 201–300 码点 reason；此断言锁住与后端 200 一致。
  assert.equal(MAX_SYNC_REASON_LEN, 200, '上限必须钉死为后端 utf8.RuneCount>200 的 200');
  assert.equal(validateModelSyncReason(''), null, '空 reason 合法（后端自填默认）');
  assert.equal(validateModelSyncReason('上游发布新模型'), null, '普通 reason 合法');
  // 边界：恰好 200 合法，201 报错（判别 > 200 守门；若写成 >=200 则 200 误拒 → red）。
  assert.equal(validateModelSyncReason('x'.repeat(MAX_SYNC_REASON_LEN)), null, '恰好 200 合法');
  assert.ok(validateModelSyncReason('x'.repeat(MAX_SYNC_REASON_LEN + 1)), '201 应报错');
});

test('TestValidateReason_CodePointsNotUtf16', () => {
  // 判别：按【码点】计长而非 UTF-16 .length。150 个 emoji = 码点 150（合法）但 UTF-16 长 300（>200）。
  // 若实现误用 .length，则此例被错拒 → red。
  assert.equal(validateModelSyncReason('😀'.repeat(150)), null, '150 emoji（码点 150）应合法');
  // 反向：201 个 emoji = 码点 201 → 必须报错（防止把码点判定写反成「总放行」）。
  assert.ok(validateModelSyncReason('😀'.repeat(MAX_SYNC_REASON_LEN + 1)), '201 emoji 应报错');
});

test('TestValidateReason_TrimsBeforeCounting', () => {
  // 判别：先 trim 再计长。两侧各 10 空格 + 200 实字符：不 trim 则 220>200 误拒；trim 后 200 合法。
  const padded = ' '.repeat(10) + 'x'.repeat(MAX_SYNC_REASON_LEN) + ' '.repeat(10);
  assert.equal(validateModelSyncReason(padded), null, 'trim 后恰好 200 应合法');
});

// ── 请求体构造：空 → 省略 reason 键；非空 → trim 后 { reason } ──────────

test('TestBuildBody_OmitsEmptyReason', () => {
  // 判别：空 reason 必须【省略键】（让后端填 admin_manual），不能塞 reason:''。
  assert.deepEqual(buildModelSyncBody(''), {}, '空 reason → {}');
  assert.deepEqual(buildModelSyncBody('   '), {}, '纯空白 reason → {}');
  assert.equal('reason' in buildModelSyncBody(''), false, '空时不得含 reason 键');
});

test('TestBuildBody_TrimsNonEmpty', () => {
  // 判别：非空 reason trim 后带键。漏 trim → 发出带空格脏值；漏带键 → 后端拿不到原因。
  assert.deepEqual(buildModelSyncBody('  对账  '), { reason: '对账' }, '非空 → trim 后 { reason }');
});

// ── 结果汇总 ────────────────────────────────────────────────────────────

test('TestVendorChangeCount', () => {
  // 判别：变更数 = 新增+更新+复活+停用，且【不含】unchanged / snapshot_bumps。
  // 四项取互异非零值，漏任一→和变；unchanged/snapshot 取非零，若被误纳入→和变。
  const c = vendorChangeCount(item({ added: 1, updated: 2, reactivated: 3, disabled: 4, unchanged: 99, snapshot_bumps: 88 }));
  assert.equal(c, 10, '1+2+3+4=10，排除 unchanged(99)/snapshot(88)');
});

test('TestSyncHadChanges', () => {
  const none = { total_added: 0, total_updated: 0, total_disabled: 0, results: [] };
  assert.equal(syncHadChanges(none), false, '全 0 → 无变更');
  // 三项各自单独非零都应判为有变更（漏任一项 → 该例 red）。
  assert.equal(syncHadChanges({ ...none, total_added: 3 }), true, '仅新增 → 有变更');
  assert.equal(syncHadChanges({ ...none, total_updated: 2 }), true, '仅更新 → 有变更');
  assert.equal(syncHadChanges({ ...none, total_disabled: 1 }), true, '仅停用 → 有变更');
});

// ── adminModelSync.ts 接线：端点 + POST + builder + 无 tenant_id ────────

test('TestEndpoint_TriggerWiring', () => {
  const body = bodyAfter('export function triggerModelSync');
  // 判别：端点路径写错 → 打到错误后端路由。锚定单引号定界符，避免尾部追加 typo 非判别。
  assert.match(body, /'\/admin\/v1\/model-sync'/, '端点路径');
  // 判别：动词用错 → 405。
  assert.match(body, /apiPost/, '用 apiPost（POST）');
  // 判别：漏 builder → 绕过 trim/空省略构造。
  assert.match(body, /buildModelSyncBody/, '经 buildModelSyncBody 构造请求体');
  // 判别：误加 tenant_id → 全局目录端点不接受，且语义错（platform_admin 全局，无租户维度）。
  assert.doesNotMatch(body, /tenant_id/, '不得带 tenant_id');
});
