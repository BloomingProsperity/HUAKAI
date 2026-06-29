// Mock data for the HUAKAI console UI kit (no real backend).
window.HK_ACCOUNTS = [
  { id: "claude-pool-01", channel: "anthropic / oauth", provider: "Anthropic", models: ["claude-sonnet-4.5", "claude-opus-4.1"], health: "operational", schedule: "active", inFlight: 3, cap: 10, fail: 0 },
  { id: "gpt-team-a", channel: "openai / api-key", provider: "OpenAI", models: ["gpt-5", "gpt-4.1-mini"], health: "cooling_down", schedule: "limited", inFlight: 8, cap: 8, fail: 2 },
  { id: "vertex-eu-1", channel: "google / vertex", provider: "Google Vertex", models: ["gemini-2.5-pro"], health: "degraded", schedule: "active", inFlight: 1, cap: 6, fail: 1 },
  { id: "bedrock-us", channel: "aws / bedrock", provider: "AWS Bedrock", models: ["claude-sonnet-4.5"], health: "operational", schedule: "active", inFlight: 2, cap: 12, fail: 0 },
  { id: "router-or-1", channel: "openrouter / key", provider: "OpenRouter", models: ["mixed"], health: "failed", schedule: "requires_action", inFlight: 0, cap: 4, fail: 9 },
];

window.HK_HEALTH = {
  operational: { label: "健康", variant: "success" },
  degraded: { label: "降级", variant: "secondary" },
  cooling_down: { label: "冷却中", variant: "warning" },
  failed: { label: "失败", variant: "destructive" },
};
window.HK_SCHEDULE = {
  active: { label: "可调度", variant: "outline" },
  limited: { label: "受限", variant: "secondary" },
  requires_action: { label: "需处理", variant: "destructive" },
};
