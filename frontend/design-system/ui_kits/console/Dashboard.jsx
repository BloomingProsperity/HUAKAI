// HUAKAI 运营总览 dashboard view — KPI grid, cache-hit trend, account table, alert panel.
function PageHeader({ onRefresh, spinning }) {
  const { Button } = window.HUAKAIDesignSystem_36f9be;
  return (
    <section style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: 16, flexWrap: "wrap", borderRadius: 8, border: "1px solid var(--border)", background: "var(--bg-surface)", padding: "16px 20px", boxShadow: "var(--shadow-card)", marginBottom: 24 }}>
      <div>
        <div style={{ fontSize: 12, fontWeight: 500, color: "var(--primary-300)" }}>P1 总览</div>
        <h1 style={{ margin: "4px 0 0", fontSize: 24, fontWeight: 700, color: "var(--text-strong)" }}>运营总览</h1>
        <p style={{ margin: "8px 0 0", fontSize: 14, color: "var(--text-muted)" }}>真实后端账号池健康、成本、用量与缓存效率集中视图</p>
      </div>
      <Button variant="outline" size="sm" onClick={onRefresh}>
        <Icon name="refresh-cw" style={{ animation: spinning ? "hk-spin 0.8s linear infinite" : "none" }} /> 刷新
      </Button>
    </section>
  );
}

function TrendPanel() {
  const { Card, CardHeader, CardTitle, CardContent } = window.HUAKAIDesignSystem_36f9be;
  const pts = [62, 58, 64, 71, 69, 74, 78, 82, 80, 85, 87, 84, 88, 86, 90];
  const w = 760, h = 180, max = 100;
  const step = w / (pts.length - 1);
  const line = pts.map((p, i) => `${i * step},${h - (p / max) * h}`).join(" ");
  const area = `0,${h} ${line} ${w},${h}`;
  return (
    <Card style={{ marginBottom: 24 }}>
      <CardHeader><CardTitle>24h 缓存命中率趋势</CardTitle></CardHeader>
      <CardContent>
        <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" style={{ width: "100%", height: 180, display: "block" }}>
          <defs>
            <linearGradient id="hk-fill" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="rgba(20,184,166,0.28)" />
              <stop offset="100%" stopColor="rgba(20,184,166,0)" />
            </linearGradient>
          </defs>
          {[0.25, 0.5, 0.75].map((g) => <line key={g} x1="0" y1={h * g} x2={w} y2={h * g} stroke="rgba(148,163,184,0.18)" strokeDasharray="3 3" />)}
          <polygon points={area} fill="url(#hk-fill)" />
          <polyline points={line} fill="none" stroke="#14b8a6" strokeWidth="2.5" />
        </svg>
        <div style={{ display: "flex", justifyContent: "space-between", marginTop: 8, fontSize: 11, fontFamily: "var(--font-mono)", color: "var(--text-subtle)" }}>
          <span>00:00</span><span>06:00</span><span>12:00</span><span>18:00</span><span>现在</span>
        </div>
      </CardContent>
    </Card>
  );
}

function AccountTable() {
  const { Card, CardHeader, CardTitle, CardContent, Badge, Table, THead, TBody, TR, TH, TD } = window.HUAKAIDesignSystem_36f9be;
  return (
    <Card>
      <CardHeader><CardTitle><Icon name="layers" color="var(--primary-400)" /> Top 5 供应商账号</CardTitle></CardHeader>
      <CardContent style={{ padding: "0 0 8px" }}>
        <Table>
          <THead><TR hover={false}><TH>账号</TH><TH>供应商</TH><TH>模型</TH><TH>健康状态</TH><TH>并发</TH><TH>调度</TH></TR></THead>
          <TBody>
            {window.HK_ACCOUNTS.map((a) => {
              const hl = window.HK_HEALTH[a.health], sc = window.HK_SCHEDULE[a.schedule];
              return (
                <TR key={a.id}>
                  <TD><div style={{ fontWeight: 500, color: "var(--text-strong)" }}>{a.id}</div><div style={{ fontSize: 11, color: "var(--text-subtle)", marginTop: 2 }}>{a.channel}</div></TD>
                  <TD style={{ color: "var(--text-muted)" }}>{a.provider}</TD>
                  <TD style={{ color: "var(--text-muted)" }}>{a.models[0]}{a.models.length > 1 ? ` +${a.models.length - 1}` : ""}</TD>
                  <TD><Badge variant={hl.variant}>{hl.label}</Badge></TD>
                  <TD mono>{a.inFlight}/{a.cap}</TD>
                  <TD><Badge variant={sc.variant}>{sc.label}</Badge></TD>
                </TR>
              );
            })}
          </TBody>
        </Table>
      </CardContent>
    </Card>
  );
}

