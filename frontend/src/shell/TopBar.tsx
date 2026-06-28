import { Link, useNavigate } from 'react-router-dom'
import { clearAll, useAuth } from '../auth/store'
import { logout } from '../auth/api'

/*
 * 顶栏:品牌 + 命令面板入口(Cmd-K 签名交互挂载点)+ 当前用户 / 登出。
 * onOpenHermes:由 AppShell 在运营台壳注入,点击 Cmd-K 按钮唤起 Hermes 面板;非运营台壳为 undefined。
 */
export function TopBar({ onOpenHermes }: { onOpenHermes?: () => void } = {}) {
  const auth = useAuth()
  const nav = useNavigate()
  const onLogout = async () => {
    await logout()
    clearAll()
    nav('/login', { replace: true })
  }
  return innerTopBar(auth.user?.email, onLogout, onOpenHermes)
}

function innerTopBar(email: string | undefined, onLogout: () => void, onOpenHermes?: () => void) {
  return (
    <header
      style={{
        height: 56,
        flexShrink: 0,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        padding: '0 var(--hk-space-5)',
        background: 'var(--hk-surface)',
        borderBottom: '1px solid var(--hk-line)',
        zIndex: 'var(--hk-z-topbar)' as unknown as number,
      }}
    >
      <Link
        to="/"
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--hk-space-2)',
          color: 'var(--hk-ink-900)',
          fontWeight: 700,
          fontSize: 16,
          letterSpacing: '-0.01em',
        }}
      >
        <span
          aria-hidden
          style={{
            width: 22,
            height: 22,
            borderRadius: 'var(--hk-radius-sm)',
            background: 'linear-gradient(135deg, var(--hk-primary-500), var(--hk-primary-700))',
            display: 'inline-block',
          }}
        />
        HUAKAI 控制台
      </Link>
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)' }}>
        <button
          type="button"
          aria-label={onOpenHermes ? '唤起 Hermes 运维助手' : '命令面板'}
          title={onOpenHermes ? 'Hermes 运维助手(⌘K)' : undefined}
          onClick={onOpenHermes}
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--hk-space-2)',
            padding: 'var(--hk-space-1) var(--hk-space-3)',
            fontSize: 13,
            color: 'var(--hk-ink-500)',
            background: 'var(--hk-surface-sunken)',
            border: '1px solid var(--hk-line)',
            borderRadius: 'var(--hk-radius-md)',
            cursor: 'pointer',
          }}
        >
          {onOpenHermes ? 'Hermes' : '快速跳转'}
          <kbd
            style={{
              fontFamily: 'var(--hk-font-mono)',
              fontSize: 11,
              color: 'var(--hk-ink-700)',
              background: 'var(--hk-surface)',
              border: '1px solid var(--hk-line)',
              borderRadius: 'var(--hk-radius-sm)',
              padding: '0 4px',
            }}
          >
            ⌘K
          </kbd>
        </button>
        {email && <span style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>{email}</span>}
        <button
          type="button"
          onClick={onLogout}
          style={{ height: 30, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }}
        >
          登出
        </button>
      </div>
    </header>
  )
}
