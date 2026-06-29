// Chat 调试器 — send a prompt through the gateway; fake streamed SSE response.
function Chat() {
  const { Card, CardContent, Button, Input, Label, Badge } = window.HUAKAIDesignSystem_36f9be;
  const [model, setModel] = React.useState("claude-sonnet-4.5");
  const [prompt, setPrompt] = React.useState("用一句话解释反向代理网关的作用。");
  const [output, setOutput] = React.useState("");
  const [streaming, setStreaming] = React.useState(false);
  const timer = React.useRef(null);

  const send = () => {
    if (streaming) return;
    const full = "反向代理网关（如 HUAKAI）位于客户端与多个上游 LLM 账号之间，统一协议入口、做健康感知的账号调度与限流重试，并在转发流式响应的同时完成用量与计费结算。";
    setOutput(""); setStreaming(true);
    let i = 0;
    timer.current = setInterval(() => {
      i += 2;
      setOutput(full.slice(0, i));
      if (i >= full.length) { clearInterval(timer.current); setStreaming(false); }
    }, 24);
  };
  React.useEffect(() => () => clearInterval(timer.current), []);

  const models = ["claude-sonnet-4.5", "claude-opus-4.1", "gpt-5", "gemini-2.5-pro"];
  return (
    <div>
      <div style={{ marginBottom: 20 }}>
        <h1 style={{ margin: 0, fontSize: 24, fontWeight: 700, color: "var(--text-strong)" }}>Chat 调试器</h1>
        <p style={{ margin: "6px 0 0", fontSize: 14, color: "var(--text-muted)" }}>POST /v1/messages · 支持 SSE 流式</p>
      </div>
      <div style={{ display: "grid", gridTemplateColumns: "minmax(0,1fr) minmax(0,1fr)", gap: 24 }}>
        <Card>
          <CardContent style={{ padding: 20, display: "flex", flexDirection: "column", gap: 16 }}>
            <div>
              <Label>模型</Label>
              <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                {models.map((m) => (
                  <button key={m} onClick={() => setModel(m)} style={{
                    padding: "6px 12px", borderRadius: 6, fontSize: 12, fontFamily: "var(--font-mono)", cursor: "pointer",
                    border: `1px solid ${model === m ? "var(--accent-soft-border)" : "var(--border)"}`,
                    background: model === m ? "var(--accent-soft-bg)" : "var(--bg-surface-2)",
                    color: model === m ? "var(--primary-300)" : "var(--text-muted)",
                  }}>{m}</button>
                ))}
              </div>
            </div>
            <div>
              <Label>Prompt</Label>
              <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} rows={5} style={{
                width: "100%", padding: "10px 12px", background: "var(--bg-surface-2)", color: "var(--text-body)",
                border: "1px solid var(--border-strong)", borderRadius: 6, fontFamily: "var(--font-sans)", fontSize: 14, resize: "vertical", outline: "none",
              }} />
            </div>
            <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
              <Button onClick={send} disabled={streaming}>
                <Icon name={streaming ? "loader" : "send"} style={{ animation: streaming ? "hk-spin 0.8s linear infinite" : "none" }} /> {streaming ? "流式中…" : "发送"}
              </Button>
              <span style={{ fontSize: 12, fontFamily: "var(--font-mono)", color: "var(--text-subtle)" }}>hk_live_9fA2…7c</span>
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardContent style={{ padding: 20 }}>
            <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 12 }}>
              <Label style={{ margin: 0 }}>响应</Label>
              <Badge variant={streaming ? "warning" : output ? "success" : "secondary"}>{streaming ? "streaming" : output ? "200 OK" : "idle"}</Badge>
            </div>
            <div style={{
              minHeight: 200, borderRadius: 8, border: "1px solid var(--border)", background: "var(--bg-surface-2)",
              padding: 14, fontSize: 14, lineHeight: 1.6, color: "var(--text-body)", whiteSpace: "pre-wrap",
            }}>
              {output || <span style={{ color: "var(--text-subtle)" }}>响应将在此处流式显示…</span>}
              {streaming && <span style={{ color: "var(--primary-400)" }}>▌</span>}
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

window.Chat = Chat;
