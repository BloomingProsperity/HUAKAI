import { apiGet } from './client';
import { listUsageRecords } from './observability';
import { getProviderAccountHealth, listProviderAccounts } from './providerAccounts';
import type {
  AccountModeCatalogResponse,
  AdminChannelCatalogList,
  AdminProviderCatalogItem,
  AdminProviderCatalogList,
  ProviderAccount,
  ProviderAccountHealthSnapshot,
  ProviderAccountList,
  UsageRecord,
} from './types';

const DASHBOARD_USAGE_LIMIT = 200;
const DASHBOARD_ACCOUNT_LIMIT = 200;
const DASHBOARD_CATALOG_LIMIT = 500;
const DASHBOARD_MAX_PAGES = 25;
const DASHBOARD_HEALTH_BATCH_SIZE = 20;
const DASHBOARD_HEALTH_SNAPSHOT_LIMIT = 50;

export type DashboardHealthState = 'operational' | 'degraded' | 'failed' | 'cooling_down' | 'error' | 'unknown';
export type DashboardScheduleStatus = 'active' | 'limited' | 'disabled' | 'requires_action';

export interface DashboardUsageSummary {
  input_tokens: number;
  output_tokens: number;
  cache_creation_tokens: number;
  cache_read_tokens: number;
  actual_cost: number;
  request_count: number;
  pending_reconciliation_count: number;
  settlement_p50_ms: number | null;
  settlement_p95_ms: number | null;
  settlement_p99_ms: number | null;
  cache_hit_ratio: number | null;
  usage_has_more: boolean;
  source_from: string;
  source_to: string;
}

export interface DashboardHealthStats {
  healthy: number;
  degraded: number;
  failed: number;
  total: number;
}

export interface DashboardAccountRow {
  id: number;
  name: string;
  provider: string;
  channel: string;
  health_state: DashboardHealthState;
  schedule_status: DashboardScheduleStatus;
  in_flight: number;
  cap: number;
  models: string[];
  last_dispatch_at: string | null;
  health_updated_at: string;
  failure_count: number;
  requires_action: boolean;
}

export interface DashboardChartPoint {
  time: string;
  cache_creation_tokens: number;
  cache_read_tokens: number;
  requests: number;
  hit_rate: number;
  ratio: number;
}

export interface DashboardCatalogSummary {
  provider_count: number;
  enabled_provider_count: number;
  channel_count: number;
  enabled_channel_count: number;
  account_mode_count: number;
  available_models: string[];
  available_providers: string[];
}

export interface DashboardSnapshot {
  usage: DashboardUsageSummary;
  accounts: DashboardAccountRow[];
  alert_accounts: DashboardAccountRow[];
  health_stats: DashboardHealthStats;
  chart_points: DashboardChartPoint[];
  catalog: DashboardCatalogSummary;
  in_flight: number;
  total_cap_concurrency: number;
  tenant_id: number | null;
  loaded_at: string;
  latest_backend_event_at: string | null;
  source_warnings: string[];
}

// listAccountModes — GET /admin/v1/account-modes
export function listAccountModes(): Promise<AccountModeCatalogResponse> {
  return apiGet<AccountModeCatalogResponse>('/admin/v1/account-modes');
}

// listAdminProviders — GET /admin/v1/providers
export function listAdminProviders(opts?: { tenant_id?: number; limit?: number; offset?: number }): Promise<AdminProviderCatalogList> {
  return apiGet<AdminProviderCatalogList>('/admin/v1/providers', {
    tenant_id: opts?.tenant_id,
    limit: opts?.limit,
    offset: opts?.offset,
  });
}

// listAdminChannels — GET /admin/v1/channels
export function listAdminChannels(opts?: { tenant_id?: number; limit?: number; offset?: number }): Promise<AdminChannelCatalogList> {
  return apiGet<AdminChannelCatalogList>('/admin/v1/channels', {
    tenant_id: opts?.tenant_id,
    limit: opts?.limit,
    offset: opts?.offset,
  });
}

