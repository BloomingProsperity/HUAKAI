import { describe, expect, it } from 'vitest'
import {
  buildTenantQuery,
  formatIntList,
  formatStrList,
  mapTLSProfileRows,
  nextStatus,
  parseIntList,
  parseStrList,
  profileToForm,
  statusLabel,
  statusTone,
  toCreateRequest,
  validateForm,
  validateJa3,
} from './tlsfp'
import { EMPTY_FORM, type ProfileForm, type TLSFingerprintProfile } from './types'

describe('buildTenantQuery', () => {
  it('只带 tenant_id', () => {
    expect(buildTenantQuery(7)).toEqual({ tenant_id: 7 })
  })
})

describe('parseIntList', () => {
  it('逗号/空白混合分隔,保留顺序', () => {
    const r = parseIntList('4865, 4866  4867')
    expect(r.ok).toBe(true)
    if (r.ok) expect(r.value).toEqual([4865, 4866, 4867])
  })

  it('空白文本→空数组(不为 null)', () => {
    const r = parseIntList('   ')
    expect(r.ok).toBe(true)
    // 判别核心:空输入必须产出 [](后端接受 [],不是 null/不省略)。
    if (r.ok) expect(r.value).toEqual([])
  })

  it('非整数 token→error(避免发非法码点)', () => {
    // 判别核心:负号/小数/字母任一即拒;变异(放行非数字)→ ok=true→RED。
    expect(parseIntList('4865, abc').ok).toBe(false)
    expect(parseIntList('-1').ok).toBe(false)
    expect(parseIntList('4865.5').ok).toBe(false)
  })

  it('超出 0~65535 范围→error', () => {
    // 判别核心:65536 越界必须拒;边界 65535 放行。
    expect(parseIntList('65536').ok).toBe(false)
    expect(parseIntList('65535').ok).toBe(true)
  })
})

describe('parseStrList', () => {
  it('切分 trim 丢空', () => {
    expect(parseStrList('h2, http/1.1')).toEqual(['h2', 'http/1.1'])
    expect(parseStrList('   ')).toEqual([])
  })
})

describe('formatIntList / formatStrList', () => {
  it('拍成逗号+空格文本;空/undefined→空串', () => {
    expect(formatIntList([4865, 4866])).toBe('4865, 4866')
    expect(formatStrList(['h2', 'http/1.1'])).toBe('h2, http/1.1')
    // 判别核心:null/undefined 兜底为空串而非抛错。
    expect(formatIntList(undefined as unknown as number[])).toBe('')
  })
})

describe('validateJa3', () => {
  it('空串放行(返回 null)', () => {
    expect(validateJa3('')).toBeNull()
    expect(validateJa3('   ')).toBeNull()
  })

  it('非空须 32 位 hex', () => {
    expect(validateJa3('cd08e31494f9531f560d64c695473da9')).toBeNull()
    // 判别核心:31 位/含非 hex/超长一律拒;变异(无条件 null)→ 这些应 null→RED。
    expect(validateJa3('cd08e31494f9531f560d64c695473da')).not.toBeNull() // 31 位
    expect(validateJa3('zz08e31494f9531f560d64c695473da9')).not.toBeNull() // 含 z
    expect(validateJa3('cd08e31494f9531f560d64c695473da9a')).not.toBeNull() // 33 位
  })
})

describe('validateForm', () => {
  const filled: ProfileForm = {
    ...EMPTY_FORM,
    name: 'chrome-131',
    description: '  拟真 Chrome 131  ',
    cipherSuites: '4865, 4866',
    alpnProtocols: 'h2, http/1.1',
    expectedJa3Hash: 'cd08e31494f9531f560d64c695473da9',
  }

  it('名称空白→拒(镜像后端 name trim 非空)', () => {
    // 判别核心:仅空格的 name 必须拒(后端 service.go:63 strings.TrimSpace==""→ErrInvalidInput)。
    const r = validateForm({ ...filled, name: '   ' })
    expect(r.ok).toBe(false)
  })

  it('合法表单→产出内容体,name/description 已 trim,空描述归 null', () => {
    const r = validateForm(filled)
    expect(r.ok).toBe(true)
    if (r.ok) {
      expect(r.value.name).toBe('chrome-131')
      // 判别核心:description 必须 trim(变异:直接透传→带前后空格→RED)。
      expect(r.value.description).toBe('拟真 Chrome 131')
      expect(r.value.cipher_suites).toEqual([4865, 4866])
      expect(r.value.alpn_protocols).toEqual(['h2', 'http/1.1'])
      expect(r.value.expected_ja3_hash).toBe('cd08e31494f9531f560d64c695473da9')
    }
    // 空描述→null
    const r2 = validateForm({ ...filled, description: '   ' })
    if (r2.ok) expect(r2.value.description).toBeNull()
  })

  it('某个整数数组非法→带字段名拒', () => {
    // 判别核心:非法码点必须冒泡成 ok=false(变异:忽略 parse 错误→ok=true→RED)。
    const r = validateForm({ ...filled, supportedCurves: '29, oops' })
    expect(r.ok).toBe(false)
    if (!r.ok) expect(r.error).toContain('支持曲线')
  })

  it('非法 JA3→拒', () => {
    const r = validateForm({ ...filled, expectedJa3Hash: 'nothex' })
    expect(r.ok).toBe(false)
  })
})

