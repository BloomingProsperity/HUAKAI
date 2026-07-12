import { useEffect, useState, type ReactNode } from 'react'
import { ApiError } from '../../lib/api'
import {
  getAuditMerkleTree,
  getAuditPubkeyByFingerprint,
  getCurrentAuditPubkey,
  getWellKnownPubkeys,
  listAuditPubkeys,
  verifyAuditEntry,
  verifyTrustProof,
} from './api'
import {
  formatTrustTime,
  keyStatusLabel,
  mapAuditVerification,
  mapTrustVerification,
  parseTrustProofJSON,
} from './trust'
import type {
  AuditMerkleTreeResponse,
  AuditPubkey,
  AuditVerifyResponse,
  TrustVerifyResponse,
  VerificationPresentation,
  WellKnownPubkeysResponse,
} from './types'

export function TrustPage() {
  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <div className="hk-eyebrow">HUAKAI 信任链</div>
          <h1>信任验证中心</h1>
          <p className="hk-sub">不用“相信平台说了算”：你可以核对签名公钥、请求证明与公开账本锚点。</p>
        </div>
      </header>

      <PlatformKeysSection />
      <ProofVerifySection />
      <MerkleAnchorSection />
    </div>
  )
}

function PlatformKeysSection() {
  const [current, setCurrent] = useState<AuditPubkey | null>(null)
  const [history, setHistory] = useState<AuditPubkey[]>([])
  const [wellKnown, setWellKnown] = useState<WellKnownPubkeysResponse | null>(null)
  const [selected, setSelected] = useState<AuditPubkey | null>(null)
  const [inspecting, setInspecting] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    void Promise.allSettled([
      getCurrentAuditPubkey(controller.signal),
      listAuditPubkeys(controller.signal),
      getWellKnownPubkeys(controller.signal),
    ]).then(([currentResult, historyResult, wellKnownResult]) => {
      if (controller.signal.aborted) return
      const errors: string[] = []
      if (currentResult.status === 'fulfilled') setCurrent(currentResult.value)
      else errors.push(errorText(currentResult.reason, '当前公钥加载失败'))
      if (historyResult.status === 'fulfilled') setHistory(historyResult.value.keys ?? [])
      else errors.push(errorText(historyResult.reason, '公钥历史加载失败'))
      if (wellKnownResult.status === 'fulfilled') setWellKnown(wellKnownResult.value)
      else errors.push(errorText(wellKnownResult.reason, '公开公钥清单加载失败'))
      setError(errors.length > 0 ? errors.join('；') : null)
      setLoading(false)
    })
    return () => controller.abort()
  }, [])

  const inspect = async (fingerprint: string) => {
    setInspecting(fingerprint)
    setError(null)
    try {
      setSelected(await getAuditPubkeyByFingerprint(fingerprint))
    } catch (cause) {
      setError(errorText(cause, '公钥详情加载失败'))
    } finally {
      setInspecting('')
    }
  }

  const statusFor = (key: AuditPubkey): string | undefined => (
    wellKnown?.keys.find((known) => known.kid === key.fingerprint)?.status ?? key.key_status
  )

  const currentStatus = current ? statusFor(current) : undefined

  return (
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>平台公钥</h3>
        {current && (
          <span className={`hk-pill ${currentStatus === 'active' ? 'hk-pill--ok' : 'hk-pill--crit'}`}>
            {keyStatusLabel(currentStatus)}
          </span>
        )}
      </div>
      <div className="hk-card__body hk-col">
        <p className="hk-section-copy">这是平台给证明签名的公开钥匙；指纹和轮换时间让历史证明仍能找到签发时使用的密钥。</p>
        {error && <ErrorBox>{error}</ErrorBox>}
        {current && wellKnown && !wellKnown.current && <ErrorBox>公开清单当前没有可采信的有效密钥。</ErrorBox>}
        {current && wellKnown?.current && current.fingerprint !== wellKnown.current && (
          <ErrorBox>审计当前公钥与公开清单的当前指纹不一致，请暂停采信新证明。</ErrorBox>
        )}
        {loading ? (
          <div className="hk-empty">正在读取公钥与轮换历史…</div>
        ) : (
          <>
            <div className="hk-grid hk-grid--2">
              <div className="hk-codebox">
                <strong>当前签名公钥</strong>
                <Detail label="算法" value={current?.algorithm || '—'} />
                <Detail label="指纹" value={current?.fingerprint || '—'} mono />
                <Detail label="状态" value={keyStatusLabel(currentStatus)} />
                <Detail label="生效时间" value={formatTrustTime(current?.effective_from)} />
                <Detail label="公钥(Base64)" value={current?.public_key_base64 || '—'} mono />
              </div>
              <div className="hk-codebox">
                <strong>公开发现信息</strong>
                <Detail label="清单版本" value={wellKnown?.schema_version || '—'} mono />
                <Detail label="当前指纹" value={wellKnown?.current || '—'} mono />
                <Detail label="清单生成" value={formatTrustTime(wellKnown?.generated_at)} />
                <Detail label="预计轮换后" value={formatTrustTime(wellKnown?.next_rotation_after)} />
                <Detail label="已吊销登记" value={String(wellKnown?.revoked?.length ?? 0)} />
              </div>
            </div>

            <div className="hk-tablewrap">
              <table className="hk-table">
                <thead>
                  <tr>
                    {['指纹', '算法', '状态', '生效时间', '失效时间', ''].map((title) => <th key={title || 'action'}>{title}</th>)}
                  </tr>
                </thead>
                <tbody>
                  {history.map((key) => (
                    <tr key={key.fingerprint}>
                      <td className="hk-mono">{key.fingerprint}</td>
                      <td>{key.algorithm}</td>
                      <td>
                        <span className={`hk-pill ${statusFor(key) === 'active' ? 'hk-pill--ok' : statusFor(key) === 'revoked' ? 'hk-pill--crit' : 'hk-pill--info'}`}>
                          {keyStatusLabel(statusFor(key))}
                        </span>
                      </td>
                      <td>{formatTrustTime(key.effective_from)}</td>
                      <td>{formatTrustTime(key.effective_to)}</td>
                      <td>
                        <button type="button" className="hk-btn hk-btn--sm" disabled={inspecting === key.fingerprint} onClick={() => inspect(key.fingerprint)}>
                          {inspecting === key.fingerprint ? '核对中…' : '按指纹核对'}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {history.length === 0 && <div className="hk-empty">暂无可展示的公钥历史。</div>}
            </div>

            {selected && (
              <div className="hk-codebox" aria-live="polite">
                <strong>按指纹核对结果</strong>
                <Detail label="指纹" value={selected.fingerprint} mono />
                <Detail label="公钥(Base64)" value={selected.public_key_base64} mono />
              </div>
            )}
          </>
        )}
      </div>
    </section>
  )
}

type VerifyMode = 'request' | 'proof'

function ProofVerifySection() {
  const [mode, setMode] = useState<VerifyMode>('request')
  const [requestID, setRequestID] = useState('')
  const [tenantScopeRef, setTenantScopeRef] = useState('')
  const [proofJSON, setProofJSON] = useState('')
  const [auditResult, setAuditResult] = useState<AuditVerifyResponse | null>(null)
  const [trustResult, setTrustResult] = useState<TrustVerifyResponse | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const switchMode = (next: VerifyMode) => {
    setMode(next)
    setAuditResult(null)
    setTrustResult(null)
    setError(null)
  }

  const verifyRequest = async () => {
    if (!requestID.trim() || !tenantScopeRef.trim()) {
      setError('request_id 与 tenant_scope_ref 都必须填写')
      return
    }
    setBusy(true)
    setError(null)
    setAuditResult(null)
    try {
      setAuditResult(await verifyAuditEntry({ request_id: requestID.trim(), tenant_scope_ref: tenantScopeRef.trim() }))
    } catch (cause) {
      setError(errorText(cause, '请求证明校验失败'))
    } finally {
      setBusy(false)
    }
  }

  const verifyProof = async () => {
    setBusy(true)
    setError(null)
    setTrustResult(null)
    try {
      const request = parseTrustProofJSON(proofJSON)
      setTrustResult(await verifyTrustProof(request))
    } catch (cause) {
      setError(errorText(cause, '证明校验失败'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="hk-card">
      <div className="hk-card__head"><h3>证明验证</h3></div>
      <div className="hk-card__body hk-col">
        <p className="hk-section-copy">这是把一次请求或一份签名回执交给后端验真；签名不匹配、密钥吊销或链锚点缺失都会明确标红。</p>
        <div className="hk-seg" role="tablist" aria-label="证明验证方式">
          <button type="button" role="tab" aria-selected={mode === 'request'} className={mode === 'request' ? 'is-on' : ''} onClick={() => switchMode('request')}>按请求 ID</button>
          <button type="button" role="tab" aria-selected={mode === 'proof'} className={mode === 'proof' ? 'is-on' : ''} onClick={() => switchMode('proof')}>粘贴证明 JSON</button>
        </div>

        {mode === 'request' ? (
          <div className="hk-form-grid">
            <label className="hk-field">
              <span>request_id</span>
              <input className="hk-input hk-mono" value={requestID} onChange={(event) => setRequestID(event.target.value)} placeholder="例如 req_…" />
            </label>
            <label className="hk-field">
              <span>tenant_scope_ref</span>
              <input className="hk-input hk-mono" value={tenantScopeRef} onChange={(event) => setTenantScopeRef(event.target.value)} placeholder="从请求响应头或收据取得" />
            </label>
            <div className="hk-field hk-field--actions">
              <span>隐私作用域</span>
              <button type="button" className="hk-btn hk-btn--green" disabled={busy || !requestID.trim() || !tenantScopeRef.trim()} onClick={verifyRequest}>
                {busy ? '校验中…' : '校验请求证明'}
              </button>
            </div>
          </div>
        ) : (
          <div className="hk-col">
            <label className="hk-field">
              <span>签名信封 JSON（payload、signature、pubkey_fingerprint）</span>
              <textarea
                className="hk-input hk-textarea hk-mono"
                rows={9}
                value={proofJSON}
                onChange={(event) => setProofJSON(event.target.value)}
                placeholder={'{\n  "payload": { "schema_version": "trust.receipt.v1", "request_id": "req_…" },\n  "signature": "…",\n  "pubkey_fingerprint": "0123456789abcdef"\n}'}
              />
            </label>
            <div className="hk-inline-actions">
              <button type="button" className="hk-btn hk-btn--green" disabled={busy || !proofJSON.trim()} onClick={verifyProof}>
                {busy ? '校验中…' : '校验粘贴证明'}
              </button>
            </div>
          </div>
        )}

        {error && <ErrorBox>{error}</ErrorBox>}
        {auditResult && <AuditVerificationResult result={auditResult} />}
        {trustResult && <TrustVerificationResult result={trustResult} />}
      </div>
    </section>
  )
}

function AuditVerificationResult({ result }: { result: AuditVerifyResponse }) {
  const presentation = mapAuditVerification(result)
  return (
    <VerificationResult presentation={presentation}>
      <Detail label="Ledger ID" value={result.ledger_entry.ledger_id} mono />
      <Detail label="请求 ID" value={result.ledger_entry.request_id} mono />
      <Detail label="账本时间" value={formatTrustTime(result.ledger_entry.timestamp)} />
      <Detail label="签名指纹" value={result.chain_proof.pubkey_fingerprint} mono />
      <Detail label="Merkle 根" value={result.chain_proof.merkle_root} mono />
    </VerificationResult>
  )
}

function TrustVerificationResult({ result }: { result: TrustVerifyResponse }) {
  return (
    <VerificationResult presentation={mapTrustVerification(result)}>
      <Detail label="状态" value={result.status || '—'} mono />
      <Detail label="密钥状态" value={keyStatusLabel(result.key_status)} />
      <Detail label="Canonical Hash" value={result.canonical_hash || '—'} mono />
      <Detail label="证明版本" value={result.schema_version || '—'} mono />
    </VerificationResult>
  )
}

function VerificationResult({ presentation, children }: { presentation: VerificationPresentation; children: ReactNode }) {
  return (
    <div className={`hk-verify-result ${presentation.passed ? 'hk-verify-result--ok' : 'hk-verify-result--no'}`} aria-live="polite">
      <div className="hk-inline-actions">
        <span className={`hk-pill hk-pill--${presentation.tone}`}>{presentation.label}</span>
        <span className={presentation.passed ? 'hk-result--ok' : 'hk-result--no'}>{presentation.signature}</span>
        {presentation.chain && <span className={presentation.passed ? 'hk-result--ok' : 'hk-result--no'}>{presentation.chain}</span>}
      </div>
      <p className="hk-section-copy">{presentation.detail}</p>
      <div className="hk-kv">{children}</div>
    </div>
  )
}

function MerkleAnchorSection() {
  const [tree, setTree] = useState<AuditMerkleTreeResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [nonce, setNonce] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError(null)
    getAuditMerkleTree(controller.signal)
      .then(setTree)
      .catch((cause: unknown) => {
        if (!controller.signal.aborted) setError(errorText(cause, 'Merkle 锚点加载失败'))
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [nonce])

  return (
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>Merkle 锚点</h3>
        <button type="button" className="hk-act" disabled={loading} onClick={() => setNonce((value) => value + 1)}>刷新</button>
      </div>
      <div className="hk-card__body hk-col">
        <p className="hk-section-copy">这是当前审计账本的公开摘要；任何历史条目被改动都会让后续根值无法与已记录锚点对上。</p>
        {error && <ErrorBox>{error}</ErrorBox>}
        <div className="hk-codebox">
          <Detail label="账本条目数" value={loading ? '读取中…' : String(tree?.size ?? 0)} />
          <Detail label="最新 Merkle 根" value={loading ? '读取中…' : (tree?.latest_merkle_root || '—')} mono />
        </div>
      </div>
    </section>
  )
}

function Detail({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="hk-kv__r">
      <span className="hk-kv__k">{label}</span>
      <span className={`hk-kv__v ${mono ? 'hk-mono' : ''}`}>{value}</span>
    </div>
  )
}

function ErrorBox({ children }: { children: ReactNode }) {
  return <div className="hk-errorbox" role="alert">{children}</div>
}

function errorText(cause: unknown, fallback: string): string {
  if (cause instanceof ApiError) return `${cause.message}(${cause.code})`
  if (cause instanceof Error && cause.message) return cause.message
  return fallback
}
