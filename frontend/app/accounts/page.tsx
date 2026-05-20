'use client';

import React, { useState, useEffect, useCallback } from 'react';
import {
  listProviderAccounts,
  createProviderAccount,
  updateProviderAccount,
  clearProviderAccountRateLimit,
} from '../../lib/api/providerAccounts';
import { ApiError } from '../../lib/api/client';
import type { ProviderAccount, ProviderAccountCreate, ProviderAccountUpdate } from '../../lib/api/types';

// 健康状态颜色映射
const HEALTH_COLOR: Record<string, string> = {
  operational: '#3fb950',
  degraded: '#d29922',
  failed: '#f85149',
  cooling_down: '#58a6ff',
  error: '#f85149',
};

// 空的新建表单初始值
const EMPTY_CREATE: ProviderAccountCreate = {
  provider_id: 1,
  channel_id: 1,
  name: '',
  account_type: 'api_key',
  credentials: {},
  cap_concurrency: 4,
  priority: 100,
  model_allow_list: [],
  capability_flags: [],
};

export default function AccountsPage() {
  const [accounts, setAccounts] = useState<ProviderAccount[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  // 新建面板
  const [showCreate, setShowCreate] = useState(false);
  const [createForm, setCreateForm] = useState<ProviderAccountCreate>({ ...EMPTY_CREATE });
  // credentials 以 JSON 字符串编辑
  const [credJson, setCredJson] = useState('{}');
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState('');

  // 编辑面板（行内）
  const [editId, setEditId] = useState<number | null>(null);
  const [editForm, setEditForm] = useState<ProviderAccountUpdate>({});
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState('');

  // 加载账号列表
  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const res = await listProviderAccounts({ limit: 50 });
      setAccounts(res.items);
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

  // 新建账号提交
  async function handleCreate() {
    setCreating(true);
    setCreateError('');
    try {
      const creds = JSON.parse(credJson) as Record<string, unknown>;
      await createProviderAccount({ ...createForm, credentials: creds });
      setShowCreate(false);
      setCreateForm({ ...EMPTY_CREATE });
      setCredJson('{}');
      await load();
    } catch (err: unknown) {
      setCreateError(String(err));
    } finally {
      setCreating(false);
    }
  }

  // 编辑保存
  async function handleSave(id: number) {
    setSaving(true);
    setSaveError('');
    try {
      await updateProviderAccount(id, editForm);
      setEditId(null);
      await load();
    } catch (err: unknown) {
      setSaveError(String(err));
    } finally {
      setSaving(false);
    }
  }

  // 清除 rate limit
  async function handleClearRL(id: number) {
    try {
      await clearProviderAccountRateLimit(id);
      await load();
    } catch (err: unknown) {
      setError(String(err));
    }
  }

  function startEdit(account: ProviderAccount) {
    setEditId(account.id);
    setEditForm({
      enabled: account.enabled,
      priority: account.priority,
      cap_concurrency: account.cap_concurrency,
      pool_mode: account.pool_mode,
      temp_unschedulable_enabled: account.temp_unschedulable_enabled,
    });
    setSaveError('');
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      {/* 标题行 */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '1rem' }}>
        <h1 style={{ fontSize: '1rem', color: '#e6edf3', fontWeight: 600 }}>
          面板 1 — Provider Accounts
        </h1>
        <button onClick={load} disabled={loading} style={{ marginLeft: 'auto', background: '#21262d' }}>
          {loading ? '刷新中…' : '刷新'}
        </button>
        <button onClick={() => { setShowCreate(!showCreate); setCreateError(''); }}>
          {showCreate ? '取消' : '+ 新建账号'}
        </button>
      </div>

      {error && (
        error === '__501__'
          ? <div className="not-impl-msg">后端尚未实现此端点 (501) — 等待 backend wire</div>
          : <div className="error-msg">{error}</div>
      )}

      {/* 新建表单 */}
      {showCreate && (
        <div style={{ background: '#161b22', border: '1px solid #30363d', borderRadius: 6, padding: '1rem' }}>
          <div className="section-title">新建 Provider Account</div>
          <div className="field-group" style={{ marginTop: '0.75rem' }}>
            <div>
              <label>name</label>
              <input
                type="text"
                value={createForm.name}
                onChange={(e) => setCreateForm((f) => ({ ...f, name: e.target.value }))}
                placeholder="my-account"
              />
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: '0.5rem' }}>
              <div>
                <label>provider_id</label>
                <input type="text" value={createForm.provider_id}
                  onChange={(e) => setCreateForm((f) => ({ ...f, provider_id: Number(e.target.value) }))} />
              </div>
              <div>
                <label>channel_id</label>
                <input type="text" value={createForm.channel_id}
                  onChange={(e) => setCreateForm((f) => ({ ...f, channel_id: Number(e.target.value) }))} />
              </div>
              <div>
                <label>account_type</label>
                <select value={createForm.account_type}
                  onChange={(e) => setCreateForm((f) => ({ ...f, account_type: e.target.value as ProviderAccountCreate['account_type'] }))}>
                  <option value="oauth">oauth</option>
                  <option value="api_key">api_key</option>
                  <option value="service_account">service_account</option>
                  <option value="upstream_static">upstream_static</option>
                </select>
              </div>
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0.5rem' }}>
              <div>
                <label>cap_concurrency</label>
                <input type="text" value={createForm.cap_concurrency}
                  onChange={(e) => setCreateForm((f) => ({ ...f, cap_concurrency: Number(e.target.value) }))} />
              </div>
              <div>
                <label>priority</label>
                <input type="text" value={createForm.priority}
                  onChange={(e) => setCreateForm((f) => ({ ...f, priority: Number(e.target.value) }))} />
              </div>
            </div>
            <div>
              <label>model_allow_list（逗号分隔）</label>
              <input type="text"
                value={(createForm.model_allow_list ?? []).join(',')}
                onChange={(e) => setCreateForm((f) => ({ ...f, model_allow_list: e.target.value.split(',').map((s) => s.trim()).filter(Boolean) }))}
                placeholder="claude-3-5-sonnet-20241022,..."
              />
            </div>
            <div>
              <label>credentials（JSON，WRITE-ONLY）</label>
              <textarea rows={3} value={credJson} onChange={(e) => setCredJson(e.target.value)}
                placeholder='{"api_key":"sk-..."}'
                style={{ fontFamily: 'monospace', fontSize: '0.8rem' }} />
            </div>
          </div>
          {createError && <div className="error-msg">{createError}</div>}
          <button onClick={handleCreate} disabled={creating || !createForm.name}>
            {creating ? '创建中…' : '创建'}
          </button>
        </div>
      )}

      {/* 账号表 */}
      <table className="obs-table">
        <thead>
          <tr>
            <th>ID</th>
            <th>name</th>
            <th>type</th>
            <th>health</th>
            <th>credential</th>
            <th>concurrency</th>
            <th>in_flight</th>
            <th>priority</th>
            <th>enabled</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          {accounts.length === 0 && (
            <tr><td colSpan={10} style={{ textAlign: 'center', color: '#484f58' }}>
              {loading ? '加载中…' : '（无账号）'}
            </td></tr>
          )}
          {accounts.map((account) => (
            // 用具名 Fragment 承载 key，避免裸 <> 无法附 key 的 React 警告
            <React.Fragment key={account.id}>
              <tr>
                <td style={{ fontFamily: 'monospace' }}>{account.id}</td>
                <td>{account.name}</td>
                <td style={{ color: '#8b949e' }}>{account.account_type}</td>
                <td>
                  <span style={{ color: HEALTH_COLOR[account.health_state] ?? '#c9d1d9' }}>
                    ● {account.health_state}
                  </span>
                </td>
                <td style={{ color: '#8b949e' }}>{account.credential_state}</td>
                <td>{account.in_flight_count} / {account.cap_concurrency}</td>
                <td>{account.in_flight_count}</td>
                <td>{account.priority}</td>
                <td style={{ color: account.enabled ? '#3fb950' : '#f85149' }}>
                  {account.enabled ? 'YES' : 'NO'}
                </td>
                <td>
                  <div style={{ display: 'flex', gap: '0.4rem', flexWrap: 'wrap' }}>
                    <button onClick={() => startEdit(account)} style={{ background: '#21262d', fontSize: '0.75rem', padding: '0.25rem 0.6rem' }}>
                      编辑
                    </button>
                    {account.rate_limit_reset_at && (
                      <button onClick={() => handleClearRL(account.id)}
                        style={{ background: '#6e40c9', fontSize: '0.75rem', padding: '0.25rem 0.6rem' }}>
                        清 RL
                      </button>
                    )}
                  </div>
                </td>
              </tr>
              {/* 行内编辑面板 */}
              {editId === account.id && (
                <tr>
                  <td colSpan={10}>
                    <div style={{ background: '#0d1117', border: '1px solid #30363d', borderRadius: 4, padding: '0.75rem', display: 'flex', gap: '1rem', flexWrap: 'wrap', alignItems: 'flex-end' }}>
                      <div>
                        <label>enabled</label>
                        <select value={editForm.enabled ? 'true' : 'false'}
                          onChange={(e) => setEditForm((f) => ({ ...f, enabled: e.target.value === 'true' }))}>
                          <option value="true">true</option>
                          <option value="false">false</option>
                        </select>
                      </div>
                      <div>
                        <label>priority</label>
                        <input type="text" value={editForm.priority ?? ''}
                          onChange={(e) => setEditForm((f) => ({ ...f, priority: Number(e.target.value) }))}
                          style={{ width: 80 }} />
                      </div>
                      <div>
                        <label>cap_concurrency</label>
                        <input type="text" value={editForm.cap_concurrency ?? ''}
                          onChange={(e) => setEditForm((f) => ({ ...f, cap_concurrency: Number(e.target.value) }))}
                          style={{ width: 80 }} />
                      </div>
                      <div>
                        <label>pool_mode</label>
                        <select value={editForm.pool_mode ? 'true' : 'false'}
                          onChange={(e) => setEditForm((f) => ({ ...f, pool_mode: e.target.value === 'true' }))}>
                          <option value="false">false</option>
                          <option value="true">true</option>
                        </select>
                      </div>
                      <div style={{ display: 'flex', gap: '0.5rem' }}>
                        <button onClick={() => handleSave(account.id)} disabled={saving}>{saving ? '保存中…' : '保存'}</button>
                        <button onClick={() => setEditId(null)} style={{ background: '#21262d' }}>取消</button>
                      </div>
                    </div>
                    {saveError && <div className="error-msg" style={{ padding: '0.25rem 0.75rem' }}>{saveError}</div>}
                  </td>
                </tr>
              )}
            </React.Fragment>
          ))}
        </tbody>
      </table>

      {/* rate limit 状态摘要 */}
      {accounts.some((a) => a.rate_limit_reset_at) && (
        <div style={{ fontSize: '0.75rem', color: '#d29922' }}>
          ⚠ 有账号处于 rate-limit 冷却中，点击"清 RL"可手动解除
        </div>
      )}
    </div>
  );
}