export async function loadDashboardSnapshot(now = new Date()): Promise<DashboardSnapshot> {
  const sourceFrom = startOfLocalDay(now);
  const sourceTo = now;
  const [providerAccounts, accountModes] = await Promise.all([
    listDashboardProviderAccounts(),
    listAccountModes(),
  ]);
  const accountTenantID = dashboardTenantID(providerAccounts.items);
  const firstUsageRecords = await listDashboardUsageRecords(sourceFrom, sourceTo, accountTenantID);
  const usageTenantID = dashboardTenantID(firstUsageRecords.items);
  const tenantID = accountTenantID ?? usageTenantID;
  const usageRecords = accountTenantID || !tenantID
    ? firstUsageRecords
    : await listDashboardUsageRecords(sourceFrom, sourceTo, tenantID);
  const [providers, channels] = await Promise.all([
    listAdminProviders({ tenant_id: tenantID, limit: DASHBOARD_CATALOG_LIMIT, offset: 0 }),
    listAdminChannels({ tenant_id: tenantID, limit: DASHBOARD_CATALOG_LIMIT, offset: 0 }),
  ]);

  const healthTargetAccounts = dashboardHealthSnapshotAccounts(providerAccounts.items, now);
  const healthSnapshots = await listDashboardHealthSnapshots(healthTargetAccounts);
  const healthByAccount = new Map(healthSnapshots.map((snapshot) => [snapshot.id, snapshot]));
  const providerByID = new Map(providers.items.map((provider) => [provider.id, provider]));
  const channelByID = new Map(channels.items.map((channel) => [channel.id, channel]));
  const accounts = providerAccounts.items
    .map((account) => buildAccountRow(account, healthByAccount.get(account.id), providerByID, channelByID, now))
    .sort(compareDashboardAccounts)
    .slice(0, 5);
  const allAccountRows = providerAccounts.items.map(
    (account) => buildAccountRow(account, healthByAccount.get(account.id), providerByID, channelByID, now),
  );

  return {
    usage: aggregateUsage(usageRecords.items, usageRecords.hasMore, sourceFrom, sourceTo),
    accounts,
    alert_accounts: allAccountRows,
    health_stats: aggregateHealth(allAccountRows),
    chart_points: buildChartPoints(usageRecords.items),
    catalog: buildCatalogSummary(providerAccounts.items, accountModes, providers, channels),
    in_flight: sum(providerAccounts.items.map((account) => account.in_flight_count)),
    total_cap_concurrency: sum(providerAccounts.items.map((account) => account.cap_concurrency)),
    tenant_id: tenantID ?? null,
    loaded_at: now.toISOString(),
    latest_backend_event_at: latestEventTime(usageRecords.items, providerAccounts.items, healthSnapshots),
    source_warnings: [
      ...(usageRecords.hasMore ? [`usage 记录超过 ${DASHBOARD_USAGE_LIMIT * DASHBOARD_MAX_PAGES} 条，本次只统计已读取分页`] : []),
      ...(providerAccounts.hasMore ? [`provider account 超过 ${DASHBOARD_ACCOUNT_LIMIT * DASHBOARD_MAX_PAGES} 条，本次只统计已读取分页`] : []),
      ...(healthTargetAccounts.length < providerAccounts.items.length
        ? [`health snapshot 仅读取 ${healthTargetAccounts.length}/${providerAccounts.items.length} 个风险与高负载账号；其余账号使用 provider account 列表状态`]
        : []),
    ],
  };
}

async function listDashboardUsageRecords(from: Date, to: Date, tenantID?: number): Promise<{ items: UsageRecord[]; hasMore: boolean }> {
  const items: UsageRecord[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < DASHBOARD_MAX_PAGES; page += 1) {
    const response = await listUsageRecords({
      cursor,
      limit: DASHBOARD_USAGE_LIMIT,
      tenant_id: tenantID,
      from: from.toISOString(),
      to: to.toISOString(),
    });
    items.push(...response.items);
    if (!response.page.has_more) return { items, hasMore: false };
    if (!response.page.next_cursor) return { items, hasMore: true };
    cursor = response.page.next_cursor;
  }
  return { items, hasMore: true };
}

