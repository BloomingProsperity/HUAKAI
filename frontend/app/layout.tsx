import type { Metadata } from 'next';
import './globals.css';

// 全局 metadata
export const metadata: Metadata = {
  title: 'HUAKAI 反代联调控制台',
  description: 'HUAKAI gateway debug console — 6 panels',
};

// 导航链接定义
const NAV_LINKS = [
  { href: '/', label: 'Home' },
  { href: '/accounts', label: '1 Accounts' },
  { href: '/bindings', label: '2 Bindings' },
  { href: '/chat', label: '3 Chat' },
  { href: '/selection', label: '4 Selection' },
  { href: '/renew', label: '5 Renew' },
  { href: '/mimicry', label: '6 Mimicry' },
  { href: '/observability', label: 'Obs' },
];

// 极简 layout：header + nav（6 panel + Observability）
export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh-CN">
      <body>
        <header>
          <span className="brand">HUAKAI</span>
          <nav style={{ display: 'flex', gap: '0.75rem', flexWrap: 'wrap' }}>
            {NAV_LINKS.map(({ href, label }) => (
              <a key={href} href={href}>{label}</a>
            ))}
          </nav>
        </header>
        <main>{children}</main>
      </body>
    </html>
  );
}
