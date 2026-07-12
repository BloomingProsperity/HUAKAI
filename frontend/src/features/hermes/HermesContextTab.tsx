import React, { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { getModuleContext, type HermesModuleView } from './hermesClient'
import { groupModulesByCategory, probeTone } from './hermesHistory'
import * as S from './hermesPanelStyles'

/*
 * Hermes 只读模块上下文页(GET /v1/hermes/context)。把后端合并后的模块知识视图
 *(身份 + 能力 + 实时探针枚举状态)按 category 聚合展示,供运维者了解「什么已接线、健康如何」。
 * 纯只读:仅含模块身份与枚举状态,绝不含密钥或用户数据。请求显式带 admin Bearer + as_user_id/tenant_id。
 */

interface Props {
  adminToken: string
  asUserId: number
  tenantId?: number
}

export function HermesContextTab({ adminToken, asUserId, tenantId }: Props) {
  const [modules, setModules] = useState<HermesModuleView[] | null>(null)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    setErr(null)
    void getModuleContext(adminToken, { asUserId, tenantId }, ctrl.signal)
      .then((m) => setModules(m))
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return
        setErr(e instanceof ApiError || e instanceof Error ? e.message : '加载失败')
      })
    return () => ctrl.abort()
  }, [adminToken, asUserId, tenantId])

  const groups = groupModulesByCategory(modules ?? [])

  return (
    <div style={S.messageScroll}>
      {err && (
        <div style={S.errorBox}>
          <strong>加载失败</strong>
          <div style={{ marginTop: 4 }}>{err}</div>
        </div>
      )}
      {modules === null && !err ? (
        <p style={muted}>加载中…</p>
      ) : (modules ?? []).length === 0 ? (
        <p style={muted}>暂无模块上下文。</p>
      ) : (
        groups.map((g) => (
          <React.Fragment key={g.category}>
            <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--hk-ink-500)', marginTop: 'var(--hk-space-2)' }}>
              {g.category}({g.modules.length})
            </div>
            {g.modules.map((m) => {
              const tone = probeTone(m.live_probe.status)
              return (
                <div key={m.id} style={S.moduleRow}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', gap: 'var(--hk-space-2)', alignItems: 'center' }}>
                    <strong style={{ fontSize: 12, color: 'var(--hk-ink-900)' }}>{m.title || m.id}</strong>
                    <span style={S.probePill[tone]}>{m.live_probe.status || '未知'}</span>
                  </div>
                  {m.live_probe.detail && (
                    <div style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>{m.live_probe.detail}</div>
                  )}
                  {m.capabilities && m.capabilities.length > 0 && (
                    <div style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>能力:{m.capabilities.join('、')}</div>
                  )}
                  {m.catalog?.status && (
                    <div style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>
                      状态:{m.catalog.status}
                      {m.catalog.parity ? ` · parity ${m.catalog.parity}` : ''}
                    </div>
                  )}
                </div>
              )
            })}
          </React.Fragment>
        ))
      )}
    </div>
  )
}

const muted: React.CSSProperties = { fontSize: 12, color: 'var(--hk-ink-500)', margin: 0 }
