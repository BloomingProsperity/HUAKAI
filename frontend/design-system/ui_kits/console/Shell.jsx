// HUAKAI console shell — two-portal grouped sidebar (运营台 / 用户门户), sticky header, breadcrumb.

// One nav row. level: 0 = top leaf, 1 = group header, 2 = sub-item.
function NavRow({ level = 0, active = false, bright = false, onClick, title, children }) {
  const [h, setH] = React.useState(false);
  const sub = level === 2;
  return (
    <button
      title={title}
      onClick={onClick}
      onMouseEnter={() => setH(true)}
      onMouseLeave={() => setH(false)}
      style={{
        display: "flex", alignItems: "center", gap: sub ? 10 : 12, width: "100%",
        minHeight: sub ? 34 : 40, padding: sub ? "0 12px 0 38px" : "0 12px",
        border: "none", borderRadius: 8, cursor: "pointer", textAlign: "left",
        fontFamily: "var(--font-sans)", fontSize: sub ? 13 : 14, fontWeight: level === 1 ? 600 : 500,
        background: active ? "rgba(20,184,166,0.10)" : h ? "rgba(148,163,184,0.10)" : "transparent",
        color: active ? "var(--primary-300)" : bright ? "var(--text-strong)" : "var(--text-muted)",
        boxShadow: active ? "inset 0 0 0 1px var(--accent-soft-border)" : "none",
        transition: "background var(--dur-fast), color var(--dur-fast)",
      }}
    >
      {children}
    </button>
  );
}

function PortalSwitch({ portal, onPortal }) {
  return (
    <div style={{ display: "flex", gap: 4, margin: "0 12px", padding: 4, borderRadius: 8, background: "var(--bg-surface-2)", border: "1px solid var(--border)" }}>
      {[["ops", "运营台"], ["user", "用户门户"]].map(([k, label]) => {
        const on = portal === k;
        return (
          <button key={k} onClick={() => !on && onPortal(k)} style={{
            flex: 1, minHeight: 34, border: "none", borderRadius: 6, cursor: on ? "default" : "pointer",
            fontSize: 13, fontWeight: 600, fontFamily: "var(--font-sans)",
            background: on ? "var(--primary-500)" : "transparent", color: on ? "#fff" : "var(--text-muted)",
            boxShadow: on ? "var(--shadow-glow)" : "none", transition: "background var(--dur-fast), color var(--dur-fast)",
          }}>{label}</button>
        );
      })}
    </div>
  );
}

function NavTree({ portal, activeId, onSelect }) {
  const nav = window.HK_NAV[portal];
  const activeGroup = activeId.split(":")[1];
  const [open, setOpen] = React.useState({});
  React.useEffect(() => { setOpen((o) => ({ ...o, [activeGroup]: true })); }, [activeGroup, portal]);
  return (
    <nav style={{ flex: 1, minHeight: 0, overflowY: "auto", padding: "10px 12px 16px" }}>
      <ul style={{ listStyle: "none", margin: 0, padding: 0, display: "flex", flexDirection: "column", gap: 2 }}>
        {nav.groups.map((g) => {
          const gid = portal + ":" + g.label;
          if (!g.items) {
            const on = activeId === gid;
            return (
              <li key={g.label}>
                <NavRow level={0} active={on} bright={on} onClick={() => onSelect(gid)}>
                  <Icon name={g.icon} /><span>{g.label}</span>
                </NavRow>
              </li>
            );
          }
          const expanded = !!open[g.label];
          const groupActive = activeGroup === g.label;
          return (
            <li key={g.label}>
              <NavRow level={1} bright={groupActive} onClick={() => setOpen((o) => ({ ...o, [g.label]: !o[g.label] }))}>
                <Icon name={g.icon} />
                <span style={{ flex: 1 }}>{g.label}</span>
                <Icon name="chevron-right" size={14} style={{ transform: expanded ? "rotate(90deg)" : "none", transition: "transform var(--dur-fast)", opacity: 0.7 }} />
              </NavRow>
              {expanded && (
                <ul style={{ listStyle: "none", margin: "2px 0 4px", padding: 0, display: "flex", flexDirection: "column", gap: 2 }}>
                  {g.items.map((it) => {
                    const iid = gid + ":" + it;
                    const on = activeId === iid;
                    return (
                      <li key={it}>
                        <NavRow level={2} active={on} bright={on} onClick={() => onSelect(iid)}>
                          <span style={{ width: 6, height: 6, borderRadius: "50%", flexShrink: 0, background: on ? "var(--primary-400)" : "var(--neutral-700)", boxShadow: on ? "var(--shadow-glow)" : "none" }} />
                          <span>{it}</span>
                        </NavRow>
                      </li>
                    );
                  })}
                </ul>
              )}
            </li>
          );
        })}
      </ul>
    </nav>
  );
}

