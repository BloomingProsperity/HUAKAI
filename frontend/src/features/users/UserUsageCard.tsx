import { summarizeUserUsage, type UserUsageResponse } from './detail'

export function UserUsageCard({
  response,
  loading,
  error,
}: {
  response: UserUsageResponse | null
  loading: boolean
  error: string | null
}) {
  const summary = response ? summarizeUserUsage(response) : null

  return (
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>用量聚合</h3>
        <span style={{ marginLeft: 'auto', color: 'var(--hk-ink-300)', fontSize: 11 }}>
          {loading ? '刷新中…' : response?.next_cursor ? '当前批次，仍有更多明细' : '当前返回范围'}
        </span>
      </div>
      {error ? (
        <div style={{ margin: 'var(--hk-space-3) var(--hk-space-4)', padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-sm)', color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', fontSize: 13 }}>
          {error}
        </div>
      ) : loading && !response ? (
        <div className="hk-empty">加载用量中…</div>
      ) : !summary || summary.requestCount === 0 ? (
        <div className="hk-empty">暂无用量记录。</div>
      ) : (
        <div className="hk-card__body" style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
          <div className="hk-metric-grid" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))' }}>
            <Metric label="请求数" value={formatInteger(summary.requestCount)} />
            <Metric label="输入 Token" value={formatInteger(summary.inputTokens)} />
            <Metric label="输出 Token" value={formatInteger(summary.outputTokens)} />
            <Metric label="实际费用" value={summary.actualCost ?? '—'} mono />
          </div>
          <div className="hk-kv">
            <KV label="Token 合计（输入 + 输出）" value={formatInteger(summary.inputTokens + summary.outputTokens)} />
            <KV label="缓存创建 Token" value={formatInteger(summary.cacheCreationTokens)} />
            <KV label="缓存读取 Token" value={formatInteger(summary.cacheReadTokens)} />
            <KV
              label="请求结果"
              value={`成功 ${summary.successCount} / 错误 ${summary.errorCount} / 其他 ${summary.otherCount}`}
            />
          </div>
          {response?.next_cursor && (
            <div style={{ color: 'var(--hk-warn)', fontSize: 12 }}>
              该用户还有更多历史明细；以上合计仅覆盖本次返回的 {summary.requestCount} 条，不代表全量历史。
            </div>
          )}
        </div>
      )}
    </section>
  )
}

function Metric({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="hk-metric">
      <div className="hk-metric__label">{label}</div>
      <div className={mono ? 'hk-metric__v hk-mono' : 'hk-metric__v'}>{value}</div>
    </div>
  )
}

function KV({ label, value }: { label: string; value: string }) {
  return (
    <div className="hk-kv__r">
      <span className="hk-kv__k">{label}</span>
      <span className="hk-kv__v hk-mono">{value}</span>
    </div>
  )
}

function formatInteger(value: number): string {
  return new Intl.NumberFormat('zh-CN').format(value)
}
