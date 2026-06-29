// HUAKAI site — supported upstream providers strip.

const HK_PROVIDERS = [
  { mark: "A", name: "Anthropic", channel: "anthropic / oauth", models: ["claude-sonnet-4.5", "claude-opus-4.1"] },
  { mark: "O", name: "OpenAI", channel: "openai / api-key", models: ["gpt-5", "gpt-4.1-mini"] },
  { mark: "G", name: "Google Vertex", channel: "google / vertex", models: ["gemini-2.5-pro"] },
  { mark: "B", name: "AWS Bedrock", channel: "aws / bedrock", models: ["claude-sonnet-4.5"] },
  { mark: "R", name: "OpenRouter", channel: "openrouter / key", models: ["多模型聚合"] },
];

function ProviderCard({ p }) {
  const [h, setH] = React.useState(false);
  return (
    <div
      onMouseEnter={() => setH(true)} onMouseLeave={() => setH(false)}
      style={{
        display: "flex", flexDirection: "column", gap: 12, padding: 18,
        borderRadius: "var(--radius)", border: `1px solid ${h ? "var(--accent-soft-border)" : "var(--border)"}`,
        background: "var(--bg-surface)", boxShadow: "var(--shadow-card)",
        transform: h ? "translateY(-2px)" : "none",
        transition: "transform var(--dur-base) var(--ease-out), border-color var(--dur-base)",
      }}>
      <div style={{ display: "flex", alignItems: "center", gap: 11 }}>
        <span style={{
          width: 36, height: 36, flexShrink: 0, display: "flex", alignItems: "center", justifyContent: "center",
          borderRadius: 9, background: "var(--bg-surface-2)", border: "1px solid var(--border)",
          fontFamily: "var(--font-mono)", fontSize: 16, fontWeight: 700,
          color: h ? "var(--primary-300)" : "var(--text-muted)", transition: "color var(--dur-base)",
        }}>{p.mark}</span>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 14.5, fontWeight: 600, color: "var(--text-strong)" }}>{p.name}</div>
          <div style={{ fontSize: 11.5, fontFamily: "var(--font-mono)", color: "var(--text-subtle)" }}>{p.channel}</div>
        </div>
      </div>
      <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
        {p.models.map((m) => (
          <span key={m} style={{
            fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-muted)",
            padding: "3px 8px", borderRadius: "var(--radius-sm)", background: "var(--bg-surface-2)", border: "1px solid var(--border)",
          }}>{m}</span>
        ))}
      </div>
    </div>
  );
}

function SiteProviders() {
  return (
    <section id="providers" className="hk-section">
      <div className="hk-container">
        <div style={{ maxWidth: 720 }}>
          <div className="hk-eyebrow">上游供应商 · upstream providers</div>
          <h2 className="hk-h2">接你已经在用的账号</h2>
          <p className="hk-lead">
            HUAKAI 不转售额度。它用你自己的 provider 账号，按健康度（<span className="hk-code">operational</span> /
            <span className="hk-code">cooling_down</span> / <span className="hk-code">degraded</span>）与并发上限调度。
          </p>
        </div>
        <div className="hk-provider-grid" style={{ marginTop: 32 }}>
          {HK_PROVIDERS.map((p) => <ProviderCard key={p.name} p={p} />)}
        </div>
        <p style={{ marginTop: 18, fontSize: 13, color: "var(--text-subtle)", display: "flex", alignItems: "center", gap: 8 }}>
          <Icon name="plus" size={14} color="var(--text-subtle)" /> 更多 provider 适配中 — 协议层可扩展。
        </p>
      </div>
    </section>
  );
}
window.SiteProviders = SiteProviders;
