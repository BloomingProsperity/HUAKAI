'use client';

import { useState, useEffect, useCallback } from 'react';
import { listMimicryProfiles, updateMimicryProfile } from '../../lib/api/mimicry';
import type { MimicryProfile } from '../../lib/api/types';

// ⚠ MOCK — 后端尚无 /admin/v1/mimicry-profiles 端点
// 字段对应 backend/internal/gateway/mimicry_compose.go MimicryPlan struct

// 布尔开关字段列表（对应 MimicryPlan 各步骤）
const BOOL_FIELDS: { key: keyof MimicryProfile; label: string; desc: string }[] = [
  { key: 'enabled', label: 'Enabled（feature flag）', desc: 'false = 整个管线 no-op（安全默认）' },
  { key: 'system_rewrite', label: 'Step 1: system_rewrite', desc: 'R7.3 系统提示词重写' },
  { key: 'strip_system_cache_control', label: 'Step 2: strip_system_cache_control', desc: '清除 system 各块上的 cache_control，为 step 3 让路' },
  { key: 'cache_breakpoints', label: 'Step 3: cache_breakpoints', desc: 'R7.2 在指定位置注入 cache_control breakpoints' },
  { key: 'use_ttl_ordering_for_step3', label: 'Step 3+: use_ttl_ordering', desc: 'TTL 排序版本（长 TTL 在前）' },
  { key: 'tool_names', label: 'Step 4: tool_names obfuscation', desc: 'R7.4 工具名混淆' },
  { key: 'apply_tools_tail_cache_bp', label: 'Step 6: tools_tail_cache_bp', desc: 'tools[-1] 上挂 ephemeral cache_control' },
];