function Sidebar({ portal, portalLabel, activeId, onSelect, onPortal }) {
  return (
    <aside style={{
      position: "fixed", insetBlock: 0, left: 0, width: "var(--sidebar-w)", zIndex: 20,
      display: "flex", flexDirection: "column", background: "var(--bg-surface)",
      borderRight: "1px solid var(--border)", boxShadow: "var(--shadow-card)",
    }}>
      <div style={{ height: "var(--header-h)", flexShrink: 0, display: "flex", alignItems: "center", gap: 12, padding: "0 14px", borderBottom: "1px solid var(--border)" }}>
        <span style={{ width: 40, height: 40, flexShrink: 0, display: "flex", alignItems: "center", justifyContent: "center", borderRadius: 10, background: "var(--primary-500)", color: "#fff", fontWeight: 700, fontSize: 14, boxShadow: "var(--shadow-glow)" }}>HK</span>
        <span style={{ minWidth: 0 }}>
          <span style={{ display: "block", fontSize: 16, fontWeight: 700, color: "var(--text-strong)" }}>HUAKAI</span>
          <span style={{ display: "block", fontSize: 12, color: "var(--text-muted)" }}>{portalLabel}</span>
        </span>
      </div>
      <div style={{ padding: "12px 0", flexShrink: 0 }}>
        <PortalSwitch portal={portal} onPortal={onPortal} />
      </div>
      <NavTree portal={portal} activeId={activeId} onSelect={onSelect} />
      <div style={{ borderTop: "1px solid var(--border)", padding: 12, flexShrink: 0 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 12, borderRadius: 8, background: "var(--bg-surface-2)", padding: 12 }}>
          <Icon name="shield-check" color="var(--primary-300)" />
          <div style={{ minWidth: 0 }}>
            <div style={{ fontSize: 12, fontWeight: 600, color: "var(--text-strong)" }}>{portalLabel}</div>
            <div style={{ fontSize: 11, color: "var(--text-muted)", display: "flex", alignItems: "center", gap: 6 }}>
              <Icon name="activity" size={12} /> 本地开发 · v0.1.0
            </div>
          </div>
        </div>
      </div>
    </aside>
  );
}

