package anthropicoauth

// AnthropicPublicCLIClientID 是 Anthropic 官方 Claude Code CLI 的公开 OAuth
// client_id。credentialworker 刷新链(adapters/anthropic.go)把它当作硬编内置
// approved profile:operator 未显式注入 client_id 时的默认值,且一律不接受
// credential payload 覆盖(ANT-3 SSRF / auth-token 泄露防线)。
const AnthropicPublicCLIClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
