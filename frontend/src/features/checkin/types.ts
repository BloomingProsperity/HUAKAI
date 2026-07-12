/*
 * 每日签到(用户态)前端类型 —— 镜像 checkinhttp 的 JSON 形态。
 * 端点(session 鉴权,挂在 /v1/me 组下):
 *   GET  /v1/me/checkin?month=YYYY-MM  → 今日是否已签 + 本月记录 + 配置区间
 *   POST /v1/me/checkin                → 执行签到,返还随机奖励到余额
 * 金额单位:cents(美分),前端展示时换算成元/美元。
 */

/** GET 状态响应(statusResponse)。 */
export interface CheckinStatus {
  /** 平台是否开启每日签到(关闭时后端 POST 返回 daily_checkin_disabled)。 */
  enabled: boolean
  /** 单次奖励区间下限(cents)。 */
  min_cents: number
  /** 单次奖励区间上限(cents)。 */
  max_cents: number
  /** 查询月份(YYYY-MM,UTC)。 */
  month: string
  /** 今日(UTC 日)是否已签到。 */
  checked_in_today: boolean
  /** 本月签到记录(按签到日)。 */
  records: CheckinRecord[]
}

/** 单条签到记录(recordView)。 */
export interface CheckinRecord {
  /** 签到日(YYYY-MM-DD,UTC)。 */
  checkin_date: string
  /** 本次返还奖励(cents)。 */
  reward_cents: number
  /** 币种(后端固定 USD)。 */
  currency_code: string
  /** 关联计费事件 ID(可缺省)。 */
  billing_event_id?: number
  /** 记录创建时刻(RFC3339)。 */
  created_at: string
}

/** POST 签到成功响应(postResponse)。 */
export interface CheckinResult {
  /** 本次返还奖励(cents)。 */
  reward_cents: number
  /** 签到日(YYYY-MM-DD,UTC)。 */
  checkin_date: string
  /** 签到后的最新余额(cents)。 */
  new_balance: number
}
