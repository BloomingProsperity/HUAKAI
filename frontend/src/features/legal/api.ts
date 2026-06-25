import { apiGet } from '../../lib/api'
import type { SiteConfig } from './types'

/*
 * 法律条款页数据访问层。
 *
 * 仅一个只读端点:GET /v1/site/config(匿名,无鉴权,无请求体/参数)。
 *   - 真实端点:backend/internal/sitepublichttp/handler.go:79 + routes_siteconfig.go:19
 *   - 鉴权:public 壳,不带 token(api.ts 的 tokenForPath 对非 /admin、非 session 路径不注入)。
 *
 * 返回体字段众多(品牌+注册开关+captcha 等),本层用 Partial 接收再由 pickSiteConfig
 * 规整为本页只关心的 4 个字段,避免对未消费字段产生死耦合。
 */
const PATH = '/v1/site/config'

export async function fetchSiteConfig(signal?: AbortSignal): Promise<SiteConfig> {
  const raw = await apiGet<Partial<SiteConfig> & Record<string, unknown>>(PATH, { signal })
  return {
    site_name: typeof raw.site_name === 'string' ? raw.site_name : '',
    site_footer: typeof raw.site_footer === 'string' ? raw.site_footer : '',
    site_contact_info: typeof raw.site_contact_info === 'string' ? raw.site_contact_info : '',
    site_doc_url: typeof raw.site_doc_url === 'string' ? raw.site_doc_url : '',
  }
}
