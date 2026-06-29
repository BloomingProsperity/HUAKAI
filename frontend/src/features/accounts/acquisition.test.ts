import { describe, expect, it } from 'vitest'
import {
  EMPTY_OAUTH_CLIENT,
  EMPTY_WIZARD_FORM,
  buildCallbackBody,
  buildImportBody,
  buildOAuthClientPayload,
  buildOAuthStartBody,
  canCancel,
  canDeliverCallback,
  helperPathForMethod,
  isFinalized,
  isTerminal,
  parseScopes,
  statusTone,
  validateCallback,
  validateImport,
  validateOAuthStart,
  type AcquisitionFlow,
  type WizardForm,
} from './acquisition'

// 测试夹具:在空表单上叠字段,避免每个用例重写全表单。
function form(overrides: Partial<WizardForm>): WizardForm {
  return {
    ...EMPTY_WIZARD_FORM,
    ...overrides,
    oauthClient: { ...EMPTY_WIZARD_FORM.oauthClient, ...(overrides.oauthClient ?? {}) },
  }
}

function flow(overrides: Partial<AcquisitionFlow>): AcquisitionFlow {
  return {
    id: 'flow-1',
    tenant_id: 1,
    provider_account_id: 9,
    vendor: 'anthropic',
    auth_mode: 'claude_ai_oauth',
    flow_kind: 'oauth',
    status: 'started',
    ...overrides,
  }
}

describe('helperPathForMethod', () => {
  it('非 oauth 方式映射到对应 helper 子路径', () => {
    expect(helperPathForMethod('paste')).toBe('paste')
    expect(helperPathForMethod('cli_import')).toBe('cli-import')
    expect(helperPathForMethod('csv_import')).toBe('csv-import')
    expect(helperPathForMethod('json_import')).toBe('json-import')
  })
  it('oauth 无 helper 路径', () => {
    // 判别核心:oauth 必须返回 null(组件据此走 startAcquisition 而非 importCredentials)。
    // 变异(返回 'oauth')→ 此断言 RED。
    expect(helperPathForMethod('oauth')).toBeNull()
  })
})

describe('parseScopes', () => {
  it('逗号/空白混合分隔,去空', () => {
    expect(parseScopes('a, b  c,,d')).toEqual(['a', 'b', 'c', 'd'])
    // 判别核心:全空/纯分隔符必须得空数组,不能塞入空串(否则后端收到空 scope)。
    expect(parseScopes('   , ,')).toEqual([])
  })
})

describe('buildOAuthStartBody', () => {
  it('必带 tenant_id/provider_account_id/vendor/auth_mode/flow_kind=oauth', () => {
    const body = buildOAuthStartBody(3, 9, form({ vendor: 'openai', authMode: 'chatgpt_oauth' }))
    expect(body).toMatchObject({
      tenant_id: 3,
      provider_account_id: 9,
      vendor: 'openai',
      auth_mode: 'chatgpt_oauth',
      flow_kind: 'oauth',
    })
    // 判别核心:tenant_id 必须取传入值。变异(漏写 tenant_id)→ undefined ≠ 3 → RED。
    expect(body.tenant_id).toBe(3)
    // flow_kind 必须恒为 oauth。变异(写成其他)→ RED。
    expect(body.flow_kind).toBe('oauth')
  })

  it('未勾选自定义 client 时不下发 oauth_client', () => {
    const body = buildOAuthStartBody(1, 9, form({ vendor: 'anthropic', authMode: 'claude_ai_oauth' }))
    // 判别核心:useCustomOAuthClient=false 时绝不带 oauth_client(否则把空 client_secret 等下发)。
    // 变异(无条件下发 oauth_client)→ 'oauth_client' in body 为 true → RED。
    expect('oauth_client' in body).toBe(false)
  })

  it('勾选自定义 client 时下发 oauth_client 且含 client_secret', () => {
    const body = buildOAuthStartBody(
      1,
      9,
      form({
        vendor: 'gemini',
        authMode: 'oauth',
        useCustomOAuthClient: true,
        oauthClient: { ...EMPTY_OAUTH_CLIENT, clientId: 'cid', clientSecret: 's3cr3t' },
      }),
    )
    expect(body.oauth_client).toMatchObject({ client_id: 'cid', client_secret: 's3cr3t' })
  })

  it('空 redirect_uri/scopes/reason 省略', () => {
    const body = buildOAuthStartBody(1, 9, form({ vendor: 'a', authMode: 'b' }))
    expect('redirect_uri' in body).toBe(false)
    expect('requested_scopes' in body).toBe(false)
    expect('reason' in body).toBe(false)
  })
})

describe('buildOAuthClientPayload', () => {
  it('只下发非空字段,client_secret 原样(只写)', () => {
    const out = buildOAuthClientPayload({
      clientId: 'cid',
      clientSecret: '  top secret ',
      authUrl: '',
      tokenUrl: '',
      redirectUri: '',
      scopes: 'x y',
      source: 'operator_config',
    })
    expect(out).toMatchObject({ client_id: 'cid', scopes: ['x', 'y'], source: 'operator_config' })
    // 判别核心:空 auth_url/token_url/redirect_uri 不得出现(否则后端收到空 URL)。
    expect('auth_url' in out).toBe(false)
    // client_secret 去首尾空白但保留内部内容(密钥里可能含空格?保守只 trim 端点)。
    expect(out.client_secret).toBe('top secret')
  })
  it('client_secret 为空时不下发', () => {
    const out = buildOAuthClientPayload({
      clientId: 'cid',
      clientSecret: '   ',
      authUrl: '',
      tokenUrl: '',
      redirectUri: '',
      scopes: '',
      source: '',
    })
    // 判别核心:纯空白 client_secret 必须省略(否则把空串当 secret 下发覆盖默认注入)。
    expect('client_secret' in out).toBe(false)
  })
})

