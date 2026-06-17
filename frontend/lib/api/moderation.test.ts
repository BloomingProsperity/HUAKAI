// 内容审核黑名单 client 强测试。两部分:
//  (1) 纯逻辑(moderation-bulk.ts)——哈希格式校验 + 批量行解析,真行为风险,变异自检;
//  (2) 源码接线断言(moderation.ts)——删除端点的【租户作用域】、批量端点、DELETE 的鉴权头。
//      moderation.ts 含 ./client 导入,node strip-types 无法直接 import,故照既有
//      dashboard-real-api.test.ts 范式读源码文本断言关键接线。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { isValidHashHex, parseBulkLines } from './moderation-bulk.ts';

const ROOT = process.cwd();
const moderationSrc = readFileSync(join(ROOT, 'lib/api/moderation.ts'), 'utf8');

// ── (1) isValidHashHex:64 位小写 hex,锚点 + 长度 + 字符集三重约束 ──────────

test('TestIsValidHashHex_AcceptsExactly64LowerHex', () => {
  // 捕获:把 {64} 放宽成 {1,} 或去掉 ^$ 锚点 → 下面任一断言 red。
  assert.equal(isValidHashHex('a'.repeat(64)), true, '64 位小写 hex 应通过');
  assert.equal(isValidHashHex('0123456789abcdef'.repeat(4)), true, '混合小写 hex 应通过');
});

test('TestIsValidHashHex_RejectsUppercaseShortLongAndNonHex', () => {
  // 大写:若误用 /i 标志会放行 → red(后端只收小写)。
  assert.equal(isValidHashHex('A'.repeat(64)), false, '大写必须拒');
  // 过短:若 {64}→{1,} 会放行 → red。
  assert.equal(isValidHashHex('a'.repeat(63)), false, '63 位必须拒');
  // 过长:若去掉尾锚 $ 会放行前 64 位 → red(这是 $ 锚点的判别式)。
  assert.equal(isValidHashHex('a'.repeat(65)), false, '65 位必须拒');
  // 非 hex 字符:若字符集写错会放行 → red。
  assert.equal(isValidHashHex('g'.repeat(64)), false, '非 hex 字符必须拒');
  assert.equal(isValidHashHex(''), false, '空串必须拒');
});

// ── (1) parseBulkLines:去空行 + 去首尾空白 + 保序 ───────────────────────

test('TestParseBulkLines_DropsBlankAndTrims', () => {
  const out = parseBulkLines('k1\n\n  k2  \n   \nk3\n');
  // 判别式 A:若漏 filter(空行)→ out 含 '' → length≠3 red(防提交空项到后端)。
  // 判别式 B:若漏 trim → 含 '  k2  ' → 精确相等 red。
  // 判别式 C:保序 —— 顺序错 red。
  assert.deepEqual(out, ['k1', 'k2', 'k3'], '应去空行/去空白/保序');
});

test('TestParseBulkLines_AllBlankYieldsEmpty', () => {
  // 全空白输入必须得空数组(否则会 POST 一批空项)。
  assert.deepEqual(parseBulkLines('\n  \n\t\n'), [], '全空白应得空数组');
});

// ── (2) 源码接线:删除端点必须带租户作用域(防跨租户误删/作用域混淆)─────────

test('TestDeleteEndpoints_AreTenantScoped', () => {
  // 判别式:若把 deleteKeyword/deleteHash 写成 `keywords/${id}` 漏掉 tenantQuery,
  // 下面断言 red —— 这是租户隔离的关键(删除必须落在指定租户 scope)。
  assert.match(
    moderationSrc,
    /keywords\/\$\{id\}\$\{tenantQuery\(tenantId\)\}/,
    'deleteKeyword 必须带 ?tenant_id(tenantQuery)',
  );
  assert.match(
    moderationSrc,
    /hashes\/\$\{id\}\$\{tenantQuery\(tenantId\)\}/,
    'deleteHash 必须带 ?tenant_id(tenantQuery)',
  );
});

test('TestBulkEndpoints_Present', () => {
  // 判别式:批量端点路径写错(漏 /bulk)→ red。
  assert.ok(moderationSrc.includes('/keywords/bulk'), '关键词批量端点缺失');
  assert.ok(moderationSrc.includes('/hashes/bulk'), '哈希批量端点缺失');
});

test('TestAdminDelete_AttachesBearerAuth', () => {
  // 判别式:若 adminDelete 漏挂 Authorization 头 → 管理端 DELETE 会 401/无鉴权 → red。
  assert.match(moderationSrc, /method:\s*'DELETE'/, 'adminDelete 应发 DELETE');
  assert.match(moderationSrc, /Authorization:\s*`Bearer \$\{token\}`/, 'DELETE 必须挂 admin Bearer 头');
});