async function listDashboardProviderAccounts(): Promise<{ items: ProviderAccount[]; hasMore: boolean }> {
  const items: ProviderAccount[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < DASHBOARD_MAX_PAGES; page += 1) {
    const response: ProviderAccountList = await listProviderAccounts({
      cursor,
      limit: DASHBOARD_ACCOUNT_LIMIT,
    });
    items.push(...response.items);
    if (!response.page.has_more) return { items, hasMore: false };
    if (!response.page.next_cursor) return { items, hasMore: true };
    cursor = response.page.next_cursor;
  }
  return { items, hasMore: true };
}

async function listDashboardHealthSnapshots(accounts: ProviderAccount[]): Promise<ProviderAccountHealthSnapshot[]> {
  const snapshots: ProviderAccountHealthSnapshot[] = [];
  for (let index = 0; index < accounts.length; index += DASHBOARD_HEALTH_BATCH_SIZE) {
    const batch = accounts.slice(index, index + DASHBOARD_HEALTH_BATCH_SIZE);
    snapshots.push(...await Promise.all(batch.map((account) => getProviderAccountHealth(account.id))));
  }
  return snapshots;
}

function dashboardHealthSnapshotAccounts(accounts: ProviderAccount[], now: Date): ProviderAccount[] {
  const selected = new Map<number, ProviderAccount>();
  const ordered = [...accounts].sort(compareProviderAccounts);
  for (const account of ordered) {
    if (providerAccountNeedsHealthSnapshot(account, now)) {
      selected.set(account.id, account);
    }
    if (selected.size >= DASHBOARD_HEALTH_SNAPSHOT_LIMIT) return Array.from(selected.values());
  }
  for (const account of ordered) {
    selected.set(account.id, account);
    if (selected.size >= DASHBOARD_HEALTH_SNAPSHOT_LIMIT) return Array.from(selected.values());
  }
  return Array.from(selected.values());
}

function providerAccountNeedsHealthSnapshot(account: ProviderAccount, now: Date): boolean {
  return !account.enabled
    || normalizeAccountHealth(account.health_state) !== 'operational'
    || account.in_flight_count >= account.cap_concurrency
    || scheduleBlockerActive(account.rate_limit_reset_at, now)
    || scheduleBlockerActive(account.overload_until, now)
    || scheduleBlockerActive(account.temp_unschedulable_until, now);
}

function startOfLocalDay(now: Date): Date {
  const date = new Date(now);
  date.setHours(0, 0, 0, 0);
  return date;
}

function buildAccountRow(
  account: ProviderAccount,
  health: ProviderAccountHealthSnapshot | undefined,
  providerByID: Map<number, AdminProviderCatalogItem>,
  channelByID: Map<number, { name: string }>,
  now: Date,
): DashboardAccountRow {
  return {
    id: account.id,
    name: account.name,
    provider: providerLabel(providerByID.get(account.provider_id), account.provider_id),
    channel: channelByID.get(account.channel_id)?.name ?? `channel_id:${account.channel_id}`,
    health_state: normalizeHealthState(account, health),
    schedule_status: scheduleStatus(account, health, now),
    in_flight: account.in_flight_count,
    cap: account.cap_concurrency,
    models: account.model_allow_list,
    last_dispatch_at: account.last_dispatch_at,
    health_updated_at: health?.updated_at ?? account.updated_at,
    failure_count: health?.failure_count ?? 0,
    requires_action: health?.requires_action ?? false,
  };
}

function providerLabel(provider: AdminProviderCatalogItem | undefined, providerID: number): string {
  if (!provider) return `provider_id:${providerID}`;
  return provider.display_name ? `${provider.display_name} (${provider.code})` : provider.code;
}

function normalizeHealthState(account: ProviderAccount, health: ProviderAccountHealthSnapshot | undefined): DashboardHealthState {
  if (!health) return normalizeAccountHealth(account.health_state);
  if (health.health_state === 'healthy') return 'operational';
  if (health.health_state === 'throttled') return 'degraded';
  if (health.health_state === 'cooldown') return 'cooling_down';
  if (health.health_state === 'revoked') return 'failed';
  return normalizeAccountHealth(account.health_state);
}

