import { useState } from 'react'
import { DEFAULT_TENANT_ID } from './alerting'
import { EventsTab } from './EventsTab'
import { RulesTab } from './RulesTab'
import { SilencesTab } from './SilencesTab'
import { Field, inp } from './ui'

/*
 * Ops 告警控制台(运营台 · 安全审计)。三资源 Tab:告警规则 / 告警事件 / 静默规则。
 * 全部走 /v1/admin/alert-*(admin token)。管理端点需 tenant_id;单租户部署默认 1,顶栏可改,
 * 切 tenant 后各 Tab 通过 key 重挂以重新加载。真码:backend/internal/alertinghttp、
 * backend/cmd/gateway/routes_alerting.go:9(mountAlertingAdminRoutes,admin 鉴权)。
 */

type TabKey = 'rules' | 'events' | 'silences'

const TABS: ReadonlyArray<{ key: TabKey; label: string }> = [
  { key: 'rules', label: '告警规则' },
  { key: 'events', label: '告警事件' },
  { key: 'silences', label: '静默规则' },
]

export function AlertingPage() {
  const [tenantId, setTenantId] = useState(DEFAULT_TENANT_ID)
  const [tab, setTab] = useState<TabKey>('rules')

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--hk-space-3)' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
          <h1 style={{ fontSize: 22 }}>告警控制台</h1>
          <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
            运营台 · 阈值告警规则、触发事件与静默窗口。指标命中规则即产生事件,可手动恢复或用静默临时抑制通知。
          </p>
        </div>
        <Field label="租户 ID" flex={0}>
          <input
            value={tenantId}
            inputMode="numeric"
            onChange={(e) => {
              const v = Number.parseInt(e.target.value, 10)
              setTenantId(Number.isInteger(v) && v > 0 ? v : DEFAULT_TENANT_ID)
            }}
            style={{ ...inp, width: 96 }}
          />
        </Field>
      </header>

      <nav style={{ display: 'flex', gap: 'var(--hk-space-1)', borderBottom: '1px solid var(--hk-line)' }}>
        {TABS.map((t) => {
          const active = t.key === tab
          return (
            <button
              key={t.key}
              type="button"
              onClick={() => setTab(t.key)}
              style={{
                appearance: 'none',
                border: 'none',
                background: 'transparent',
                padding: 'var(--hk-space-2) var(--hk-space-4)',
                fontSize: 14,
                fontWeight: active ? 600 : 400,
                color: active ? 'var(--hk-primary-700)' : 'var(--hk-ink-500)',
                borderBottom: active ? '2px solid var(--hk-primary-500)' : '2px solid transparent',
                marginBottom: -1,
                cursor: 'pointer',
              }}
            >
              {t.label}
            </button>
          )
        })}
      </nav>

      {/* key=tenantId 保证切租户后子 Tab 重挂、重新拉数据 */}
      {tab === 'rules' && <RulesTab key={tenantId} tenantId={tenantId} />}
      {tab === 'events' && <EventsTab key={tenantId} tenantId={tenantId} />}
      {tab === 'silences' && <SilencesTab key={tenantId} tenantId={tenantId} />}
    </div>
  )
}
