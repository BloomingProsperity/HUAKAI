// Package officialclient 判定某 vendor 的账号是否强制官方客户端入站:Owner 决策
// (2026-07-07)后仅 Anthropic/Claude 账号强制 Claude Code;OpenAI/codex/chatgpt
// OAuth 账号默认放开,可由标准客户端经翻译层使用。
//
// 关于 Anthropic 直发门的诚实边界:UA、X-App、Stainless 头、body 字段全由客户端
// 自报,curl 可逐项复刻;官方也无客户端远程证明机制。因此本门只是**Claude Code
// 兼容形态门(启发式)**——回答"这是不是一个我们能服务的 Claude-family 请求形态",
// 不回答"调用者是谁"。真正的访问控制由其它层独立完成:API key 认证 + tenant 隔离 +
// 该 key 的 public model allowlist + 凭据状态 + 限流/quota;形态通过绝不绕过它们。
// 注意:当前选号按 tenant/pool/model/协议族/凭据状态过滤,**不含 per-反转账号 ACL**
// (哪个 key 能用池内哪个具体反转账号)——是否引入该 ACL 属 Owner 产品决策,未建。
// 审计语义应记"兼容形态通过"而非"已验证官方客户端"。
//
// 用法:通用路径可调 GateDecision;Anthropic 直发调 DecideAnthropicOfficialDirect。
package officialclient