function normalizeAccountHealth(state: ProviderAccount['health_state']): DashboardHealthState {
  if (state === 'healthy') return 'operational';
  if (state === 'throttled') return 'degraded';
  if (state === 'cooldown') return 'cooling_down';
  if (state === 'revoked') return 'failed';
  if (state === 'operational' || state === 'degraded' || state === 'failed' || state === 'cooling_down' || state === 'error') {
    return state;
  }
  return 'unknown';
}

function scheduleStatus(account: ProviderAccount, health: ProviderAccountHealthSnapshot | undefined, now: Date): DashboardScheduleStatus {
  if (!account.enabled || health?.enabled === false) return 'disabled';
  if (health?.requires_action) return 'requires_action';
  if (health?.health_state === 'revoked') return 'requires_action';
  if (health?.health_state === 'throttled' || health?.health_state === 'cooldown') return 'limited';
  if (
    scheduleBlockerActive(account.rate_limit_reset_at, now)
    || scheduleBlockerActive(account.overload_until, now)
    || scheduleBlockerActive(account.temp_unschedulable_until, now)
  ) return 'limited';
  return 'active';
}

function scheduleBlockerActive(value: string | null | undefined, now: Date): boolean {
  const until = parseDate(value);
  return until !== null && until > now;
}

function compareDashboardAccounts(left: DashboardAccountRow, right: DashboardAccountRow): number {
  if (right.in_flight !== left.in_flight) return right.in_flight - left.in_flight;
  if (right.cap !== left.cap) return right.cap - left.cap;
  return left.name.localeCompare(right.name);
}

function compareProviderAccounts(left: ProviderAccount, right: ProviderAccount): number {
  if (right.in_flight_count !== left.in_flight_count) return right.in_flight_count - left.in_flight_count;
  if (right.cap_concurrency !== left.cap_concurrency) return right.cap_concurrency - left.cap_concurrency;
  return left.name.localeCompare(right.name);
}

function aggregateUsage(
  records: UsageRecord[],
  usageHasMore: boolean,
  sourceFrom: Date,
  sourceTo: Date,
): DashboardUsageSummary {
  const settlementDurations = records
    .map((record) => durationMs(record.requested_at, settledTimestamp(record)))
    .filter((value): value is number => value !== null);
  const cacheCreation = sum(records.map((record) => numberOrZero(record.cache_creation_tokens)));
  const cacheRead = sum(records.map((record) => numberOrZero(record.cache_read_tokens)));
  const cacheDenominator = cacheCreation + cacheRead;

  return {
    input_tokens: sum(records.map((record) => numberOrZero(record.tokens_input))),
    output_tokens: sum(records.map((record) => numberOrZero(record.tokens_output))),
    cache_creation_tokens: cacheCreation,
    cache_read_tokens: cacheRead,
    actual_cost: sum(records.map((record) => parseCost(record.actual_cost))),
    request_count: records.length,
    pending_reconciliation_count: records.filter((record) => record.pending_reconciliation).length,
    settlement_p50_ms: percentile(settlementDurations, 0.5),
    settlement_p95_ms: percentile(settlementDurations, 0.95),
    settlement_p99_ms: percentile(settlementDurations, 0.99),
    cache_hit_ratio: cacheDenominator === 0 ? null : cacheRead / cacheDenominator,
    usage_has_more: usageHasMore,
    source_from: sourceFrom.toISOString(),
    source_to: sourceTo.toISOString(),
  };
}

function buildChartPoints(records: UsageRecord[]): DashboardChartPoint[] {
  const byHour = new Map<string, { cacheCreation: number; cacheRead: number; requests: number; sortKey: number }>();
  for (const record of records) {
    const observedAt = parseDate(settledTimestamp(record) ?? record.requested_at);
    if (!observedAt) continue;
    const hour = new Date(observedAt);
    hour.setMinutes(0, 0, 0);
    const key = hour.toISOString();
    const bucket = byHour.get(key) ?? { cacheCreation: 0, cacheRead: 0, requests: 0, sortKey: hour.getTime() };
    bucket.cacheCreation += numberOrZero(record.cache_creation_tokens);
    bucket.cacheRead += numberOrZero(record.cache_read_tokens);
    bucket.requests += 1;
    byHour.set(key, bucket);
  }

  return Array.from(byHour.entries())
    .sort(([, left], [, right]) => left.sortKey - right.sortKey)
    .map(([key, bucket]) => {
      const denominator = bucket.cacheCreation + bucket.cacheRead;
      if (denominator === 0) return null;
      const ratio = bucket.cacheRead / denominator;
      return {
        time: new Date(key).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false }),
        cache_creation_tokens: bucket.cacheCreation,
        cache_read_tokens: bucket.cacheRead,
        requests: bucket.requests,
        hit_rate: Number((ratio * 100).toFixed(1)),
        ratio,
      };
    })
    .filter((point): point is DashboardChartPoint => point !== null);
}

