import { ApiError, apiGet } from './client';
import type { AuthCredentialRenewStatusList } from './types';

const RENEW_STATUS_PATH = '/admin/v1/credentials/renew-status';

export class RenewCredentialsForbiddenError extends Error {
  constructor() {
    super('This panel requires an admin token with credential renew status access (HTTP 403). Switch to a platform_admin token, or a tenant_operator token scoped to the requested tenant, then refresh.');
    this.name = 'RenewCredentialsForbiddenError';
  }
}

function isForbiddenError(err: unknown): boolean {
  return err instanceof ApiError && err.status === 403;
}

export async function listRenewStatus(opts?: {
  limit?: number;
  cursor?: string;
  tenantId?: number;
}): Promise<AuthCredentialRenewStatusList> {
  try {
    return await apiGet<AuthCredentialRenewStatusList>(RENEW_STATUS_PATH, {
      limit: opts?.limit,
      cursor: opts?.cursor,
      tenant_id: opts?.tenantId,
    });
  } catch (err: unknown) {
    if (isForbiddenError(err)) {
      throw new RenewCredentialsForbiddenError();
    }
    throw err;
  }
}
