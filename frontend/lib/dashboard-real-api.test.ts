import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

const ROOT = process.cwd();

function readFrontendSource(path: string): string {
  return readFileSync(join(ROOT, path), 'utf8');
}

test('TestDashboardUsesRealAdminAPIInsteadOfDashboardMockConstants', () => {
  const page = readFrontendSource('app/dashboard/page.tsx');
  const chart = readFrontendSource('components/dashboard/TrendChart.tsx');
  const api = readFrontendSource('lib/api/dashboard.ts');
  const observabilityApi = readFrontendSource('lib/api/observability.ts');

  // 判别点：只要 dashboard 或图表重新接入假常量，这里会直接 red。
  for (const source of [page, chart]) {
    assert.doesNotMatch(source, /dashboard-mock/);
    assert.doesNotMatch(source, /MOCK_PROVIDER_ACCOUNTS|MOCK_USAGE|MOCK_CHART_DATA/);
  }

  // 判别点：页面必须走真实 admin API 聚合入口，而不是本地静态数据。
  assert.match(page, /loadDashboardSnapshot/);
  assert.match(api, /listUsageRecords/);
  assert.match(api, /listProviderAccounts/);
  assert.match(api, /getProviderAccountHealth/);
  assert.match(api, /listAccountModes/);
  assert.match(api, /listAdminProviders/);
  assert.match(api, /listAdminChannels/);

  // 判别点：真实 usage API 当前返回顶层 next_cursor/total，dashboard 不能只读不存在的 page。
  assert.match(observabilityApi, /normalizeUsageRecordList/);
  assert.match(observabilityApi, /next_cursor/);
  assert.match(api, /created_at/);
  assert.match(api, /alert_accounts/);
  assert.match(page, /snapshot\.alert_accounts/);

  // 判别点：platform_admin/bootstrap 访问时，catalog 必须带真实行推导出的 tenant scope。
  assert.match(api, /dashboardTenantID/);
  assert.match(api, /listAdminProviders\(\{ tenant_id: tenantID/);
  assert.match(api, /listAdminChannels\(\{ tenant_id: tenantID/);
  assert.match(observabilityApi, /tenant_id/);
  assert.match(page, /snapshot\.tenant_id/);

  // 判别点：过期限流/过载时间戳不能继续把账号标成 limited。
  assert.match(api, /scheduleBlockerActive/);
  assert.doesNotMatch(api, /account\.rate_limit_reset_at\s*\|\|\s*account\.overload_until\s*\|\|\s*account\.temp_unschedulable_until/);

  // 判别点：dashboard 不能在加载时对所有账号无上限逐个打 health endpoint。
  assert.match(api, /DASHBOARD_HEALTH_SNAPSHOT_LIMIT/);
  assert.match(api, /dashboardHealthSnapshotAccounts/);
});