describe('toCreateRequest', () => {
  it('把 tenant_id 拼到内容体前', () => {
    const content = {
      name: 'x',
      description: null,
      grease_enabled: true,
      cipher_suites: [],
      supported_curves: [],
      ec_point_formats: [],
      signature_algorithms: [],
      alpn_protocols: [],
      tls_supported_versions: [],
      key_share_groups: [],
      psk_modes: [],
      extensions_order: [],
      expected_ja3_hash: '',
    }
    const req = toCreateRequest(9, content)
    // 判别核心:tenant_id 必须出现且为入参值(后端 create 从 body 取 tenant_id)。
    expect(req.tenant_id).toBe(9)
    expect(req.name).toBe('x')
  })
})

describe('statusTone / statusLabel', () => {
  it('active→ok/启用,disabled→muted/停用,drift_detected→danger/指纹漂移', () => {
    expect(statusTone('active')).toBe('ok')
    expect(statusTone('disabled')).toBe('muted')
    // 判别核心:drift_detected 必须 danger(指纹暴露风险,不可与 disabled 同级 muted)。
    expect(statusTone('drift_detected')).toBe('danger')
    expect(statusTone('weird')).toBe('info')
    expect(statusLabel('active')).toBe('启用')
    expect(statusLabel('drift_detected')).toBe('指纹漂移')
    expect(statusLabel('weird')).toBe('weird')
  })
})

describe('nextStatus', () => {
  it('active→disabled,其余→active(含 drift_detected 覆盖式清除漂移)', () => {
    expect(nextStatus('active')).toBe('disabled')
    expect(nextStatus('disabled')).toBe('active')
    // 判别核心:drift_detected 的切换目标是 active(后端管理员覆盖清漂移路径),不是 disabled。
    expect(nextStatus('drift_detected')).toBe('active')
  })
})

describe('profileToForm', () => {
  it('DTO→表单,数组拍成文本,null 描述→空串', () => {
    const p: TLSFingerprintProfile = {
      id: 1,
      tenant_id: 1,
      name: 'safari',
      description: null,
      grease_enabled: false,
      cipher_suites: [4865, 4866],
      supported_curves: [29],
      ec_point_formats: [0],
      signature_algorithms: [1027],
      alpn_protocols: ['h2'],
      tls_supported_versions: [772],
      key_share_groups: [29],
      psk_modes: [1],
      extensions_order: [0, 23],
      expected_ja3_hash: 'abc',
      status: 'active',
    }
    const f = profileToForm(p)
    expect(f.name).toBe('safari')
    // 判别核心:null 描述回填为空串(变异:透传 null→input value 报错/显示 "null")。
    expect(f.description).toBe('')
    expect(f.cipherSuites).toBe('4865, 4866')
    expect(f.extensionsOrder).toBe('0, 23')
    expect(f.greaseEnabled).toBe(false)
  })
})

describe('mapTLSProfileRows', () => {
  it('完整映射指纹列表摘要(删 JA3/ALPN/状态任一映射→红)', () => {
    const profile: TLSFingerprintProfile = {
      id: 4, tenant_id: 1, name: 'chrome', description: '桌面', grease_enabled: true,
      cipher_suites: [1, 2], supported_curves: [], ec_point_formats: [], signature_algorithms: [],
      alpn_protocols: ['h2', 'http/1.1'], tls_supported_versions: [], key_share_groups: [], psk_modes: [],
      extensions_order: [], expected_ja3_hash: '1234567890abcdef1234567890abcdef', status: 'drift_detected',
      last_validated_at: null,
    }
    expect(mapTLSProfileRows([profile])[0]).toMatchObject({
      id: 4, name: 'chrome', description: '桌面', status: '指纹漂移', statusTone: 'danger',
      grease: '开', cipherSuiteCount: 2, alpn: 'h2, http/1.1', ja3: '12345678…cdef', lastValidatedAt: '—',
    })
  })
})
