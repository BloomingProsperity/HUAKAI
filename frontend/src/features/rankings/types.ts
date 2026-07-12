/*
 * 模型排行(公开榜)前端类型 —— 镜像 publicrankinghttp 的 JSON。
 * 端点:GET /v1/public/rankings?limit=N(公开,无需鉴权)。
 * 数据源=平台真实计费用量聚合(按模型聚合的调用次数/总 token);
 * scope 固定 "platform",metric 固定 "request_count"(后端按调用次数降序)。
 */

/** 单条排行项。request_share 是后端给的占比字符串(0~1 的定点小数,如 "0.123456")。 */
export interface RankingEntry {
  /** 后端按调用次数降序后给出的名次(从 1 起)。 */
  rank: number
  /** 模型名(已 trim)。 */
  model: string
  /** 调用次数。 */
  request_count: number
  /** 总 token 数(输入+输出)。 */
  token_total: number
  /** 调用次数占全榜比(字符串定点小数,0~1)。 */
  request_share: string
}

/** 排行响应包络。后端裸返回此对象(非裸数组)。 */
export interface RankingsResponse {
  /** 统计范围,当前固定 "platform"。 */
  scope: string
  /** 主排序指标,当前固定 "request_count"。 */
  metric: string
  /** 排行明细。 */
  rankings: RankingEntry[]
}