describe('buildImportBody', () => {
  it('必带 content + finalize,vendor/auth_mode/reason 空则省略', () => {
    const body = buildImportBody(2, 9, form({ content: '{"api_key":"sk-x"}' }), true)
    expect(body).toMatchObject({ tenant_id: 2, provider_account_id: 9, content: '{"api_key":"sk-x"}', finalize: true })
    expect('vendor' in body).toBe(false)
    expect('reason' in body).toBe(false)
  })
  it('finalize 透传 false', () => {
    // 判别核心:finalize 必须取参数值,不能恒 true(恒 true 会在只建流时误落库)。
    // 变异(写死 finalize:true)→ RED。
    const body = buildImportBody(2, 9, form({ content: 'x' }), false)
    expect(body.finalize).toBe(false)
  })
  it('content 原样保留(不 trim,可能含有意义的换行/空白)', () => {
    const raw = '  line1\nline2  '
    const body = buildImportBody(2, 9, form({ content: raw }), true)
    expect(body.content).toBe(raw)
  })
})

describe('buildCallbackBody', () => {
  it('state/code 去首尾空白', () => {
    expect(buildCallbackBody('  st  ', ' code ')).toEqual({ state: 'st', code: 'code' })
  })
})

describe('validateOAuthStart', () => {
  it('tenant 非法拦下', () => {
    expect(validateOAuthStart(0, form({ vendor: 'a', authMode: 'b' })).ok).toBe(false)
    expect(validateOAuthStart(-1, form({ vendor: 'a', authMode: 'b' })).ok).toBe(false)
  })
  it('vendor/auth_mode 缺失拦下', () => {
    expect(validateOAuthStart(1, form({ vendor: '', authMode: 'b' })).ok).toBe(false)
    expect(validateOAuthStart(1, form({ vendor: 'a', authMode: '' })).ok).toBe(false)
  })
  it('齐全则通过', () => {
    expect(validateOAuthStart(1, form({ vendor: 'a', authMode: 'b' })).ok).toBe(true)
  })
})

describe('validateImport', () => {
  it('空 content 拦下', () => {
    // 判别核心:空/纯空白 content 必须 ok=false(否则空导入被发出)。
    // 变异(去掉 content 检查)→ ok 变 true → RED。
    expect(validateImport(1, form({ content: '   ' })).ok).toBe(false)
    expect(validateImport(1, form({ content: '' })).ok).toBe(false)
  })
  it('tenant 非法拦下', () => {
    expect(validateImport(0, form({ content: 'x' })).ok).toBe(false)
  })
  it('content 非空 + tenant 合法则通过', () => {
    expect(validateImport(1, form({ content: 'x' })).ok).toBe(true)
  })
})

describe('validateCallback', () => {
  it('state 或 code 缺失拦下', () => {
    expect(validateCallback('', 'code').ok).toBe(false)
    expect(validateCallback('st', '').ok).toBe(false)
  })
  it('齐全则通过', () => {
    expect(validateCallback('st', 'code').ok).toBe(true)
  })
})

describe('流状态机', () => {
  it('isTerminal 正确识别终态', () => {
    expect(isTerminal('finalized')).toBe(true)
    expect(isTerminal('cancelled')).toBe(true)
    expect(isTerminal('expired')).toBe(true)
    expect(isTerminal('failed')).toBe(true)
    // 判别核心:进行中态绝不算终态(否则按钮被错误禁用/启用)。
    expect(isTerminal('started')).toBe(false)
    expect(isTerminal('waiting_for_user')).toBe(false)
  })

  it('isFinalized 仅 finalized 为真', () => {
    expect(isFinalized('finalized')).toBe(true)
    expect(isFinalized('cancelled')).toBe(false)
  })

  it('canDeliverCallback:仅未终态的 oauth 流允许', () => {
    expect(canDeliverCallback(flow({ flow_kind: 'oauth', status: 'started' }))).toBe(true)
    // 判别核心:已 finalized 的 oauth 流不可再投递 callback(防 replay)。
    // 变异(只看 flow_kind 不看 status)→ 这里变 true → RED。
    expect(canDeliverCallback(flow({ flow_kind: 'oauth', status: 'finalized' }))).toBe(false)
    // 非 oauth 流永远不允许 callback。
    expect(canDeliverCallback(flow({ flow_kind: 'paste', status: 'started' }))).toBe(false)
  })

  it('canCancel:终态不可取消', () => {
    expect(canCancel('started')).toBe(true)
    expect(canCancel('finalized')).toBe(false)
    expect(canCancel('cancelled')).toBe(false)
  })

  it('statusTone:finalized→ok,failed→danger,started→warn', () => {
    expect(statusTone('finalized')).toBe('ok')
    expect(statusTone('failed')).toBe('danger')
    expect(statusTone('expired')).toBe('danger')
    expect(statusTone('started')).toBe('warn')
    expect(statusTone('cancelled')).toBe('muted')
  })
})
