/*
 * Telegram Login Widget 纯逻辑(可单测,无 DOM / 无网络)。
 *
 * Telegram 官方 Login Widget(telegram.org/js/telegram-widget.js)在用户授权后,通过 data-onauth
 * 回调把一个 user 对象交给页面:{id, first_name, last_name, username, photo_url, auth_date, hash}。
 * 后端 /v1/auth/telegram-login 与 /v1/users/me/oauth-bindings/telegram 都要求 params 为字符串映射
 * (telegramauth.VerifyWidget 按排序后的 k=v 行做 HMAC-SHA256 校验),故这里负责把 user 对象规整成
 * Record<string,string>,并判定数据是否齐备到可提交。
 *
 * 安全:绝不在本地伪造/篡改任何字段——可信度完全由后端用 bot token 做的 HMAC 校验保证;前端只做透传与形态规整。
 */

/** Telegram Login Widget 回调的 user 对象(字段全可选,做防御性处理)。 */
export interface TelegramWidgetUser {
  id?: number | string
  first_name?: string
  last_name?: string
  username?: string
  photo_url?: string
  auth_date?: number | string
  hash?: string
  [key: string]: unknown
}

/**
 * 把 widget 回调的 user 对象规整成后端所需的 params: Record<string,string>。
 * 规则:丢弃 undefined/null;数值转字符串;其余原样字符串化。保留后端 HMAC 会用到的全部字段
 * (除 hash 外的字段都参与 data-check-string;hash 是被比对的签名本身)。
 *
 * 关键:必须保留 auth_date/id 等的原始字符串形态(后端按收到的字符串重算 HMAC),
 * 任何丢字段或改写都会导致后端 HMAC 不匹配 → 校验失败。
 */
export function telegramWidgetParams(user: TelegramWidgetUser): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [key, value] of Object.entries(user)) {
    if (value === undefined || value === null) continue
    out[key] = typeof value === 'string' ? value : String(value)
  }
  return out
}

/**
 * widget 数据是否齐备到可提交:必须含 id / auth_date / hash 三个命门字段
 *(后端 telegramauth.VerifyWidget 缺任一即 ErrInvalidInput)。缺失时不应发请求。
 */
export function telegramWidgetReady(params: Record<string, string>): boolean {
  return Boolean(params.id) && Boolean(params.auth_date) && Boolean(params.hash)
}
