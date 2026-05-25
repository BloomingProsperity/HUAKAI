// HUAKAI 反代联调控制台首页
// 6 panel 入口卡片 + 快速状态说明

// 面板定义
const PANELS = [
  {
    href: '/accounts',
    num: '1',
    title: '测试账号入库',
    desc: 'Provider Account CRUD — list / create / edit / clear-rate-limit',
    endpoints: ['GET /admin/v1/provider-accounts', 'POST /admin/v1/provider-accounts', 'PATCH /admin/v1/provider-accounts/{id}', 'POST /admin/v1/provider-accounts/{id}/clear-rate-limit'],
    mock: false,
  },
  {
    href: '/bindings',
    num: '2',
    title: '测试模型绑定',
    desc: 'Pool Group CRUD + account binding preview',
    endpoints: ['GET /admin/v1/pools', 'POST /admin/v1/pools', 'PATCH /admin/v1/pools/{id}'],
    mock: false,
  },
  {
    href: '/chat',
    num: '3',
    title: '测试 Chat 调试器',
    desc: 'OpenAI chat/completions + Anthropic messages，双 tab，支持 SSE 流式',
    endpoints: ['POST /v1/chat/completions', 'POST /v1/messages'],
    mock: false,
  },
  {
    href: '/selection',
    num: '4',
    title: '账号选中 / Slot / Claim / Usage',
    desc: '实时观测面板 — debug/vars 轮询 + billing claims + usage records',
    endpoints: ['GET /debug/vars', 'GET /admin/v1/billing/claims', 'GET /admin/v1/usage'],
    mock: false,
  },
  {
    href: '/renew',
    num: '5',
    title: 'Renew Status',
    desc: 'Auth credential renew status list',
    endpoints: ['GET /admin/v1/credentials/renew-status'],
    mock: false,
  },
  {
    href: '/mimicry',
    num: '6',
    title: 'Proxy / Mimicry Profile',
    desc: 'R7 强伪装 6-step pipeline 配置；对应 MimicryPlan struct',
    endpoints: ['GET /admin/v1/mimicry-profiles（预留）', 'PATCH /admin/v1/mimicry-profiles/{id}（预留）'],
    mock: true,
  },
] as const;

export default function HomePage() {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1.5rem' }}>
      {/* 标题 */}
      <div>
        <h1 style={{ fontSize: '1.1rem', color: '#e6edf3', fontWeight: 700, marginBottom: '0.25rem' }}>
          HUAKAI 反代联调控制台
        </h1>
        <p style={{ fontSize: '0.8rem', color: '#8b949e' }}>
          调试网关 → 选择下方面板。后端接口按 <code style={{ color: '#58a6ff' }}>docs/openapi/openapi.yaml</code> 设计，
          标记 <span style={{ background: '#6e40c9', color: '#fff', fontSize: '0.65rem', padding: '0.1rem 0.4rem', borderRadius: 3 }}>MOCK</span> 的面板后端尚未实现，接口签名已预留。
        </p>
      </div>

      {/* 面板卡片网格 */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: '1rem' }}>
        {PANELS.map((panel) => (
          <a
            key={panel.href}
            href={panel.href}
            className="panel-card-link"
            style={{ textDecoration: 'none', display: 'block' }}
          >
            <div className="panel-card" style={{
              background: '#161b22',
              border: '1px solid #30363d',
              borderRadius: 8,
              padding: '1rem',
              transition: 'border-color 0.15s',
              cursor: 'pointer',
              height: '100%',
            }}>
              {/* 面板编号 + MOCK badge */}
              <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', marginBottom: '0.4rem' }}>
                <span style={{
                  fontSize: '0.65rem', background: '#21262d', color: '#8b949e',
                  padding: '0.1rem 0.4rem', borderRadius: 3, fontWeight: 700,
                }}>
                  #{panel.num}
                </span>
                {panel.mock && (
                  <span style={{
                    fontSize: '0.65rem', background: '#6e40c9', color: '#fff',
                    padding: '0.1rem 0.4rem', borderRadius: 3, fontWeight: 700,
                  }}>
                    MOCK
                  </span>
                )}
              </div>

              {/* 标题 */}
              <div style={{ color: '#e6edf3', fontWeight: 600, fontSize: '0.9rem', marginBottom: '0.3rem' }}>
                {panel.title}
              </div>

              {/* 描述 */}
              <div style={{ color: '#8b949e', fontSize: '0.75rem', marginBottom: '0.6rem' }}>
                {panel.desc}
              </div>

              {/* Endpoints */}
              <div style={{ display: 'flex', flexDirection: 'column', gap: '0.15rem' }}>
                {panel.endpoints.map((ep) => (
                  <code key={ep} style={{ fontSize: '0.65rem', color: '#484f58' }}>{ep}</code>
                ))}
              </div>
            </div>
          </a>
        ))}
      </div>

      {/* Observability 快捷入口 */}
      <div style={{ borderTop: '1px solid #21262d', paddingTop: '1rem' }}>
        <a href="/observability" style={{ color: '#58a6ff', fontSize: '0.8rem' }}>
          → Observability（Cache Metrics 实时轮询）
        </a>
      </div>

      {/* Token 设置提示 — admin 与 customer 是不同 namespace 不能混用 */}
      <div style={{ fontSize: '0.75rem', color: '#484f58', background: '#161b22', border: '1px solid #21262d', borderRadius: 4, padding: '0.5rem 0.75rem' }}>
        <div style={{ marginBottom: '0.3rem' }}>
          调 admin API (/admin/v1/*) 需 admin token (hk_admin_ 开头)：
        </div>
        <code style={{ display: 'block', color: '#8b949e', marginBottom: '0.5rem' }}>
          localStorage.setItem(&apos;huakai_admin_token&apos;, &apos;hk_admin_YOUR_24CHARS&apos;)
        </code>
        <div style={{ marginBottom: '0.3rem' }}>
          调聊天接口 (/v1/chat/completions, /v1/messages) 需客户 API key (hk_live_ / hk_test_ 开头, admin token 会被拒绝)：
        </div>
        <code style={{ display: 'block', color: '#8b949e' }}>
          localStorage.setItem(&apos;huakai_api_key&apos;, &apos;hk_live_YOUR_24CHARS&apos;)
        </code>
      </div>
    </div>
  );
}
