// 站点配置（公开端点 GET /v1/site/config）：登录/注册需要 tenant_id，
// 单租户部署由后端在此暴露默认 tenant 与功能开关。拉取失败时回落到 DEFAULT_TENANT_ID。
export const DEFAULT_TENANT_ID = 1;

export interface SiteConfig {
  tenant_id: number;
  site_name?: string;
  registration_enabled?: boolean;
  captcha_enabled?: boolean;
  // 后端公开 config 把启用的社交登录 provider 以【逗号分隔字符串】下发(如 "github,google");
  // passkey_enabled 是 bool 开关。两者均由运维平台设置驱动(oauth_providers_enabled / passkey_enabled)。
  oauth_providers_enabled?: string;
  passkey_enabled?: boolean;
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
      oauth_providers_enabled: data.oauth_providers_enabled,
      passkey_enabled: data.passkey_enabled,
    };
  } catch {
    return { tenant_id: DEFAULT_TENANT_ID };
  }
}

// 解析后端逗号清单为去重小写 provider 数组。空/未配置 → 空数组(=不渲染任何社交按钮)。
// 与后端 Normalize(strings.Split(raw, ",")) 对齐:去空白、剔空段、统一小写、去重保序。
export function parseEnabledProviders(raw: string | undefined | null): string[] {
  if (!raw) return [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const part of raw.split(',')) {
    const p = part.trim().toLowerCase();
    if (p && !seen.has(p)) {
      seen.add(p);
      out.push(p);
    }
  }
  return out;
}
