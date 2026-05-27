export type UsageTrustStatus = 'verified' | 'signed-only' | 'unverified' | 'missing' | 'mismatch';

export type UsageTrustTone = 'green' | 'yellow' | 'gray' | 'red';

export interface UsageTrustRow {
  provider?: string | null;
  requested_model?: string | null;
  upstream_model?: string | null;
  trust_status?: string | null;
  status?: string | null;
}

export type UsageTrustSource = UsageTrustRow;

export interface UsageTrustView {
  providerModelLabel: string;
  status: UsageTrustStatus;
  statusLabel: string;
  tone: UsageTrustTone;
}

const VALID_STATUSES: UsageTrustStatus[] = ['verified', 'signed-only', 'unverified', 'missing', 'mismatch'];

export function normalizeUsageTrustStatus(raw?: string | null): UsageTrustStatus {
  if (!raw) return 'unverified';
  return (VALID_STATUSES as string[]).includes(raw) ? raw as UsageTrustStatus : 'missing';
}

export function usageTrustStatusTone(status: string): UsageTrustTone {
  switch (normalizeUsageTrustStatus(status)) {
    case 'verified':
      return 'green';
    case 'signed-only':
      return 'yellow';
    case 'missing':
    case 'mismatch':
      return 'red';
    case 'unverified':
    default:
      return 'gray';
  }
}

export function buildUsageTrustView(row: UsageTrustRow): UsageTrustView {
  const status = normalizeUsageTrustStatus(row.trust_status ?? row.status);
  const provider = cleanCell(row.provider);
  const model = cleanCell(row.upstream_model) || cleanCell(row.requested_model);
  return {
    providerModelLabel: provider || model ? `${provider || 'unknown'} / ${model || 'unknown'}` : '-',
    status,
    statusLabel: status,
    tone: usageTrustStatusTone(status),
  };
}

export function usageTrustHasMismatchWarning(rows: Array<UsageTrustRow | UsageTrustView>): boolean {
  return rows.some((row) => {
    const raw = 'trust_status' in row ? row.trust_status ?? row.status : row.status;
    return normalizeUsageTrustStatus(raw) === 'mismatch';
  });
}

function cleanCell(value?: string | null): string {
  return typeof value === 'string' ? value.trim() : '';
}
