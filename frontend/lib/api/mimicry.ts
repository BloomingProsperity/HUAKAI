// Mimicry profile CRUD — 后端尚无此端点，全部 MOCK
// 接口签名预留形态：GET/POST /admin/v1/mimicry-profiles
// 字段对应 backend/internal/gateway/mimicry_compose.go MimicryPlan struct

import type { MimicryProfile } from './types';

// ⚠ MOCK — 后端暂无此端点
const MOCK_PROFILES: MimicryProfile[] = [
  {
    id: 'default',
    name: 'Default（关闭）',
    enabled: false,
    system_rewrite: false,
    strip_system_cache_control: false,
    cache_breakpoints: false,
    use_ttl_ordering_for_step3: false,
    tool_names: false,
    metadata_user_id: '',
    apply_tools_tail_cache_bp: false,
    tools_tail_ttl: 'ephemeral',
  },
  {
    id: 'claude-code-full',
    name: 'Claude Code 全伪装',
    enabled: true,
    system_rewrite: true,
    strip_system_cache_control: true,
    cache_breakpoints: true,
    use_ttl_ordering_for_step3: true,
    tool_names: true,
    metadata_user_id: 'user_mock_001',
    apply_tools_tail_cache_bp: true,
    tools_tail_ttl: 'ephemeral',
  },
];

// listMimicryProfiles — ⚠ MOCK
// 真实形态：GET /admin/v1/mimicry-profiles
export async function listMimicryProfiles(): Promise<MimicryProfile[]> {
  await new Promise((r) => setTimeout(r, 80));
  return MOCK_PROFILES.map((p) => ({ ...p }));
}

// getMimicryProfile — ⚠ MOCK
export async function getMimicryProfile(id: string): Promise<MimicryProfile> {
  await new Promise((r) => setTimeout(r, 60));
  const p = MOCK_PROFILES.find((p) => p.id === id);
  if (!p) throw new Error(`Profile ${id} not found`);
  return { ...p };
}

// updateMimicryProfile — ⚠ MOCK
// 真实形态：PATCH /admin/v1/mimicry-profiles/{id}
export async function updateMimicryProfile(
  id: string,
  patch: Partial<MimicryProfile>,
): Promise<MimicryProfile> {
  await new Promise((r) => setTimeout(r, 150));
  const idx = MOCK_PROFILES.findIndex((p) => p.id === id);
  if (idx === -1) throw new Error(`Profile ${id} not found`);
  Object.assign(MOCK_PROFILES[idx], patch);
  return { ...MOCK_PROFILES[idx] };
}
