import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useMe } from '../../auth/me'
import { listProviderAccounts } from '../accounts/api'
import { EMPTY_ACCOUNT_FILTERS } from '../accounts/query'
import { listApiKeys } from '../keys/api'
import { listPricing } from '../models/api'
import { getQuota } from '../usage/api'
import { accountCountLabel, keyCount, metricDisplay, quotaWindowCount, type MetricState } from './metrics'

/*
 * Dashboard 真数据指标条。四张卡各自独立加载:无权限端点(admin 账号)失败时该卡降级显"—",
 * 不连累其余卡与整页。账号=admin、Key/配额=session、模型=公开。
 */
export function DashboardMetrics() {
  const tenantId = useMe().tenantId
  const [accounts, setAccounts] = useState<MetricState<string>>({ status: 'loading' })
  const [keys, setKeys] = useState<MetricState<number>>({ status: 'loading' })
  const [models, setModels] = useState<MetricState<number>>({ status: 'loading' })
  const [quota, setQuota] = useState<MetricState<number>>({ status: 'loading' })

  useEffect(() => {
    const ctrl = new AbortController()
    const { signal } = ctrl

    // R=端点响应原型,V=卡片展示值类型;二者可不同(如模型端点返回数组、卡片展示其长度)。
    const run = <R, V>(p: Promise<R>, set: (s: MetricState<V>) => void, map: (v: R) => MetricState<V>) => {
      p.then((v) => {
        if (!signal.aborted) set(map(v))
      }).catch(() => {
        if (!signal.aborted) set({ status: 'unavailable' })
      })
    }

    if (tenantId != null) run(listProviderAccounts(tenantId, EMPTY_ACCOUNT_FILTERS, signal), setAccounts, (r) => ({ status: 'ok', value: accountCountLabel(r) }))
    run(listApiKeys(0, 1, signal), setKeys, (r) => ({ status: 'ok', value: keyCount(r) }))
    run(listPricing(signal), setModels, (r) => ({ status: 'ok', value: r.length }))
    run(getQuota(signal), setQuota, (r) => ({ status: 'ok', value: quotaWindowCount(r) }))

    return () => ctrl.abort()
  }, [tenantId])

  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 'var(--hk-space-4)' }}>
      <Card to="/accounts" label="上游账号" value={metricDisplay(accounts, (s) => s)} hint="账号池" />
      <Card to="/keys" label="API Key" value={metricDisplay(keys, String)} hint="已签发(活跃)" />
      <Card to="/models" label="可用模型" value={metricDisplay(models, String)} hint="公开价目" />
      <Card to="/usage" label="配额窗口" value={metricDisplay(quota, String)} hint="我的计费窗口" />
    </div>
  )
}

function Card({ to, label, value, hint }: { to: string; label: string; value: string; hint: string }) {
  return (
    <Link
      to={to}
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--hk-space-1)',
        padding: 'var(--hk-space-4)',
        background: 'var(--hk-surface)',
        border: '1px solid var(--hk-line)',
        borderRadius: 'var(--hk-radius-lg)',
        boxShadow: 'var(--hk-shadow-1)',
        textDecoration: 'none',
        color: 'inherit',
      }}
    >
      <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>{label}</span>
      <span style={{ fontSize: 28, fontWeight: 700, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-900)', lineHeight: 1.1 }}>{value}</span>
      <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{hint}</span>
    </Link>
  )
}