import (
	"bytes"
	"encoding/json"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

// 判定原因常量(供审计日志与 403 错误信息渲染)。
const (
	ReasonNoRestriction      = "no_restriction"                // 未开门控,放行
	ReasonOfficialClientOK   = "official_client_ok"            // 官方客户端,放行
	ReasonNonOfficialReject  = "non_official_client_rejected"  // 非官方客户端,拒
	ReasonVendorNoOfficial   = "vendor_has_no_official_client" // vendor 无官方客户端映射
	ReasonVendorNotEnforced  = "vendor_official_client_not_enforced"
	ReasonStrictRequestShape = "strict_official_request_shape_mismatch"
	// ReasonShapeCompatible:入站命中 Claude Code 兼容形态(启发式,非不可伪造鉴真)。
	ReasonShapeCompatible = "claude_code_compatible_shape_accepted"
)

// supportedAnthropicVersions 是接受的 anthropic-version 值集合。用集合而非"像日期"
// 判据挡掉垃圾值;需随官方新增版本扩展(Claude Code 目前长期发 2023-06-01)。
var supportedAnthropicVersions = map[string]bool{
	"2023-06-01": true,
	"2023-01-01": true,
}

// DirectDecision 是 S1 官方直发的封闭判决。此处刻意没有改写态：
// 严格判据不成立只能拒绝，非官方准入留给后续独立切片。
type DirectDecision string

const (
	DirectDecisionOfficialDirect DirectDecision = "official_direct"
	DirectDecisionReject         DirectDecision = "reject"
)

// DirectResult 带回官方直发 body 的独立克隆。调用方可以继续做路由/model
// 处理，但在最终 composer 接缝必须再次克隆并保持字节等价。
type DirectResult struct {
	Decision DirectDecision
	Reason   string
	Body     []byte
}

var (
	// claudeCLIUserAgentPattern 锚定 `claude-cli/<semver>` 前缀,后接可选的
	// ` (external, <入口>[, ...])`。入口不做封闭白名单——cli / cli-bg / claude-vscode /
	// sdk-ts / sdk-py / jetbrains 等演进入口都放行(只要求非空可打印 token);旧版
	// 无 external 后缀的裸 `claude-cli/<semver>` 也接受。锚定 ^ 挡掉 `curl/8 ... claude-cli/...`。
	claudeCLIUserAgentPattern = regexp.MustCompile(`^claude-cli/[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?(?: \(external, [^)]+\))?$`)
	semanticVersionPattern    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
)

// RequiresStrictAnthropicDirect 报告本次账号是否进入 Anthropic 官方直发门。
// 只覆盖反转账号；API key 与其它 vendor 沿用各自现有策略。
func RequiresStrictAnthropicDirect(accountType, vendor string, forceOfficialClient bool) bool {
	if !IsReverseAccountType(accountType) || (!vendorEnforcesOfficialClient(vendor) && !forceOfficialClient) {
		return false
	}
	required, ok := RequiredIdentity(vendor)
	return ok && required == clientid.IdentityClaudeCode
}

// DecideAnthropicOfficialDirect 判定 /v1/messages 入站是否为可直发的 Claude Code 兼容
// 形态:MessagesCore(model+messages+整数 max_tokens>=0)。UA/X-App/Stainless 核心头做
// 形态校验,但版本不锁死、辅助字段(system/metadata/beta/os/arch/timeout/helper)一律
// 可选,避免误拒真实 2.x 的探测/quota,以及 count_tokens 501 后回退到 /v1/messages 的
// 计数请求(max_tokens=1、常无 system)。count_tokens 端点本身当前返 501(Mandatory
// Roadmap),不经本门。这是启发式形态门,不做访问控制;先克隆输入再解码,避免改写直发字节。
func DecideAnthropicOfficialDirect(r *http.Request, body []byte) DirectResult {
	directBody := bytes.Clone(body)
	if r == nil || r.Method != http.MethodPost || r.URL == nil || r.URL.Path != "/v1/messages" {
		return DirectResult{Decision: DirectDecisionReject, Reason: ReasonStrictRequestShape}
	}
	if !claudeCLIUserAgentPattern.MatchString(strings.TrimSpace(r.UserAgent())) {
		return DirectResult{Decision: DirectDecisionReject, Reason: ReasonStrictRequestShape}
	}
	if !claudeCodeHeaderShapeOK(r.Header) || !messagesCoreShapeOK(bytes.Clone(directBody)) {
		return DirectResult{Decision: DirectDecisionReject, Reason: ReasonStrictRequestShape}
	}
	return DirectResult{Decision: DirectDecisionOfficialDirect, Reason: ReasonShapeCompatible, Body: directBody}
}

// claudeCodeHeaderShapeOK 校验 Claude Code SDK 核心头的形态:JSON Content-Type、
// X-App∈{cli,cli-bg}、Stainless lang=js/runtime=node、package 为 semver 形态、
// retry 非负整数、anthropic-version 属受支持集合。版本值不锁死、beta/os/arch/timeout/
// helper 均可选且不参与判定——这些都是自报字段,过严只会误拒真实客户端。
func claudeCodeHeaderShapeOK(h http.Header) bool {
	mediaType, _, mediaErr := mime.ParseMediaType(strings.TrimSpace(h.Get("Content-Type")))
	if mediaErr != nil || !strings.EqualFold(mediaType, "application/json") {
		return false
	}
	if !claudeCodeXAppOK(h) {
		return false
	}
	if strings.TrimSpace(h.Get("X-Stainless-Lang")) != "js" ||
		strings.TrimSpace(h.Get("X-Stainless-Runtime")) != "node" ||
		!semanticVersionPattern.MatchString(strings.TrimSpace(h.Get("X-Stainless-Package-Version"))) {
		return false
	}
	if !supportedAnthropicVersions[strings.TrimSpace(h.Get("Anthropic-Version"))] {
		return false
	}
	retryCount, err := strconv.Atoi(strings.TrimSpace(h.Get("X-Stainless-Retry-Count")))
	return err == nil && retryCount >= 0
}

// claudeCodeXAppOK 要求 X-App 落在 {cli, cli-bg}(区分大小写),且多值时不得冲突。
func claudeCodeXAppOK(h http.Header) bool {
	vals := h.Values("X-App")
	if len(vals) == 0 {
		return false
	}
	first := strings.TrimSpace(vals[0])
	for _, v := range vals[1:] {
		if strings.TrimSpace(v) != first {
			return false // 冲突重复头
		}
	}
	return first == "cli" || first == "cli-bg"
}

// messagesCoreShapeOK 校验 /v1/messages 的协议核心:非空 model、非空 messages、
// 整数 max_tokens>=0(官方允许 0 用于填充 cache)。system/metadata/beta 一律可选。
func messagesCoreShapeOK(body []byte) bool {
	root, model, messages, ok := parseClaudeCoreBody(body)
	if !ok {
		return false
	}
	var maxTokens int
	if json.Unmarshal(root["max_tokens"], &maxTokens) != nil || maxTokens < 0 {
		return false
	}
	_ = model
	_ = messages
	return true
}

// parseClaudeCoreBody 抽取并校验 Messages 协议核心(model+messages)。
func parseClaudeCoreBody(body []byte) (map[string]json.RawMessage, string, []json.RawMessage, bool) {
	var root map[string]json.RawMessage
	if len(body) == 0 || json.Unmarshal(body, &root) != nil || root == nil {
		return nil, "", nil, false
	}
	var model string
	if json.Unmarshal(root["model"], &model) != nil || strings.TrimSpace(model) == "" {
		return nil, "", nil, false
	}
	var messages []json.RawMessage
	if json.Unmarshal(root["messages"], &messages) != nil || len(messages) == 0 {
		return nil, "", nil, false
	}
	return root, model, messages, true
}

// RequiredIdentity 返回某 vendor 对应的官方客户端身份;ok=false 表示该 vendor 无对应
// 官方客户端映射。当前覆盖 Anthropic(Claude Code)与 OpenAI(Codex CLI)。注意:
// 该映射也供出站身份改写使用,不等同于入站强制策略。
func RequiredIdentity(vendor string) (clientid.Identity, bool) {
	switch strings.ToLower(strings.TrimSpace(vendor)) {
	case "anthropic", "claude":
		return clientid.IdentityClaudeCode, true
	case "openai", "codex", "chatgpt":
		return clientid.IdentityCodexCLI, true
	default:
		return "", false
	}
}

// vendorEnforcesOfficialClient 判定某 vendor 是否强制官方客户端入站。
// Owner 决策(2026-07-07):仅 Anthropic/claude 账号强制官方客户端(Claude Code);
// OpenAI/codex/chatgpt 账号默认放开——标准 chat/Responses/messages 客户端经翻译层即可用,
// 出站仍由 mimicryidentity 伪装成 Codex CLI,账号侧仍官方样貌。
func vendorEnforcesOfficialClient(vendor string) bool {
	switch strings.ToLower(strings.TrimSpace(vendor)) {
	case "anthropic", "claude":
		return true
	default:
		return false
	}
}

// reverseAuthModes 是反转/订阅号(OAuth/session 类凭据)的账号类型集合;这类账号会参与
// 入站官方客户端门的候选判定,也供出站身份改写限定 scope。官方 API key / 云凭据类
// (api_key/aistudio_api_key/bedrock/vertex_*/azure)不在此集合。取值为
// credentialstore.AuthMode*。
var reverseAuthModes = map[string]struct{}{
	credentialstore.AuthModeClaudeAIOAuth:      {},
	credentialstore.AuthModeClaudeCode:         {},
	credentialstore.AuthModeChatGPTOAuth:       {},
	credentialstore.AuthModeCodexCLIOAuth:      {},
	credentialstore.AuthModeCodexAgentIdentity: {},
	credentialstore.AuthModeCodexWebOAuth:      {},
	credentialstore.AuthModeCodeAssist:         {},
	credentialstore.AuthModeGoogleOne:          {},
	credentialstore.AuthModeAntigravity:        {},
	credentialstore.AuthModeCopilotOAuth:       {},
	credentialstore.AuthModeXAIOAuth:           {},
	credentialstore.AuthModeKimiOAuth:          {},
	credentialstore.AuthModeOAuth:              {},
	credentialstore.AuthModeRefreshToken:       {},
}

// IsReverseAccountType 报告账号类型是否为反转/订阅号(OAuth/session 类)。
// accountType 取值为 provider.AccountInfo.AccountType(= credentialstore.AuthMode*);
// 官方 API key / 云凭据类及未知/空值返回 false。
func IsReverseAccountType(accountType string) bool {
	_, ok := reverseAuthModes[strings.ToLower(strings.TrimSpace(accountType))]
	return ok
}

// GateDecision 报告某账号类型 + vendor 下、已检测出 clientIdentity 的请求是否应被拒。
// 仅反转/订阅号 + vendor 默认强制官方客户端入站或账号级 forceOfficialClient + 身份
// 非官方时拒;非反转号不拒。forceOfficialClient 只扩大已有官方客户端映射 vendor 的
// 入站门控,不越过反转账号前置条件;无官方客户端映射仍 fail-open。返回 (reject, reason)。
func GateDecision(accountType, vendor string, clientIdentity clientid.Identity, forceOfficialClient bool) (bool, string) {
	if !IsReverseAccountType(accountType) {
		return false, ReasonNoRestriction
	}
	if !vendorEnforcesOfficialClient(vendor) && !forceOfficialClient {
		return false, ReasonVendorNotEnforced
	}
	required, has := RequiredIdentity(vendor)
	if !has {
		return false, ReasonVendorNoOfficial
	}
	if clientIdentity == required {
		return false, ReasonOfficialClientOK
	}
	return true, ReasonNonOfficialReject
}
