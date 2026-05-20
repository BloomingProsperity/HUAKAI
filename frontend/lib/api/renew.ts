import { ApiError, apiGet } from './client';
import type { AuthCredentialRenewStatusList } from './types';

const RENEW_STATUS_PATH = '/admin/v1/credentials/renew-status';

export class RenewCredentialsForbiddenError extends Error {
  constructor() {
    super('此面板需要平台管理员权限：当前 token 无权读取 provider account credentials（HTTP 403）。请切换为 platform_admin token 后刷新。');
    this.name = 'RenewCredentialsForbiddenError';
  }
}

function isForbiddenError(err: unknown): boolean {
  return err instanceof ApiError && err.status === 403;
}

export async function listRenewStatus(opts?: {
  limit?: number;
  cursor?: string;
}): Promise<AuthCredentialRenewStatusList> {
  try {
    return await apiGet<AuthCredentialRenewStatusList>(RENEW_STATUS_PATH, {
      limit: opts?.limit,
      cursor: opts?.cursor,
    });
  } catch (err: unknown) {
    if (isForbiddenError(err)) {
      throw new RenewCredentialsForbiddenError();
    }
    throw err;
  }
}
