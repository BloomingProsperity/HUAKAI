// HUAKAI 落地页 — 英雄区(hero)。标题 + 请求终端 + 调度卡片 + 信任统计数据。
import { Button, Badge, StatusDot, Icon } from '../landingKit';

// 终端代码块配色:各类语法 token 的颜色映射。
type TerminalColors = {
  mut: string; // 注释/弱化文本
  key: string; // JSON 键名
  str: string; // 字符串字面量
  method: string; // HTTP 方法 / 强调
  txt: string; // 正文文本
};

// CodeTerminal:展示一条 curl 流式请求与 SSE 响应的伪终端面板。
function CodeTerminal(): JSX.Element {
  const C: TerminalColors = { mut: '#94a3b8', key: '#7dd3fc', str: '#e7c08a', method: '#bfe14a', txt: '#cbd5e1' };
  return (
    <div className="hk-rise" style={{
      borderRadius: 12, border: '1px solid var(--border)', background: 'var(--neutral-900)',
      boxShadow: 'var(--shadow-card-hover)', overflow: 'hidden',
    }}>
      <div style={{
        display: 'flex', alignItems: 'center', gap: 10, padding: '12px 16px',
        borderBottom: '1px solid var(--border)', background: 'rgba(15,23,42,0.6)',
      }}>
        <Icon name="terminal" size={15} color="#bfe14a" />
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12.5, color: '#cbd5e1' }}>
          <span style={{ color: C.method }}>POST</span> /v1/chat/completions
        </span>
        <span style={{ marginLeft: 'auto' }}><Badge variant="outline" style={{ fontSize: 11, color: '#cbd5e1', borderColor: '#3a3f4a', background: 'transparent' }}>SSE</Badge></span>
      </div>
      <pre style={{
        margin: 0, padding: '18px 18px 20px', fontFamily: 'var(--font-mono)', fontSize: 12.5,
        lineHeight: 1.7, color: C.txt, whiteSpace: 'pre-wrap', wordBreak: 'break-word',
      }}>
<span style={{ color: C.mut }}>$ </span>curl -N https://gw.local/v1/chat/completions \{'\n'}
{'  '}-H <span style={{ color: C.str }}>"authorization: Bearer hk_live_…"</span> \{'\n'}
{'  '}-d <span style={{ color: C.str }}>'{'{'}<span style={{ color: C.key }}>"model"</span>:"claude-sonnet-4.5",<span style={{ color: C.key }}>"stream"</span>:true{'}'}'</span>{'\n\n'}
<span style={{ color: C.mut }}>← 200 · </span><span style={{ color: C.method }}>text/event-stream</span>{'\n'}
<span style={{ color: C.mut }}>data: </span>{'{'}<span style={{ color: C.key }}>"delta"</span>:{'{'}<span style={{ color: C.key }}>"content"</span>:<span style={{ color: C.str }}>"pong"</span>{'}}'}<span className="hk-cursor" style={{ background: '#bfe14a' }} />
      </pre>
    </div>
  );
}

// DispatchCard:悬浮在终端右下角的调度状态小卡片,展示某个账号池的实时健康。
function DispatchCard(): JSX.Element {
  return (
    <div className="hk-float" style={{
      position: 'absolute', right: -18, bottom: -22, width: 248,
      borderRadius: 10, border: '1px solid var(--border-strong)', background: 'var(--bg-surface)',
      boxShadow: '0 16px 48px rgba(13,42,72,0.18)', padding: 14,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 11, color: 'var(--text-subtle)', textTransform: 'uppercase', letterSpacing: '0.04em', fontWeight: 600 }}>
        <Icon name="git-merge" size={13} color="var(--primary-400)" /> dispatch
      </div>
      <div style={{ marginTop: 8, fontFamily: 'var(--font-mono)', fontSize: 13, fontWeight: 600, color: 'var(--text-strong)' }}>claude-pool-01</div>
      <div style={{ marginTop: 3, fontFamily: 'var(--font-mono)', fontSize: 11.5, color: 'var(--text-muted)' }}>anthropic / oauth</div>
      <div style={{ marginTop: 12, display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <span style={{ display: 'flex', alignItems: 'center', gap: 7, fontSize: 12, color: 'var(--success-fg)' }}>
          <StatusDot tone="online" pulse /> operational
        </span>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11.5, color: 'var(--text-muted)' }}>3/10 · 42ms</span>
      </div>
    </div>
  );
}