export default function MimicryPage() {
  const [profiles, setProfiles] = useState<MimicryProfile[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  // 正在编辑的 profile id → 编辑中的 patch
  const [editing, setEditing] = useState<Record<string, Partial<MimicryProfile>>>({});
  const [saving, setSaving] = useState<Record<string, boolean>>({});
  const [saveErrors, setSaveErrors] = useState<Record<string, string>>({});

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const res = await listMimicryProfiles();
      setProfiles(res);
    } catch (err: unknown) {
      setError(String(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  function startEdit(p: MimicryProfile) {
    setEditing((prev) => ({ ...prev, [p.id]: { ...p } }));
    setSaveErrors((prev) => ({ ...prev, [p.id]: '' }));
  }

  function cancelEdit(id: string) {
    setEditing((prev) => { const next = { ...prev }; delete next[id]; return next; });
  }

  function patchEdit(id: string, key: keyof MimicryProfile, value: unknown) {
    setEditing((prev) => ({
      ...prev,
      [id]: { ...prev[id], [key]: value },
    }));
  }

  async function handleSave(id: string) {
    const patch = editing[id];
    if (!patch) return;
    setSaving((prev) => ({ ...prev, [id]: true }));
    setSaveErrors((prev) => ({ ...prev, [id]: '' }));
    try {
      const updated = await updateMimicryProfile(id, patch);
      setProfiles((ps) => ps.map((p) => p.id === id ? updated : p));
      cancelEdit(id);
    } catch (err: unknown) {
      setSaveErrors((prev) => ({ ...prev, [id]: String(err) }));
    } finally {
      setSaving((prev) => ({ ...prev, [id]: false }));
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      {/* 标题行 */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '1rem' }}>
        <h1 style={{ fontSize: '1rem', color: '#e6edf3', fontWeight: 600 }}>
          面板 6 — Proxy / Mimicry Profile 配置
        </h1>
        <span style={{
          fontSize: '0.65rem', background: '#6e40c9', color: '#fff',
          padding: '0.15rem 0.5rem', borderRadius: 3, fontWeight: 700, letterSpacing: '0.05em',
        }}>
          MOCK
        </span>
        <button onClick={load} disabled={loading} style={{ marginLeft: 'auto', background: '#21262d' }}>
          {loading ? '刷新中…' : '刷新'}
        </button>
      </div>

      {/* Mock 说明 */}
      <div style={{ fontSize: '0.75rem', color: '#8b949e', background: '#161b22', border: '1px solid #30363d', borderRadius: 4, padding: '0.5rem 0.75rem' }}>
        后端尚未实现 <code style={{ color: '#58a6ff' }}>GET/PATCH /admin/v1/mimicry-profiles</code>。
        字段对应 <code style={{ color: '#58a6ff' }}>backend/internal/gateway/mimicry_compose.go MimicryPlan</code>。
        接口签名已预留——后端就绪后替换 <code style={{ color: '#58a6ff' }}>lib/api/mimicry.ts</code> 中的 mock 实现。
      </div>

      {error && <div className="error-msg">{error}</div>}

      {/* Profile 卡片列表 */}
      {profiles.length === 0 && !loading && (
        <div style={{ color: '#484f58', fontSize: '0.875rem' }}>（无 profile）</div>
      )}

      {profiles.map((p) => {
        const isEditing = !!editing[p.id];
        const patch = editing[p.id] ?? p;
        const isSaving = saving[p.id] ?? false;
        const saveErr = saveErrors[p.id] ?? '';

        return (
          <div key={p.id} style={{ background: '#161b22', border: '1px solid #30363d', borderRadius: 6, overflow: 'hidden' }}>
            {/* Profile header */}
            <div style={{ padding: '0.6rem 0.75rem', borderBottom: '1px solid #30363d', display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
              <span style={{ fontFamily: 'monospace', color: '#8b949e', fontSize: '0.75rem' }}>{p.id}</span>
              <span style={{ color: '#e6edf3', fontWeight: 600 }}>{p.name}</span>
              <span style={{
                fontSize: '0.7rem', fontWeight: 700,
                color: p.enabled ? '#3fb950' : '#484f58',
                border: `1px solid ${p.enabled ? '#3fb950' : '#484f58'}`,
                borderRadius: 3, padding: '0.05rem 0.4rem',
              }}>
                {p.enabled ? 'ENABLED' : 'DISABLED'}
              </span>
              <div style={{ marginLeft: 'auto', display: 'flex', gap: '0.4rem' }}>
                {!isEditing ? (
                  <button onClick={() => startEdit(p)}
                    style={{ background: '#21262d', fontSize: '0.75rem', padding: '0.25rem 0.6rem' }}>
                    编辑
                  </button>
                ) : (
                  <>
                    <button onClick={() => handleSave(p.id)} disabled={isSaving}
                      style={{ fontSize: '0.75rem', padding: '0.25rem 0.6rem' }}>
                      {isSaving ? '保存中…' : '保存'}
                    </button>
                    <button onClick={() => cancelEdit(p.id)}
                      style={{ background: '#21262d', fontSize: '0.75rem', padding: '0.25rem 0.6rem' }}>
                      取消
                    </button>
                  </>
                )}
              </div>
            </div>

            {/* 字段配置 */}
            <div style={{ padding: '0.75rem', display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
              {/* 布尔开关 */}
              {BOOL_FIELDS.map(({ key, label, desc }) => {
                const val = !!(patch[key as keyof typeof patch]);
                return (
                  <div key={key} style={{ display: 'flex', alignItems: 'flex-start', gap: '0.75rem' }}>
                    <div style={{ width: 36, flexShrink: 0, paddingTop: 2 }}>
                      {isEditing ? (
                        <input type="checkbox" checked={val}
                          onChange={(e) => patchEdit(p.id, key, e.target.checked)}
                          style={{ width: 'auto', cursor: 'pointer' }} />
                      ) : (
                        <span style={{ color: val ? '#3fb950' : '#484f58', fontWeight: 700 }}>
                          {val ? '✓' : '✗'}
                        </span>
                      )}
                    </div>
                    <div>
                      <div style={{ fontSize: '0.8rem', color: '#c9d1d9' }}>{label}</div>
                      <div style={{ fontSize: '0.7rem', color: '#484f58' }}>{desc}</div>
                    </div>
                  </div>
                );
              })}

              {/* 文本字段 */}
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0.5rem', marginTop: '0.25rem' }}>
                <div>
                  <label>metadata_user_id</label>
                  {isEditing ? (
                    <input type="text"
                      value={(patch.metadata_user_id as string) ?? ''}
                      onChange={(e) => patchEdit(p.id, 'metadata_user_id', e.target.value)}
                      placeholder="user_xxxx" />
                  ) : (
                    <div style={{ fontSize: '0.8rem', color: '#c9d1d9', fontFamily: 'monospace', padding: '0.3rem 0' }}>
                      {p.metadata_user_id || '（空）'}
                    </div>
                  )}
                </div>
                <div>
                  <label>tools_tail_ttl</label>
                  {isEditing ? (
                    <select value={(patch.tools_tail_ttl as string) ?? 'ephemeral'}
                      onChange={(e) => patchEdit(p.id, 'tools_tail_ttl', e.target.value)}>
                      <option value="ephemeral">ephemeral</option>
                      <option value="5m">5m</option>
                      <option value="1h">1h</option>
                    </select>
                  ) : (
                    <div style={{ fontSize: '0.8rem', color: '#c9d1d9', fontFamily: 'monospace', padding: '0.3rem 0' }}>
                      {p.tools_tail_ttl}
                    </div>
                  )}
                </div>
              </div>
            </div>

            {saveErr && <div className="error-msg" style={{ padding: '0 0.75rem 0.5rem' }}>{saveErr}</div>}
          </div>
        );
      })}
    </div>
  );
}