function buildCatalogSummary(
  accounts: ProviderAccount[],
  accountModes: AccountModeCatalogResponse,
  providers: AdminProviderCatalogList,
  channels: AdminChannelCatalogList,
): DashboardCatalogSummary {
  return {
    provider_count: providers.items.length,
    enabled_provider_count: providers.items.filter((provider) => provider.enabled).length,
    channel_count: channels.items.length,
    enabled_channel_count: channels.items.filter((channel) => channel.enabled).length,
    account_mode_count: accountModes.modes.filter((mode) => mode.is_enabled).length,
    available_models: uniqueSorted(accounts.flatMap((account) => account.model_allow_list)),
    available_providers: uniqueSorted(
      providers.items
        .filter((provider) => provider.enabled)
        .map((provider) => provider.display_name || provider.code),
    ),
  };
}

function aggregateHealth(accounts: DashboardAccountRow[]): DashboardHealthStats {
  const stats: DashboardHealthStats = { healthy: 0, degraded: 0, failed: 0, total: accounts.length };
  for (const account of accounts) {
    if (account.health_state === 'operational') {
      stats.healthy += 1;
    } else if (account.health_state === 'degraded' || account.health_state === 'cooling_down') {
      stats.degraded += 1;
    } else {
      stats.failed += 1;
    }
  }
  return stats;
}

function latestEventTime(
  records: UsageRecord[],
  accounts: ProviderAccount[],
  healthSnapshots: ProviderAccountHealthSnapshot[],
): string | null {
  const candidates = [
    ...records.flatMap((record) => [record.requested_at, record.settled_at, record.created_at]),
    ...accounts.flatMap((account) => [account.last_dispatch_at, account.updated_at]),
    ...healthSnapshots.flatMap((snapshot) => [snapshot.updated_at, snapshot.last_refresh_at]),
  ];
  const latest = candidates.reduce<Date | null>((current, value) => {
    const parsed = parseDate(value);
    if (!parsed) return current;
    return current === null || parsed > current ? parsed : current;
  }, null);
  return latest?.toISOString() ?? null;
}

function settledTimestamp(record: UsageRecord): string | undefined {
  return record.settled_at ?? record.created_at;
}

function durationMs(from: string | undefined, to: string | undefined): number | null {
  const start = parseDate(from);
  const end = parseDate(to);
  if (!start || !end) return null;
  const duration = end.getTime() - start.getTime();
  return duration >= 0 ? duration : null;
}

function parseDate(value: string | null | undefined): Date | null {
  if (!value) return null;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

function percentile(values: number[], ratio: number): number | null {
  if (values.length === 0) return null;
  const sorted = [...values].sort((left, right) => left - right);
  const index = Math.min(sorted.length - 1, Math.max(0, Math.ceil(sorted.length * ratio) - 1));
  return Math.round(sorted[index]);
}

function parseCost(value: string): number {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function numberOrZero(value: number | null | undefined): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

function sum(values: number[]): number {
  return values.reduce((total, value) => total + value, 0);
}

function uniqueSorted(values: string[]): string[] {
  return Array.from(new Set(values.map((value) => value.trim()).filter(Boolean))).sort((left, right) => left.localeCompare(right));
}

function dashboardTenantID(rows: Array<{ tenant_id?: number }>): number | undefined {
  const tenantIDs = new Set<number>();
  for (const row of rows) {
    if (typeof row.tenant_id === 'number' && Number.isFinite(row.tenant_id) && row.tenant_id > 0) {
      tenantIDs.add(row.tenant_id);
    }
  }
  if (tenantIDs.size !== 1) return undefined;
  return Array.from(tenantIDs)[0];
}
