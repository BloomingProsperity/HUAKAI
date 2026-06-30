import { useEffect, useRef, useState } from 'react'
import { telegramWidgetParams, telegramWidgetReady, type TelegramWidgetUser } from './telegramWidget'

/*
 * Telegram Login Widget 容器。懒加载官方脚本 telegram.org/js/telegram-widget.js,渲染官方登录按钮
 *(由 Telegram 托管的 iframe,用户的 Telegram 会话 cookie 在 telegram.org 域),用户授权后通过 data-onauth
 * 回调把 user 对象交回页面;本组件把它规整成 params 并回调 onAuth。
 *
 * 形态与既有 Turnstile 容器同款:外部脚本异步加载、失败不抛错只降级提示,绝不阻断页面其余部分。
 * 安全:widget 数据的可信度由后端用 bot token 做 HMAC 校验保证;前端只透传,不伪造/篡改。
 *
 * 运维前置(否则按钮渲染不出/校验失败):① 设 env HUAKAI_TELEGRAM_LOGIN_BOT_TOKEN;② admin 设置
 * telegram_bot_username;③ oauth_providers_enabled 含 telegram;④ BotFather 里把 bot 的 Login Domain
 * 设为本站域名;⑤ 若启用严格 CSP,放行 script-src/ frame-src https://telegram.org 与 https://oauth.telegram.org。
 */
interface TelegramLoginWidgetProps {
  /** 公开 bot 用户名(t.me/<name>);空则不渲染。 */
  botUsername: string
  /** 用户授权后回调,参数为规整好的 params(已含 id/auth_date/hash)。 */
  onAuth: (params: Record<string, string>) => void
  size?: 'small' | 'medium' | 'large'
  /** 是否申请「允许 bot 给用户发消息」权限(data-request-access=write)。绑定/登录场景一般不需要。 */
  requestAccess?: boolean
}

// 全局回调名自增序列:Telegram 脚本的 data-onauth 只能指向一个全局函数名,用自增后缀保证多实例不撞。
let telegramWidgetSeq = 0

type WindowWithCallbacks = Record<string, ((user: TelegramWidgetUser) => void) | undefined>

export function TelegramLoginWidget({ botUsername, onAuth, size = 'large', requestAccess = false }: TelegramLoginWidgetProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  // 用 ref 持有最新 onAuth,避免它变化时重建脚本(重建会让按钮闪烁/重复加载)。
  const onAuthRef = useRef(onAuth)
  onAuthRef.current = onAuth
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    const container = containerRef.current
    if (!container || !botUsername) return

    const callbackName = `__hkTelegramOnAuth_${(telegramWidgetSeq += 1)}`
    const globals = window as unknown as WindowWithCallbacks
    globals[callbackName] = (user: TelegramWidgetUser) => {
      const params = telegramWidgetParams(user || {})
      // 数据不齐(缺 id/auth_date/hash)就不提交,避免发一个注定 400 的请求。
      if (telegramWidgetReady(params)) onAuthRef.current(params)
    }

    const script = document.createElement('script')
    script.async = true
    script.src = 'https://telegram.org/js/telegram-widget.js?22'
    script.setAttribute('data-telegram-login', botUsername)
    script.setAttribute('data-size', size)
    script.setAttribute('data-onauth', `${callbackName}(user)`)
    if (requestAccess) script.setAttribute('data-request-access', 'write')
    script.onerror = () => setFailed(true)
    container.appendChild(script)

    return () => {
      delete globals[callbackName]
      // 清空容器(含脚本渲染出的 iframe),避免卸载后残留 + 全局回调泄露。
      container.innerHTML = ''
    }
  }, [botUsername, size, requestAccess])

  if (failed) {
    return (
      <p style={{ margin: 0, fontSize: 12, color: 'var(--hk-ink-500)' }}>
        Telegram 登录组件加载失败,请检查网络或稍后重试。
      </p>
    )
  }
  return <div ref={containerRef} style={{ display: 'flex', justifyContent: 'center' }} />
}
