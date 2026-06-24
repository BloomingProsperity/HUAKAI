import { Link } from 'react-router-dom'

/*
 * 顶栏:品牌 + 命令面板入口(Cmd-K 签名交互的挂载点,脚手架阶段先放可视入口,
 * 后续切片接全局快捷导航/动作)。
 */
export function TopBar() {
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
      <button
        type="button"
        aria-label="命令面板"
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
        快速跳转
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
    </header>
  )
}
