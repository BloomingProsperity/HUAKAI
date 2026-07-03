// HUAKAI 首页共享原语 —— 由 Claude Design 原型移植而来。
// 原型用 window.HUAKAIDesignSystem_36f9be.* 全局 + Lucide CDN;这里落成真 React+TS,
// 图标用内联 SVG(Lucide 同款 stroke 风格,免外部依赖)。样式 token 见 landingStyles.ts。
import type { CSSProperties, ReactNode } from 'react'

// ── 内联 SVG 图标(Lucide 路径,24×24 viewBox,currentColor stroke)──────────────
const ICON_PATHS: Record<string, ReactNode> = {
  github: (
    <>
      <path d="M15 22v-4a4.8 4.8 0 0 0-1-3.5c3 0 6-2 6-5.5.08-1.25-.27-2.48-1-3.5.28-1.15.28-2.35 0-3.5 0 0-1 0-3 1.5-2.64-.5-5.36-.5-8 0C6 2 5 2 5 2c-.3 1.15-.3 2.35 0 3.5A5.4 5.4 0 0 0 4 9c0 3.5 3 5.5 6 5.5-.39.49-.68 1.05-.85 1.65-.17.6-.22 1.23-.15 1.85v4" />
      <path d="M9 18c-4.51 2-5-2-7-2" />
    </>
  ),
  'arrow-right': (
    <>
      <path d="M5 12h14" />
      <path d="m12 5 7 7-7 7" />
    </>
  ),
  terminal: (
    <>
      <polyline points="4 17 10 11 4 5" />
      <line x1="12" x2="20" y1="19" y2="19" />
    </>
  ),
  'git-merge': (
    <>
      <circle cx="18" cy="18" r="3" />
      <circle cx="6" cy="6" r="3" />
      <path d="M6 21V9a9 9 0 0 0 9 9" />
    </>
  ),
  plus: (
    <>
      <path d="M5 12h14" />
      <path d="M12 5v14" />
    </>
  ),
  'heart-pulse': (
    <>
      <path d="M19 14c1.49-1.46 3-3.21 3-5.5A5.5 5.5 0 0 0 16.5 3c-1.76 0-3 .5-4.5 2-1.5-1.5-2.74-2-4.5-2A5.5 5.5 0 0 0 2 8.5c0 2.3 1.5 4.05 3 5.5l7 7Z" />
      <path d="M3.22 12H9.5l.5-1 2 4.5 2-7 1.5 3.5h5.27" />
    </>
  ),
  'refresh-cw': (
    <>
      <path d="M21 12a9 9 0 0 0-9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" />
      <path d="M3 3v5h5" />
      <path d="M3 12a9 9 0 0 0 9 9 9.75 9.75 0 0 0 6.74-2.74L21 16" />
      <path d="M16 16h5v5" />
    </>
  ),
  'bar-chart-3': (
    <>
      <path d="M3 3v18h18" />
      <path d="M18 17V9" />
      <path d="M13 17V5" />
      <path d="M8 17v-3" />
    </>
  ),
  'file-check-2': (
    <>
      <path d="M4 22h14a2 2 0 0 0 2-2V7l-5-5H6a2 2 0 0 0-2 2v4" />
      <path d="M14 2v4a2 2 0 0 0 2 2h4" />
      <path d="m3 15 2 2 4-4" />
    </>
  ),
  server: (
    <>
      <rect width="20" height="8" x="2" y="2" rx="2" ry="2" />
      <rect width="20" height="8" x="2" y="14" rx="2" ry="2" />
      <line x1="6" x2="6.01" y1="6" y2="6" />
      <line x1="6" x2="6.01" y1="18" y2="18" />
    </>
  ),
  copy: (
    <>
      <rect width="14" height="14" x="8" y="8" rx="2" ry="2" />
      <path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" />
    </>
  ),
  'book-open': (
    <>
      <path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2z" />
      <path d="M22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z" />
    </>
  ),
}

export interface IconProps {
  name: string
  size?: number
  color?: string
  style?: CSSProperties
}

