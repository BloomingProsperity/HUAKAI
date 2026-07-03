// HUAKAI 官网 — 品牌标识 + 吸顶顶部导航栏。
import { useState, useEffect } from 'react'
import { Button, Icon, HKLogo } from '../landingKit'

// 导航链接条目类型
type NavLink = {
  href: string
  label: string
}

export function SiteNav() {
  // 滚动超过 8px 时切换为玻璃拟态背景 + 边框
  const [scrolled, setScrolled] = useState(false)
  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 8)
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  const links: NavLink[] = [
    { href: '#features', label: '能力' },
    { href: '#providers', label: '供应商' },
    { href: '#deploy', label: '自托管' },
    { href: '#', label: '文档' },
  ]

  return (
    <header style={{
      position: 'sticky', top: 0, zIndex: 50,
      borderBottom: `1px solid ${scrolled ? 'var(--border)' : 'transparent'}`,
      background: scrolled ? 'rgba(255,255,255,0.82)' : 'rgba(255,255,255,0)',
      backdropFilter: scrolled ? 'blur(16px)' : 'none',
      transition: 'background var(--dur-base) var(--ease-standard), border-color var(--dur-base)',
    }}>
      <div className="hk-container" style={{ height: 68, display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 24 }}>
        <HKLogo />
        <nav className="hk-nav-links" style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
          {links.map((l) => (
            <a key={l.label} href={l.href} className="hk-nav-link" style={{
              padding: '8px 12px', borderRadius: 8, fontSize: 14, fontWeight: 500,
              color: 'var(--text-muted)', textDecoration: 'none',
              transition: 'color var(--dur-fast), background var(--dur-fast)',
            }}>{l.label}</a>
          ))}
        </nav>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <a href="https://github.com/BloomingProsperity/HUAKAI" target="_blank" rel="noreferrer" className="hk-ghost-link" style={{
            display: 'inline-flex', alignItems: 'center', gap: 8, height: 40, padding: '0 14px',
            borderRadius: 'var(--radius-md)', border: '1px solid var(--border-strong)', background: 'transparent',
            color: 'var(--text-body)', fontSize: 14, fontWeight: 500, textDecoration: 'none',
            transition: 'border-color var(--dur-fast), color var(--dur-fast)',
          }}>
            <Icon name="github" /> <span className="hk-hide-sm">GitHub</span>
          </a>
          <Button onClick={() => { window.location.href = '/' }}>
            进入控制台 <Icon name="arrow-right" />
          </Button>
        </div>
      </div>
    </header>
  )
}
