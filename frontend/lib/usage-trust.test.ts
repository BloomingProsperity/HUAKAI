import assert from 'node:assert/strict';
import test from 'node:test';

import {
  buildUsageTrustView,
  usageTrustHasMismatchWarning,
  usageTrustStatusTone,
} from './usage-trust.ts';

test('TestUsageTrustStatusToneTableForAllFiveStatuses', () => {
  // Mutation 自检：verified 或 signed-only 的 tone 映射被降级会让精确断言 red。
  const cases = [
    ['verified', 'green'],
    ['signed-only', 'yellow'],
    ['unverified', 'gray'],
    ['missing', 'red'],
    ['mismatch', 'red'],
  ] as const;

  for (const [status, wantTone] of cases) {
    assert.equal(usageTrustStatusTone(status), wantTone, `${status} tone`);
  }
});

test('TestUsagePanelTrustColumnShowsProviderModelAndStatus', () => {
  // Mutation 自检：去掉 provider、upstream_model 或 status 任一字段映射都会 red。
  const view = buildUsageTrustView({
    provider: 'anthropic',
    requested_model: 'claude-opus-4',
    upstream_model: 'claude-opus-4-20260514',
    trust_status: 'unverified',
  });

  assert.equal(view.providerModelLabel, 'anthropic / claude-opus-4-20260514');
  assert.equal(view.status, 'unverified');
  assert.equal(view.statusLabel, 'unverified');
  assert.equal(view.tone, 'gray');
});

test('TestPanelMissingStatusDisplaysRedBadge', () => {
  // Mutation 自检：把 missing 当成普通 unknown/unverified 会让 tone 断言 red。
  assert.equal(usageTrustStatusTone('missing'), 'red');
  const view = buildUsageTrustView({
    provider: 'openai',
    requested_model: 'gpt-4o',
    trust_status: 'missing',
  });

  assert.equal(view.status, 'missing');
  assert.equal(view.tone, 'red');
});

test('TestPanelMismatchDisplaysRedBadgeAndWarningBanner', () => {
  // Mutation 自检：只染 badge、不触发 banner，或反过来，只要漏一边都会 red。
  const rows = [
    buildUsageTrustView({ provider: 'openai', requested_model: 'gpt-4o', trust_status: 'unverified' }),
    buildUsageTrustView({ provider: 'anthropic', requested_model: 'claude-opus-4', trust_status: 'mismatch' }),
  ];

  assert.equal(rows[1].tone, 'red');
  assert.equal(usageTrustHasMismatchWarning(rows), true);
});