function HealthPanel() {
  const { Card, CardHeader, CardTitle, CardContent, Badge } = window.HUAKAIDesignSystem_36f9be;
  const total = window.HK_ACCOUNTS.length;
  const healthy = window.HK_ACCOUNTS.filter((a) => a.health === "operational").length;
  const ratio = Math.round((healthy / total) * 100);
  const risky = window.HK_ACCOUNTS.filter((a) => a.health !== "operational");
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 24 }}>
      <Card>
        <CardHeader><CardTitle><Icon name="shield-alert" color="#fbbf24" /> 异常告警条件</CardTitle></CardHeader>
        <CardContent style={{ display: "flex", flexDirection: "column", gap: 12 }}>
          {risky.map((a) => (
            <div key={a.id} style={{ borderRadius: 8, border: "1px solid var(--border)", background: "var(--bg-surface-2)", padding: 12 }}>
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: 8 }}>
                <span style={{ fontWeight: 500, color: "var(--text-strong)" }}>{a.id}</span>
                <Badge variant={window.HK_HEALTH[a.health].variant}>{window.HK_HEALTH[a.health].label}</Badge>
              </div>
              <div style={{ marginTop: 8, fontSize: 12, color: "var(--text-muted)" }}>并发 {a.inFlight}/{a.cap} · 失败计数 {a.fail}</div>
            </div>
          ))}
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle><Icon name="heart-pulse" color="var(--primary-400)" /> 健康账号比例</CardTitle></CardHeader>
        <CardContent>
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-end" }}>
            <div>
              <div style={{ fontSize: 30, fontWeight: 700, color: "var(--text-strong)", fontVariantNumeric: "tabular-nums" }}>{ratio}%</div>
              <div style={{ marginTop: 4, fontSize: 14, color: "var(--text-muted)" }}>{healthy} / {total} 健康</div>
            </div>
            <Badge variant="secondary">降级 1 · 失败 1</Badge>
          </div>
          <div style={{ marginTop: 16, height: 12, borderRadius: 999, background: "var(--bg-surface-2)", overflow: "hidden" }}>
            <div style={{ height: "100%", width: `${ratio}%`, borderRadius: 999, background: "var(--primary-500)", boxShadow: "var(--shadow-glow)" }} />
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function Dashboard() {
  const { StatCard } = window.HUAKAIDesignSystem_36f9be;
  const [spinning, setSpinning] = React.useState(false);
  const refresh = () => { setSpinning(true); setTimeout(() => setSpinning(false), 900); };
  const stats = [
    { title: "今日 Token 用量", value: "1,284,500", icon: "database-zap", description: "输入、输出、缓存合计", detail: "输入 820,400 / 输出 464,100", tone: "primary" },
    { title: "今日成本", value: "$38.42", icon: "dollar-sign", description: "usage.actual_cost 汇总", detail: "未做本地币种换算", tone: "emerald" },
    { title: "请求数", value: "9,317", icon: "zap", description: "今日 usage 记录数", detail: "待对账 12 条", tone: "blue" },
    { title: "P95 结算耗时", value: "1.24s", icon: "clock-3", description: "settled − requested", detail: "P50 0.42s / P99 2.10s", tone: "amber" },
    { title: "并发数", value: "14", icon: "activity", description: "当前飞行中请求", detail: "容量上限 40", tone: "slate" },
    { title: "缓存读取占比", value: "87.4%", icon: "gauge", description: "read / (creation + read)", detail: "读取 612k / 创建 88k", tone: "primary" },
  ];
  return (
    <div>
      <PageHeader onRefresh={refresh} spinning={spinning} />
      <section style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))", gap: 16, marginBottom: 24 }}>
        {stats.map((s) => <StatCard key={s.title} {...s} icon={<Icon name={s.icon} />} />)}
      </section>
      <TrendPanel />
      <section style={{ display: "grid", gridTemplateColumns: "minmax(0,2fr) minmax(300px,1fr)", gap: 24 }}>
        <AccountTable />
        <HealthPanel />
      </section>
    </div>
  );
}

window.Dashboard = Dashboard;
