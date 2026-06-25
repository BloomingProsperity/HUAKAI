import { apiGet, apiSend } from '../../lib/api'
import type { CheckinResult, CheckinStatus } from './types'

/*
 * 每日签到数据访问层。端点挂在 /v1/me 组(session 鉴权,操作当前登录用户自身)。
 *   GET  /v1/me/checkin?month=YYYY-MM  → 状态
 *   POST /v1/me/checkin                → 执行签到
 */
const CHECKIN_PATH = '/v1/me/checkin'

/**
 * 拉取签到状态。month 省略则后端按当前 UTC 月返回;传入须为 YYYY-MM。
 */
export async function getCheckinStatus(month?: string, signal?: AbortSignal): Promise<CheckinStatus> {
  return apiGet<CheckinStatus>(CHECKIN_PATH, { query: { month: month || undefined }, signal })
}

/**
 * 执行今日签到。成功返回本次奖励与最新余额;
 * 重复签到后端回 409 daily_checkin_already_claimed,由上层捕获 ApiError 提示。
 */
export async function doCheckin(): Promise<CheckinResult> {
  return apiSend<CheckinResult>('POST', CHECKIN_PATH)
}
