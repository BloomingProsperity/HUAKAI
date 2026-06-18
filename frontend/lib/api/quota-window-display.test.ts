// 多维 /quota 窗口展示逻辑测试。每条断言一句话说清抓的回归；均经变异实测转红。
import assert from 'node:assert/strict';
import test from 'node:test';

import { quotaMetricLabel, quotaWindowKey } from './quota-window-display.ts';

// ── quotaMetricLabel：三维各有专属中文标注 ────────────────────────────────
test('TestQuotaMetricLabel', () => {
  // 判别: 三维各专属文案(漏映射 → 回退原码,用户看到生 metric 串)。
  assert.equal(quotaMetricLabel('requests'), '请求数');
  assert.equal(quotaMetricLabel('cost_usd'), '费用 (USD)');
  assert.equal(quotaMetricLabel('tokens_estimated'), 'Token');
  // 未知 metric 回退原码(不崩)。
  assert.equal(quotaMetricLabel('weird_metric'), 'weird_metric');
});

// ── quotaWindowKey：同 window_kind 不同 metric 必须唯一(防 React 键冲突) ───
test('TestQuotaWindowKey_UniquePerMetric', () => {
  // 判别(真 bug): 多维下三个窗口常共享同一 window_kind(都 calendar_day);
  // 单用 window_kind 作 key 会冲突 → 必须含 metric。
  // mutation: quotaWindowKey 改成只返 window_kind → 三者相同 → notEqual 断言红。
  const reqKey = quotaWindowKey({ metric: 'requests', window_kind: 'calendar_day' });
  const costKey = quotaWindowKey({ metric: 'cost_usd', window_kind: 'calendar_day' });
  const tokKey = quotaWindowKey({ metric: 'tokens_estimated', window_kind: 'calendar_day' });
  assert.notEqual(reqKey, costKey, 'requests vs cost_usd 同窗口必须不同 key');
  assert.notEqual(costKey, tokKey, 'cost_usd vs tokens 同窗口必须不同 key');
  assert.notEqual(reqKey, tokKey, 'requests vs tokens 同窗口必须不同 key');
  // key 确实含 metric(不是仅靠顺序碰巧不同)。
  assert.match(reqKey, /requests/, 'key 含 metric 维度');
  // 不同 window_kind 同 metric 也唯一(key 也含 window_kind)。
  assert.notEqual(
    quotaWindowKey({ metric: 'cost_usd', window_kind: 'calendar_day' }),
    quotaWindowKey({ metric: 'cost_usd', window_kind: 'calendar_month' }),
    '同 metric 不同窗口也必须不同 key',
  );
});
