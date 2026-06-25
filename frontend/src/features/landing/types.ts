/*
 * 落地首页(对外营销门面)前端类型。
 *
 * 两个公开端点(均无需鉴权,核源码确认):
 *  - GET /v1/site/config —— 站点品牌/开关(sitepublichttp.NewHandler,
 *    routes_siteconfig.go:19;字段来自该 handler 的 boolKeys/stringKeys 投影,allowlist 不含任何密钥)。
 *  - GET /v1/pricing/page —— 公开价目表(pricingpublichttp.NewHandler,routes.go:226;
 *    裸数组 pricingItem[],价格为「每 token 美元」字符串)。
 */

/**
 * 站点公开配置。镜像 sitepublichttp handler 的 JSON 投影。
 * 仅取落地页要用的品牌/文档/注册开关字段;其余 auth 细节(captcha/passkey 等)
 * 由登录页消费,落地页不读,避免死字段。所有字段都可能缺省(handler 兜默认值)。
 */
export interface SiteConfig {
  tenant_id: number
  site_name: string
  site_subtitle: string
  site_footer: string
  site_home_content: string
  site_contact_info: string
  site_doc_url: string
  site_api_base_url: string
  registration_enabled: boolean
}

/**
 * 公开价目项。镜像 pricingpublichttp.pricingItem 的 JSON tag。
 * 价格为「每 token 美元」极小数字符串;仅含已配置定价的模型(后端已过滤无价模型)。
 */
export interface PricingItem {
  model: string
  canonical_id?: string
  owned_by?: string
  mode?: string
  input_price_per_token?: string
  output_price_per_token?: string
  context_length?: number
  max_output_tokens?: number
  capabilities?: Record<string, boolean>
}
