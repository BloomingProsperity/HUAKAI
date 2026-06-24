/*
 * 模型与定价(公开目录)前端类型 —— 镜像 pricingpublichttp 的 JSON。
 * 端点:GET /v1/pricing/page(公开,无需鉴权)。响应是裸数组 pricingItem[]。
 * 价格为「每 token 美元」字符串(极小数);仅含已配置定价的模型。
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