// 单条信任统计数据:v=数值,l=说明文案。
type HeroStat = {
  v: string;
  l: string;
};

// SiteHero:落地页首屏英雄区主组件。
export function SiteHero(): JSX.Element {
  const stats: HeroStat[] = [
    { v: '5', l: '已支持上游供应商' },
    { v: '2', l: '协议接口 OpenAI · Anthropic' },
    { v: '100%', l: '自托管 · 私钥不出本地' },
  ];
  return (
    <section id="top" className="hk-hero">
      <div className="hk-container hk-hero-grid">
        <div>
          <div className="hk-rise" style={{
            display: 'inline-flex', alignItems: 'center', gap: 9, padding: '5px 12px 5px 10px',
            borderRadius: 'var(--radius-full)', border: '1px solid var(--accent-soft-border)',
            background: 'var(--accent-soft-bg)', fontSize: 12.5, fontWeight: 500, color: 'var(--accent-soft-text)',
          }}>
            <span style={{ width: 7, height: 7, borderRadius: '50%', background: 'var(--primary-400)', boxShadow: 'var(--shadow-glow)' }} />
            自托管 · operator-side AI Gateway
          </div>
          <h1 className="hk-h1 hk-rise" style={{ margin: '22px 0 0', color: 'var(--text-strong)', fontWeight: 700, letterSpacing: '-0.025em', lineHeight: 1.08 }}>
            统一协议接口，<br />调度你自己的<br /><span style={{ color: 'var(--primary-400)' }}>LLM 账号池</span>
          </h1>
          <p className="hk-rise" style={{ margin: '24px 0 0', maxWidth: 520, fontSize: 17, lineHeight: 1.6, color: 'var(--text-muted)' }}>
            HUAKAI 挡在你自有的 Anthropic、OpenAI、Google Vertex、AWS Bedrock、OpenRouter 账号前面 —
            一套协议接口、健康感知调度、限流重试,以及按 token 与成本的用量核算。
          </p>
          <div className="hk-rise" style={{ marginTop: 30, display: 'flex', flexWrap: 'wrap', gap: 12 }}>
            <Button size="lg" onClick={() => { window.location.href = '/'; }}>
              进入控制台 <Icon name="arrow-right" />
            </Button>
            <a href="https://github.com/BloomingProsperity/HUAKAI" target="_blank" rel="noreferrer" style={{
              display: 'inline-flex', alignItems: 'center', gap: 8, height: '2.75rem', padding: '0 1.75rem',
              borderRadius: 'var(--radius-md)', border: '1px solid var(--border-strong)', background: 'var(--bg-surface)',
              color: 'var(--text-body)', fontSize: 16, fontWeight: 500, textDecoration: 'none',
            }} className="hk-outline-btn">
              <Icon name="github" /> 在 GitHub 查看
            </a>
          </div>
          <div className="hk-rise" style={{ marginTop: 44, display: 'flex', flexWrap: 'wrap', gap: 36, borderTop: '1px solid var(--border)', paddingTop: 24 }}>
            {stats.map((s) => (
              <div key={s.l}>
                <div style={{ fontSize: 28, fontWeight: 700, color: 'var(--text-strong)', fontFamily: 'var(--font-mono)', fontVariantNumeric: 'tabular-nums', letterSpacing: '-0.02em' }}>{s.v}</div>
                <div style={{ marginTop: 4, fontSize: 12.5, color: 'var(--text-subtle)' }}>{s.l}</div>
              </div>
            ))}
          </div>
        </div>
        <div style={{ position: 'relative' }} className="hk-rise">
          <CodeTerminal />
          <DispatchCard />
        </div>
      </div>
    </section>
  );
}
