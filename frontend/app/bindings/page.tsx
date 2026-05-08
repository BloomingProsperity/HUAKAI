'use client';

import { useState, useEffect, useCallback } from 'react';
import { listPoolGroups, createPoolGroup, updatePoolGroup } from '../../lib/api/pools';
import { listProviderAccounts } from '../../lib/api/providerAccounts';
import { ApiError } from '../../lib/api/client';
import type { PoolGroup, PoolGroupCreate, PoolGroupUpdate, ProviderAccount } from '../../lib/api/types';

export default function BindingsPage() {
  const [pools, setPools] = useState<PoolGroup[]>([]);
  const [accounts, setAccounts] = useState<ProviderAccount[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  // 新建 pool 表单
  const [showCreate, setShowCreate] = useState(false);
  const [createForm, setCreateForm] = useState<PoolGroupCreate>({
    name: '',
    routing_policy_version: '1.0',
    top_k_default: 1,
    capability_default: 'exact_capability_only',
    allow_tenant_operator_force: false,
    allow_last_resort: false,
    allow_mid_stream_failover: false,
  });
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState('');

  // 编辑某个 pool
  const [editId, setEditId] = useState<number | null>(null);
  const [editForm, setEditForm] = useState<PoolGroupUpdate>({});
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [pg, pa] = await Promise.all([
        listPoolGroups({ limit: 50 }),
        listProviderAccounts({ limit: 100 }),
      ]);
      setPools(pg.items);
      setAccounts(pa.items);
    } catch (err: unknown) {
      if (err instanceof ApiError && err.isNotImplemented()) {
        setError('__501__');
      } else {
        setError(String(err));
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  async function handleCreate() {
    setCreating(true);
    setCreateError('');
    try {
      await createPoolGroup(createForm);
      setShowCreate(false);
      setCreateForm({ name: '', routing_policy_version: '1.0', top_k_default: 1, capability_default: 'exact_capability_only', allow_tenant_operator_force: false, allow_last_resort: false, allow_mid_stream_failover: false });
      await load();
    } catch (err: unknown) {
      setCreateError(String(err));
    } finally {
      setCreating(false);
    }
  }

  async function handleSave(id: number) {
    setSaving(true);
    setSaveError('');
    try {
      await updatePoolGroup(id, editForm);
      setEditId(null);
      await load();
    } catch (err: unknown) {
      setSaveError(String(err));
    } finally {
      setSaving(false);
    }
  }

  function startEdit(pool: PoolGroup) {
    setEditId(pool.id);
    setEditForm({
      enabled: pool.enabled,
      top_k_default: pool.top_k_default,
      capability_default: pool.capability_default,
      allow_tenant_operator_force: pool.allow_tenant_operator_force,
      allow_last_resort: pool.allow_last_resort,
      allow_mid_stream_failover: pool.allow_mid_stream_failover,
      sticky_wait_timeout_ms: pool.sticky_wait_timeout_ms,
      fallback_wait_timeout_ms: pool.fallback_wait_timeout_ms,
    });
    setSaveError('');
  }

  // 某 pool 下属账号（通过 pool_mode / channel_id 关联，此处 mock 显示前 3 个账号）
  // 真实情况需要后端提供 pool_account_binding 接口
  function accountsForPool(pool: PoolGroup): ProviderAccount[] {
    // 暂时用 channel_id 做粗匹配展示，真实绑定逻辑由后端 pool_routing 控制
    void pool;
    return accounts.slice(0, 3);
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: '1rem' }}>
        <h1 style={{ fontSize: '1rem', color: '#e6edf3', fontWeight: 600 }}>
          面板 2 — Pool Groups & Model Bindings
        </h1>
        <button onClick={load} disabled={loading} style={{ marginLeft: 'auto', background: '#21262d' }}>
          {loading ? '刷新中…' : '刷新'}
        </button>
        <button onClick={() => { setShowCreate(!showCreate); setCreateError(''); }}>
          {showCreate ? '取消' : '+ 新建 Pool'}
        </button>
      </div>

      {error && (
        error === '__501__'
          ? <div className="not-impl-msg">后端尚未实现此端点 (501) — 等待 backend wire</div>
          : <div className="error-msg">{error}</div>
      )}

      {/* 新建 Pool 表单 */}
      {showCreate && (
        <div style={{ background: '#161b22', border: '1px solid #30363d', borderRadius: 6, padding: '1rem' }}>
          <div className="section-title">新建 Pool Group</div>
          <div className="field-group" style={{ marginTop: '0.75rem' }}>
            <div>
              <label>name</label>
              <input type="text" value={createForm.name}
                onChange={(e) => setCreateForm((f) => ({ ...f, name: e.target.value }))}
                placeholder="my-pool" />
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: '0.5rem' }}>
              <div>
                <label>routing_policy_version</label>
                <input type="text" value={createForm.routing_policy_version ?? '1.0'}
                  onChange={(e) => setCreateForm((f) => ({ ...f, routing_policy_version: e.target.value }))} />
              </div>
              <div>
                <label>top_k_default</label>
                <input type="text" value={createForm.top_k_default ?? 1}
                  onChange={(e) => setCreateForm((f) => ({ ...f, top_k_default: Number(e.target.value) }))} />
              </div>
              <div>
                <label>capability_default</label>
                <select value={createForm.capability_default ?? 'exact_capability_only'}
                  onChange={(e) => setCreateForm((f) => ({ ...f, capability_default: e.target.value as PoolGroupCreate['capability_default'] }))}>
                  <option value="exact_capability_only">exact_capability_only</option>
                  <option value="safe_equivalent_allowed">safe_equivalent_allowed</option>
                </select>
              </div>
            </div>
            <div style={{ display: 'flex', gap: '1.5rem', flexWrap: 'wrap' }}>
              {(['allow_tenant_operator_force', 'allow_last_resort', 'allow_mid_stream_failover'] as const).map((key) => (
                <label key={key} className="checkbox-row">
                  <input type="checkbox" checked={!!createForm[key]}
                    onChange={(e) => setCreateForm((f) => ({ ...f, [key]: e.target.checked }))} />
                  {key}
                </label>
              ))}
            </div>
          </div>
          {createError && <div className="error-msg">{createError}</div>}
          <button onClick={handleCreate} disabled={creating || !createForm.name}>
            {creating ? '创建中…' : '创建 Pool'}
          </button>
        </div>
      )}

      {/* Pool 列表 */}
      {pools.length === 0 && !loading && (
        <div style={{ color: '#484f58', fontSize: '0.875rem' }}>（无 Pool Group）</div>
      )}

      {pools.map((pool) => (
        <div key={pool.id} style={{ background: '#161b22', border: '1px solid #30363d', borderRadius: 6, overflow: 'hidden' }}>
          {/* Pool header */}
          <div style={{ padding: '0.6rem 0.75rem', borderBottom: '1px solid #30363d', display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
            <span style={{ fontFamily: 'monospace', color: '#8b949e', fontSize: '0.75rem' }}>#{pool.id}</span>
            <span style={{ color: '#e6edf3', fontWeight: 600 }}>{pool.name}</span>
            <span style={{ fontSize: '0.75rem', color: '#8b949e' }}>v{pool.routing_policy_version}</span>
            <span style={{ fontSize: '0.75rem', color: '#8b949e' }}>top_k={pool.top_k_default}</span>
            <span style={{ fontSize: '0.75rem', color: pool.enabled ? '#3fb950' : '#f85149' }}>
              {pool.enabled ? 'enabled' : 'disabled'}
            </span>
            <div style={{ marginLeft: 'auto', display: 'flex', gap: '0.4rem' }}>
              <button onClick={() => startEdit(pool)}
                style={{ background: '#21262d', fontSize: '0.75rem', padding: '0.25rem 0.6rem' }}>
                编辑
              </button>
            </div>
          </div>

          {/* Pool 配置详情 */}
          <div style={{ padding: '0.5rem 0.75rem', display: 'flex', gap: '1.5rem', flexWrap: 'wrap', fontSize: '0.75rem', color: '#8b949e' }}>
            <span>capability_default: <strong style={{ color: '#c9d1d9' }}>{pool.capability_default}</strong></span>
            <span>allow_force: <strong style={{ color: pool.allow_tenant_operator_force ? '#3fb950' : '#484f58' }}>{String(pool.allow_tenant_operator_force)}</strong></span>
            <span>allow_last_resort: <strong style={{ color: pool.allow_last_resort ? '#3fb950' : '#484f58' }}>{String(pool.allow_last_resort)}</strong></span>
            <span>mid_stream_failover: <strong style={{ color: pool.allow_mid_stream_failover ? '#3fb950' : '#484f58' }}>{String(pool.allow_mid_stream_failover)}</strong></span>
            <span>sticky_timeout: <strong style={{ color: '#c9d1d9' }}>{pool.sticky_wait_timeout_ms}ms</strong></span>
          </div>

          {/* 行内编辑 */}
          {editId === pool.id && (
            <div style={{ padding: '0.75rem', background: '#0d1117', borderTop: '1px solid #30363d', display: 'flex', gap: '1rem', flexWrap: 'wrap', alignItems: 'flex-end' }}>
              <div>
                <label>enabled</label>
                <select value={editForm.enabled ? 'true' : 'false'}
                  onChange={(e) => setEditForm((f) => ({ ...f, enabled: e.target.value === 'true' }))}>
                  <option value="true">true</option>
                  <option value="false">false</option>
                </select>
              </div>
              <div>
                <label>top_k_default</label>
                <input type="text" value={editForm.top_k_default ?? pool.top_k_default}
                  onChange={(e) => setEditForm((f) => ({ ...f, top_k_default: Number(e.target.value) }))}
                  style={{ width: 60 }} />
              </div>
              <div>
                <label>capability_default</label>
                <select value={editForm.capability_default ?? pool.capability_default}
                  onChange={(e) => setEditForm((f) => ({ ...f, capability_default: e.target.value as PoolGroupUpdate['capability_default'] }))}>
                  <option value="exact_capability_only">exact_capability_only</option>
                  <option value="safe_equivalent_allowed">safe_equivalent_allowed</option>
                </select>
              </div>
              <div>
                <label>sticky_wait_timeout_ms</label>
                <input type="text" value={editForm.sticky_wait_timeout_ms ?? pool.sticky_wait_timeout_ms}
                  onChange={(e) => setEditForm((f) => ({ ...f, sticky_wait_timeout_ms: Number(e.target.value) }))}
                  style={{ width: 80 }} />
              </div>
              <div style={{ display: 'flex', gap: '0.5rem' }}>
                <button onClick={() => handleSave(pool.id)} disabled={saving}>{saving ? '保存中…' : '保存'}</button>
                <button onClick={() => setEditId(null)} style={{ background: '#21262d' }}>取消</button>
              </div>
              {saveError && <div className="error-msg">{saveError}</div>}
            </div>
          )}

          {/* 关联账号预览（model alias binding） */}
          <div style={{ padding: '0.5rem 0.75rem', borderTop: '1px solid #21262d' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', marginBottom: '0.4rem' }}>
              <div className="section-title" style={{ marginTop: 0, marginBottom: 0 }}>
                关联账号（model alias binding）
              </div>
              {/* MOCK 徽章：账号→pool 真实绑定 endpoint 尚未实现 */}
              <span style={{
                background: '#3b1f6e', color: '#a371f7', border: '1px solid #6e40c9',
                borderRadius: 3, fontSize: '0.6rem', padding: '0.1rem 0.4rem',
                letterSpacing: '0.05em', fontWeight: 700,
              }}>MOCK</span>
            </div>
            <div style={{ fontSize: '0.7rem', color: '#6e40c9', marginBottom: '0.4rem' }}>
              (账号→pool 真实绑定 endpoint 尚未实现，此处为粗略预览)
            </div>
            <table className="obs-table" style={{ fontSize: '0.75rem' }}>
              <thead>
                <tr>
                  <th>account_id</th>
                  <th>name</th>
                  <th>model_allow_list</th>
                  <th>health</th>
                  <th>priority</th>
                </tr>
              </thead>
              <tbody>
                {accountsForPool(pool).map((acc) => (
                  <tr key={acc.id}>
                    <td style={{ fontFamily: 'monospace' }}>{acc.id}</td>
                    <td>{acc.name}</td>
                    <td style={{ color: '#8b949e' }}>
                      {(acc.model_allow_list ?? []).join(', ') || '（全部）'}
                    </td>
                    <td style={{ color: '#3fb950' }}>{acc.health_state}</td>
                    <td>{acc.priority}</td>
                  </tr>
                ))}
                {accountsForPool(pool).length === 0 && (
                  <tr><td colSpan={5} style={{ color: '#484f58', textAlign: 'center' }}>（无账号）</td></tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      ))}
    </div>
  );
}
