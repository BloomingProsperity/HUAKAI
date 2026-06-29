// HUAKAI site — footer.
function SiteFooter() {
  const { StatusDot } = window.HUAKAIDesignSystem_36f9be;
  const cols = [
    { h: "产品", links: ["运营总览", "账号池", "Chat 调试器", "审计"] },
    { h: "资源", links: ["文档", "GitHub", "更新日志", "协议适配"] },
    { h: "关于", links: ["架构", "安全与自托管", "许可 · MIT"] },
  ];
  return (
    <footer style={{ borderTop: "1px solid var(--border)", background: "var(--bg-surface)", marginTop: 24 }}>
      <div className="hk-container" style={{ paddingTop: 48, paddingBottom: 40 }}>
        <div className="hk-footer-grid">
          <div style={{ maxWidth: 320 }}>
            <HKLogo sub="华凯" />
            <p style={{ margin: "16px 0 0", fontSize: 13, lineHeight: 1.6, color: "var(--text-subtle)" }}>
              self-hosted AI gateway · account hub · admin ops. 挡在你自有上游账号前的统一协议与调度层。
            </p>
          </div>
          {cols.map((c) => (
            <div key={c.h}>
              <div style={{ fontSize: 12, fontWeight: 600, textTransform: "uppercase", letterSpacing: "0.04em", color: "var(--text-subtle)" }}>{c.h}</div>
              <ul style={{ listStyle: "none", margin: "14px 0 0", padding: 0, display: "flex", flexDirection: "column", gap: 10 }}>
                {c.links.map((l) => (
                  <li key={l}><a href="#" className="hk-foot-link" style={{ fontSize: 13.5, color: "var(--text-muted)", textDecoration: "none", transition: "color var(--dur-fast)" }}>{l}</a></li>
                ))}
              </ul>
            </div>
          ))}
        </div>
        <div style={{ marginTop: 40, paddingTop: 20, borderTop: "1px solid var(--border)", display: "flex", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: 12 }}>
          <span style={{ fontSize: 12.5, color: "var(--text-subtle)", fontFamily: "var(--font-mono)" }}>© 2026 HUAKAI 华凯 · v0.1.0 · MIT</span>
          <span style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 12.5, color: "var(--text-muted)" }}>
            <StatusDot tone="online" pulse /> 后端心跳 · <span style={{ fontFamily: "var(--font-mono)" }}>本地开发</span>
          </span>
        </div>
      </div>
    </footer>
  );
}
window.SiteFooter = SiteFooter;