export function Icon({ name, size = 16, color, style }: IconProps): JSX.Element {
  const path = ICON_PATHS[name]
  return (
    <span style={{ display: 'inline-flex', lineHeight: 0, ...style }} aria-hidden="true">
      <svg
        width={size}
        height={size}
        viewBox="0 0 24 24"
        fill="none"
        stroke={color || 'currentColor'}
        strokeWidth={2}
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        {path}
      </svg>
    </span>
  )
}

// ── 主按钮(柠檬绿填充)──────────────────────────────────────────────────────
export interface ButtonProps {
  children: ReactNode
  onClick?: () => void
  size?: 'md' | 'lg'
  type?: 'button' | 'submit'
  style?: CSSProperties
}

export function Button({ children, onClick, size = 'md', type = 'button', style }: ButtonProps): JSX.Element {
  const lg = size === 'lg'
  return (
    <button
      type={type}
      onClick={onClick}
      className="hk-btn"
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 8,
        height: lg ? '2.75rem' : '2.5rem',
        padding: lg ? '0 1.75rem' : '0 1.25rem',
        borderRadius: 'var(--radius-md)',
        border: 'none',
        background: 'var(--accent)',
        color: 'var(--text-on-primary)',
        fontSize: lg ? 16 : 14,
        fontWeight: 600,
        fontFamily: 'var(--font-sans)',
        cursor: 'pointer',
        boxShadow: 'var(--shadow-glow)',
        transition: 'background var(--dur-fast), transform var(--dur-fast)',
        ...style,
      }}
    >
      {children}
    </button>
  )
}

// ── 徽标 ─────────────────────────────────────────────────────────────────────
export interface BadgeProps {
  children: ReactNode
  variant?: 'default' | 'outline'
  style?: CSSProperties
}

export function Badge({ children, variant = 'default', style }: BadgeProps): JSX.Element {
  const base: CSSProperties =
    variant === 'outline'
      ? { background: 'transparent', color: 'var(--text-muted)', border: '1px solid var(--border-strong)' }
      : { background: 'var(--accent-soft-bg)', color: 'var(--accent-soft-text)', border: '1px solid var(--accent-soft-border)' }
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        fontSize: 11.5,
        fontWeight: 600,
        lineHeight: 1.4,
        padding: '2px 8px',
        borderRadius: 'var(--radius-full)',
        ...base,
        ...style,
      }}
    >
      {children}
    </span>
  )
}

// ── 状态点(在线/空闲/离线,可脉冲)──────────────────────────────────────────
export interface StatusDotProps {
  tone?: 'online' | 'idle' | 'down'
  pulse?: boolean
}

const STATUS_COLOR: Record<NonNullable<StatusDotProps['tone']>, string> = {
  online: 'var(--success-fg)',
  idle: '#d97706',
  down: '#dc2626',
}

export function StatusDot({ tone = 'online', pulse = false }: StatusDotProps): JSX.Element {
  const color = STATUS_COLOR[tone]
  return (
    <span
      className={pulse ? 'hk-statusdot hk-statusdot-pulse' : 'hk-statusdot'}
      style={{ width: 8, height: 8, borderRadius: '50%', background: color, color, display: 'inline-block', flexShrink: 0 }}
    />
  )
}

// ── 品牌锁标(HK 字标 + HUAKAI 文字)—— Nav 与 Footer 共用 ────────────────────
export interface HKLogoProps {
  size?: number
  sub?: string
}

export function HKLogo({ size = 40, sub = '华凯' }: HKLogoProps): JSX.Element {
  return (
    <a href="#top" style={{ display: 'flex', alignItems: 'center', gap: 12, textDecoration: 'none' }}>
      <span
        style={{
          width: size,
          height: size,
          flexShrink: 0,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          borderRadius: 10,
          background: 'var(--primary-500)',
          color: '#fff',
          fontWeight: 700,
          fontSize: size * 0.36,
          boxShadow: 'var(--shadow-glow)',
          letterSpacing: '-0.02em',
        }}
      >
        HK
      </span>
      <span style={{ display: 'flex', flexDirection: 'column', lineHeight: 1.1 }}>
        <span style={{ fontSize: 16, fontWeight: 700, color: 'var(--text-strong)', letterSpacing: '-0.01em' }}>HUAKAI</span>
        {sub ? <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{sub}</span> : null}
      </span>
    </a>
  )
}
