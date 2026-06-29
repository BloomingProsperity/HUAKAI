// 账号池 — provider account list with filter chips and a "新增账号" action.
function Accounts() {
  const { Card, CardHeader, CardTitle, CardContent, Badge, Button, Table, THead, TBody, TR, TH, TD, Input } = window.HUAKAIDesignSystem_36f9be;
  const [filter, setFilter] = React.useState("all");
  const chips = [
    { key: "all", label: "全部" },
    { key: "operational", label: "健康" },
    { key: "degraded", label: "降级" },
    { key: "cooling_down", label: "冷却中" },
    { key: "failed", label: "失败" },
  ];
  const rows = window.HK_ACCOUNTS.filter((a) => filter === "all" || a.health === filter);
  return (
    <div>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: 16, flexWrap: "wrap", marginBottom: 20 }}>
        <div>
          <h1 style={{ margin: 0, fontSize: 24, fontWeight: 700, color: "var(--text-strong)" }}>账号池</h1>
          <p style={{ margin: "6px 0 0", fontSize: 14, color: "var(--text-muted)" }}>Provider Account — list / create / clear-rate-limit</p>
        </div>
        <Button><Icon name="plus" /> 新增账号</Button>
      </div>
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 16, flexWrap: "wrap" }}>
        <div style={{ width: 240 }}>
          <Input placeholder="搜索账号 ID / 供应商…" />
        </div>
        <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
          {chips.map((c) => (
            <button key={c.key} onClick={() => setFilter(c.key)} style={{
              padding: "6px 12px", borderRadius: 999, fontSize: 12, fontWeight: 600, cursor: "pointer",
              fontFamily: "var(--font-sans)",
              border: `1px solid ${filter === c.key ? "var(--accent-soft-border)" : "var(--border)"}`,
              background: filter === c.key ? "var(--accent-soft-bg)" : "transparent",
              color: filter === c.key ? "var(--primary-300)" : "var(--text-muted)",
            }}>{c.label}</button>
          ))}
        </div>
      </div>
      <Card>
        <CardContent style={{ padding: "6px 0" }}>
          <Table>
            <THead><TR hover={false}><TH>账号</TH><TH>供应商</TH><TH>模型</TH><TH>健康</TH><TH>调度</TH><TH>并发</TH><TH>失败</TH><TH></TH></TR></THead>
            <TBody>
              {rows.map((a) => {
                const hl = window.HK_HEALTH[a.health], sc = window.HK_SCHEDULE[a.schedule];
                return (
                  <TR key={a.id}>
                    <TD><div style={{ fontWeight: 500, color: "var(--text-strong)" }}>{a.id}</div><div style={{ fontSize: 11, color: "var(--text-subtle)", marginTop: 2, fontFamily: "var(--font-mono)" }}>{a.channel}</div></TD>
                    <TD style={{ color: "var(--text-muted)" }}>{a.provider}</TD>
                    <TD style={{ color: "var(--text-muted)" }}>{a.models.join("、")}</TD>
                    <TD><Badge variant={hl.variant}>{hl.label}</Badge></TD>
                    <TD><Badge variant={sc.variant}>{sc.label}</Badge></TD>
                    <TD mono>{a.inFlight}/{a.cap}</TD>
                    <TD mono style={{ color: a.fail > 0 ? "var(--danger-fg)" : "var(--text-muted)" }}>{a.fail}</TD>
                    <TD><Button variant="ghost" size="sm"><Icon name="ellipsis" /></Button></TD>
                  </TR>
                );
              })}
            </TBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}

window.Accounts = Accounts;
