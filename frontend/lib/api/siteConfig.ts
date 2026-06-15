// 站点配置（公开端点 GET /v1/site/config）：登录/注册需要 tenant_id，
// 单租户部署由后端在此暴露默认 tenant 与功能开关。拉取失败时回落到 DEFAULT_TENANT_ID。
export const DEFAULT_TENANT_ID = 1;

export interface SiteConfig {
  tenant_id: number;
  site_name?: string;
  registration_enabled?: boolean;
  captcha_enabled?: boolean;
}

export async function fetchSiteConfig(): Promise<SiteConfig> {
  try {
    const resp = await fetch('/v1/site/config', { cache: 'no-store' });
    if (!resp.ok) return { tenant_id: DEFAULT_TENANT_ID };
    const data = (await resp.json()) as Partial<SiteConfig> & { default_tenant_id?: number };
    return {
      tenant_id: data.tenant_id ?? data.default_tenant_id ?? DEFAULT_TENANT_ID,
      site_name: data.site_name,
      registration_enabled: data.registration_enabled,
      captcha_enabled: data.captcha_enabled,
    };
  } catch {
    return { tenant_id: DEFAULT_TENANT_ID };
  }
}