function Header({ loc }) {
  const { StatusDot } = window.HUAKAIDesignSystem_36f9be;
  const crumbs = [loc.portalLabel, loc.groupLabel, loc.itemLabel].filter(Boolean);
  return (
    <header style={{
      position: "sticky", top: 0, zIndex: 10, minHeight: "var(--header-h)", display: "flex",
      alignItems: "center", justifyContent: "space-between", gap: 12, padding: "0 28px",
      borderBottom: "1px solid var(--border)", background: "rgba(255,255,255,0.85)", backdropFilter: "blur(16px)",
    }}>
      <nav style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13, minWidth: 0 }}>
        {crumbs.map((c, i) => {
          const last = i === crumbs.length - 1;
          return (
            <React.Fragment key={i}>
              {i > 0 && <Icon name="chevron-right" size={14} color="var(--text-subtle)" />}
              <span style={{ color: last ? "var(--text-strong)" : "var(--text-muted)", fontWeight: last ? 600 : 500, whiteSpace: "nowrap" }}>{c}</span>
            </React.Fragment>
          );
        })}
      </nav>
      <div style={{ display: "flex", alignItems: "center", gap: 12, flexShrink: 0 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, borderRadius: 8, border: "1px solid var(--success-border)", background: "var(--success-bg)", padding: "8px 12px", fontSize: 12, fontWeight: 500, color: "var(--success-fg)" }}>
          <StatusDot tone="online" pulse /> <span>后端心跳</span>
          <span style={{ fontFamily: "var(--font-mono)" }}>42ms</span>
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 8, borderRadius: 8, border: "1px solid var(--border)", background: "var(--bg-surface-2)", padding: "7px 10px", fontSize: 12, color: "var(--text-muted)" }}>
          <span style={{ width: 20, height: 20, borderRadius: "50%", background: "var(--primary-500)", color: "#fff", fontSize: 10, fontWeight: 700, display: "flex", alignItems: "center", justifyContent: "center" }}>H</span>
          <span>管理员</span>
        </div>
      </div>
    </header>
  );
}

function Shell({ portal, portalLabel, activeId, onSelect, onPortal, loc, children }) {
  return (
    <div style={{ minHeight: "100vh", background: "var(--bg-app)", color: "var(--text-body)" }}>
      <Sidebar portal={portal} portalLabel={portalLabel} activeId={activeId} onSelect={onSelect} onPortal={onPortal} />
      <div style={{ minHeight: "100vh", display: "flex", flexDirection: "column", paddingLeft: "var(--sidebar-w)" }}>
        <Header loc={loc} />
        <main style={{ flex: 1, padding: "28px" }}>{children}</main>
      </div>
    </div>
  );
}

// Generic on-brand placeholder for nodes without a built view.
function Placeholder({ loc }) {
  const { Card, CardContent, Badge, Button } = window.HUAKAIDesignSystem_36f9be;
  const title = loc.itemLabel || loc.groupLabel;
  return (
    <div>
      <section style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: 16, flexWrap: "wrap", marginBottom: 24 }}>
        <div>
          <div style={{ fontSize: 12, fontWeight: 500, color: "var(--primary-300)" }}>{loc.portalLabel} · {loc.groupLabel}</div>
          <h1 style={{ margin: "4px 0 0", fontSize: 24, fontWeight: 700, color: "var(--text-strong)", display: "flex", alignItems: "center", gap: 10 }}>
            <Icon name={loc.groupIcon} size={22} color="var(--text-muted)" /> {title}
          </h1>
        </div>
        <div style={{ display: "flex", gap: 10 }}>
          <Button variant="outline" size="sm" disabled><Icon name="refresh-cw" /> 刷新</Button>
          <Button size="sm" disabled><Icon name="plus" /> 新建</Button>
        </div>
      </section>
      <Card>
        <CardContent style={{ padding: "56px 40px", textAlign: "center" }}>
          <div style={{ width: 52, height: 52, margin: "0 auto", display: "flex", alignItems: "center", justifyContent: "center", borderRadius: 12, background: "var(--bg-surface-2)", border: "1px solid var(--border)" }}>
            <Icon name={loc.groupIcon} size={24} color="var(--text-subtle)" />
          </div>
          <div style={{ marginTop: 16, display: "flex", alignItems: "center", justifyContent: "center", gap: 8 }}>
            <span style={{ fontSize: 16, fontWeight: 600, color: "var(--text-body)" }}>{title}</span>
            <Badge variant="secondary" style={{ background: "rgba(139,92,246,0.14)", color: "#a78bfa", borderColor: "rgba(139,92,246,0.32)" }}>占位</Badge>
          </div>
          <p style={{ margin: "10px auto 0", maxWidth: 420, fontSize: 13.5, lineHeight: 1.6, color: "var(--text-subtle)" }}>
            该视图为导航布局占位 — 页面与后端接口待接入。不会用本地假数据补齐。
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

window.Shell = Shell;
window.Placeholder = Placeholder;
