// HUAKAI site — capability feature grid (6 cards).

const HK_TONES = {
  teal:    { fg: "var(--primary-300)", bg: "rgba(200,242,74,0.12)", bd: "rgba(200,242,74,0.30)" },
  emerald: { fg: "#34d399", bg: "rgba(16,185,129,0.12)", bd: "rgba(16,185,129,0.30)" },
  amber:   { fg: "#fbbf24", bg: "rgba(245,158,11,0.12)", bd: "rgba(245,158,11,0.30)" },
  blue:    { fg: "#60a5fa", bg: "rgba(59,130,246,0.12)", bd: "rgba(59,130,246,0.30)" },
  violet:  { fg: "#a78bfa", bg: "rgba(139,92,246,0.12)", bd: "rgba(139,92,246,0.30)" },
  slate:   { fg: "var(--neutral-300)", bg: "var(--bg-surface-2)", bd: "var(--border)" },
};

const HK_FEATURES = [
  { icon: "git-merge", tone: "teal", title: "统一协议接口", desc: "POST /v1/chat/completions 与 Anthropic messages 双协议，单一入口，原生支持 SSE 流式。", foot: { type: "code", text: "OpenAI · Anthropic" } },
  { icon: "heart-pulse", tone: "emerald", title: "健康感知调度", desc: "按 operational / cooling_down / degraded 状态与并发上限路由，自动绕开失败账号。", foot: { type: "code", text: "并发 3/10 · 自动避障" } },
  { icon: "refresh-cw", tone: "amber", title: "限流与重试", desc: "rate-limit 感知退避与跨账号重试；触发上限的账号进入 cooling_down 并自动恢复。", foot: { type: "code", text: "退避 · 跨账号重试" } },
  { icon: "bar-chart-3", tone: "blue", title: "用量与计费核算", desc: "按 usage.actual_cost 汇总 token、成本与缓存命中率，tabular 精确，可对账。", foot: { type: "code", text: "$38.42 · 1,284,500 tokens" } },
  { icon: "file-check-2", tone: "violet", title: "审计链路", desc: "请求 hop-chain 与 Merkle 校验接口已预留，可逐跳追溯。后端实现进行中。", foot: { type: "badge", variant: "default", text: "MOCK" } },
  { icon: "server", tone: "slate", title: "自托管", desc: "Go 后端 + Next.js 控制台，单机 docker compose 起。私钥不出本地，MIT 许可。", foot: { type: "code", text: "Go · Next.js · MIT" } },
];

function FeatureCard({ f }) {
  const { Badge } = window.HUAKAIDesignSystem_36f9be;
  const t = HK_TONES[f.tone];
  const [h, setH] = React.useState(false);
  return (
    <div
      onMouseEnter={() => setH(true)} onMouseLeave={() => setH(false)}
      style={{
        display: "flex", flexDirection: "column", padding: 22,
        borderRadius: "var(--radius)", border: `1px solid ${h ? "var(--border-strong)" : "var(--border)"}`,
        background: "var(--bg-surface)", boxShadow: h ? "var(--shadow-card-hover)" : "var(--shadow-card)",
        transform: h ? "translateY(-2px)" : "none",
        transition: "transform var(--dur-base) var(--ease-out), border-color var(--dur-base), box-shadow var(--dur-base)",
      }}>
      <span style={{
        width: 40, height: 40, display: "flex", alignItems: "center", justifyContent: "center",
        borderRadius: 10, background: t.bg, border: `1px solid ${t.bd}`, color: t.fg,
      }}>
        <Icon name={f.icon} size={19} color={t.fg} />
      </span>
      <h3 style={{ margin: "16px 0 0", fontSize: 16.5, fontWeight: 600, color: "var(--text-strong)" }}>{f.title}</h3>
      <p style={{ margin: "8px 0 0", fontSize: 14, lineHeight: 1.6, color: "var(--text-muted)", flex: 1 }}>{f.desc}</p>
      <div style={{ marginTop: 16 }}>
        {f.foot.type === "badge"
          ? <Badge variant={f.foot.variant} style={{ background: HK_TONES.violet.bg, color: HK_TONES.violet.fg, borderColor: HK_TONES.violet.bd }}>{f.foot.text}</Badge>
          : <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--text-subtle)" }}>{f.foot.text}</span>}
      </div>
    </div>
  );
}

function SiteFeatures() {
  return (
    <section id="features" className="hk-section">
      <div className="hk-container">
        <div style={{ maxWidth: 720 }}>
          <div className="hk-eyebrow">能力 · capabilities</div>
          <h2 className="hk-h2">网关该做的，它都在一处做</h2>
          <p className="hk-lead">从协议适配到调度、限流、计费与审计 — 一个进程挡在你的账号池前面，把运维收口到一块面板。</p>
        </div>
        <div className="hk-feature-grid" style={{ marginTop: 34 }}>
          {HK_FEATURES.map((f) => <FeatureCard key={f.title} f={f} />)}
        </div>
      </div>
    </section>
  );
}
window.SiteFeatures = SiteFeatures;
