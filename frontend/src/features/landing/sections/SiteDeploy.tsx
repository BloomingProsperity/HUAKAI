// HUAKAI 落地页 —— 自托管 CTA 横幅分区。
import { Button, Icon } from '../landingKit';

// 主分区组件:自托管引导横幅(克隆仓库 + docker compose 起服务)。
export function SiteDeploy(): JSX.Element {
  return (
    <section id="deploy" className="hk-section">
      <div className="hk-container">
        <div style={{ position: "relative", overflow: "hidden", borderRadius: 16, border: "1px solid var(--border-strong)", background: "var(--bg-surface)", boxShadow: "var(--shadow-card)", padding: "clamp(32px, 5vw, 56px)" }}>
          <div aria-hidden="true" style={{ position: "absolute", inset: 0, background: "radial-gradient(120% 140% at 85% 0%, rgba(200,242,74,0.16), transparent 55%)", pointerEvents: "none" }} />
          <div style={{ position: "relative", display: "flex", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: 32 }}>
            <div style={{ maxWidth: 520 }}>
              <div className="hk-eyebrow">自托管 · self-host</div>
              <h2 className="hk-h2" style={{ marginTop: 8 }}>把账号池接进来，几分钟起服务</h2>
              <p className="hk-lead" style={{ marginBottom: 0 }}>克隆仓库，配置上游账号，单机 docker compose 即可拉起网关与控制台。私钥与流量都留在你自己的机器上。</p>
            </div>
            <div style={{ display: "flex", flexDirection: "column", gap: 14, minWidth: 280, flex: 1 }}>
              <div style={{
                display: "flex", alignItems: "center", gap: 10, padding: "14px 16px", borderRadius: "var(--radius)",
                border: "1px solid var(--border)", background: "var(--neutral-950)", fontFamily: "var(--font-mono)", fontSize: 13, color: "var(--neutral-300)",
              }}>
                <span style={{ color: "var(--text-subtle)" }}>$</span>
                <span style={{ flex: 1 }}>git clone …/HUAKAI && docker compose up -d</span>
                <Icon name="copy" size={15} color="var(--text-subtle)" />
              </div>
              <div style={{ display: "flex", flexWrap: "wrap", gap: 12 }}>
                <Button size="lg" onClick={() => { window.location.href = '/'; }}>进入控制台 <Icon name="arrow-right" /></Button>
                <a href="https://github.com/BloomingProsperity/HUAKAI" target="_blank" rel="noreferrer" className="hk-outline-btn" style={{
                  display: "inline-flex", alignItems: "center", gap: 8, height: "2.75rem", padding: "0 1.5rem",
                  borderRadius: "var(--radius-md)", border: "1px solid var(--border-strong)", background: "transparent",
                  color: "var(--text-body)", fontSize: 16, fontWeight: 500, textDecoration: "none",
                }}><Icon name="book-open" /> 阅读文档</a>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
