/*
 * 可用渠道目录前端类型 —— 镜像后端 pricingpublichttp 的 JSON 形态。
 * 端点:GET /v1/pricing/page(公开,无需鉴权);见 backend/cmd/gateway/routes.go:226
 * + backend/internal/pricingpublichttp/handler.go:28(pricingItem 结构体 json tag)。
 * 响应是裸数组 PricingItem[];价格为「每 token 美元」字符串(极小数);
 * 后端只返回已配置定价的模型(无价模型被过滤掉)。
 * 注:该公开端点不暴露分组倍率(ratio 仅存在于 DB pricingcatalog 层,未进公开 DTO),
 * 故本目录以「厂商=渠道」分组并就每组聚合价目区间展示,不臆造倍率字段。
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
