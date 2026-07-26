package codexagent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// 铸身份端点相对 DefaultTaskServiceURL(https://auth.openai.com/api/accounts)。
// 与 task 登记 /v1/agent/{runtime_id}/task/register 是同族的兄弟端点:
// 前者用有效会话换 agent_runtime_id,后者用已铸身份换单次运行的 task_id。
const (
	agentRegisterPath       = "/v1/agent/register"
	agentRegisterTimeout    = 30 * time.Second
	fedRAMPRegistrationFlag = "X-OpenAI-Fedramp"
	// 官方 codex 客户端每个请求都带 originator 头,服务端据此判定第一方来源(见其
	// is_first_party_originator 白名单);缺失会被拒为 agent_registry_not_enabled。
	codexOriginator     = "codex_cli_rs"
	defaultCodexVersion = "0.145.0"
)

// GeneratedKeyMaterial 是一对新鲜的 Agent Identity 密钥:私钥留本地,公钥交上游注册。
type GeneratedKeyMaterial struct {
	// PrivateKeyPKCS8Base64 是 Ed25519 私钥的 PKCS#8 DER 标准 Base64;
	// 形状与导入路径 parsePrivateKey 期望的一致,可直接落成 agent_private_key。
	PrivateKeyPKCS8Base64 string
	// PublicKeySSH 是 "ssh-ed25519 <base64>" 形式,注册请求要的公钥编码。
	PublicKeySSH string
}

// AgentBillOfMaterials 是注册请求要求的 agent 自描述(abom),决定这次注册"看起来像哪种客户端"。
// 取值由接线层按当前要仿真的官方客户端形态提供,不在本原语里写死版本。
type AgentBillOfMaterials struct {
	AgentVersion    string `json:"agent_version"`
	AgentHarnessID  string `json:"agent_harness_id"`
	RunningLocation string `json:"running_location"`
}

// RegisterRuntimeInput 是铸一个 agent 身份所需的全部输入。
type RegisterRuntimeInput struct {
	// AccessToken 是持有登录态的 ChatGPT 会话 access_token,作为 Bearer 鉴权;绝不落日志。
	AccessToken string
	// PublicKeySSH 是 GenerateKeyMaterial 产出的 ssh-ed25519 公钥。
	PublicKeySSH string
	// ABOM 是 agent 自描述。
	ABOM AgentBillOfMaterials
	// Capabilities 是申请的能力集合,可空。
	Capabilities []string
	// IsFedRAMP 为真时补 fedramp 专用请求头。
	IsFedRAMP bool
}

// GenerateKeyMaterial 生成一对全新的 Ed25519 Agent Identity 密钥。
// 私钥永不出站,只有公钥随注册请求提交;调用方负责安全保存返回的私钥。
func GenerateKeyMaterial() (GeneratedKeyMaterial, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return GeneratedKeyMaterial{}, fmt.Errorf("%w: 无法生成 Ed25519 密钥", ErrInvalidMaterial)
	}
	defer wipe(private)
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return GeneratedKeyMaterial{}, fmt.Errorf("%w: 无法编码 PKCS#8 私钥", ErrInvalidMaterial)
	}
	defer wipe(der)
	sshPublic, err := ssh.NewPublicKey(public)
	if err != nil {
		return GeneratedKeyMaterial{}, fmt.Errorf("%w: 无法编码 SSH 公钥", ErrInvalidMaterial)
	}
	// MarshalAuthorizedKey 产出 "ssh-ed25519 <base64>\n"(无注释),去掉尾部换行即上游要的格式。
	return GeneratedKeyMaterial{
		PrivateKeyPKCS8Base64: base64.StdEncoding.EncodeToString(der),
		PublicKeySSH:          strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublic))),
	}, nil
}

// RuntimeRegistrar 用一个有效 ChatGPT access_token 向官方固定端点铸造 agent runtime 身份。
// 与 TaskBroker 一样,只打部署代码注入的固定服务,不读凭据里的目标地址。
type RuntimeRegistrar struct {
	client  *http.Client
	baseURL string
}

// NewRuntimeRegistrar 创建使用官方固定注册服务的铸身份器。
func NewRuntimeRegistrar(client *http.Client) *RuntimeRegistrar {
	if client == nil {
		client = &http.Client{Timeout: agentRegisterTimeout}
	}
	clone := *client
	if clone.Timeout <= 0 || clone.Timeout > agentRegisterTimeout {
		clone.Timeout = agentRegisterTimeout
	}
	clone.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &RuntimeRegistrar{client: &clone, baseURL: DefaultTaskServiceURL}
}

// RegisterRuntime 提交公钥换取 agent_runtime_id。access_token 只进 Authorization 头,不落日志。
func (r *RuntimeRegistrar) RegisterRuntime(ctx context.Context, in RegisterRuntimeInput) (string, error) {
	if r == nil || r.client == nil || strings.TrimSpace(r.baseURL) == "" {
		return "", fmt.Errorf("%w: 铸身份登记器未配置", ErrTaskUnavailable)
	}
	if strings.TrimSpace(in.AccessToken) == "" {
		return "", fmt.Errorf("%w: access_token 缺失", ErrInvalidMaterial)
	}
	if strings.TrimSpace(in.PublicKeySSH) == "" {
		return "", fmt.Errorf("%w: 公钥缺失", ErrInvalidMaterial)
	}
	body, err := json.Marshal(struct {
		ABOM         AgentBillOfMaterials `json:"abom"`
		PublicKey    string               `json:"agent_public_key"`
		Capabilities []string             `json:"capabilities"`
		TTL          *uint64              `json:"ttl"`
	}{ABOM: in.ABOM, PublicKey: in.PublicKeySSH, Capabilities: capabilitiesOrEmpty(in.Capabilities), TTL: nil})
	if err != nil {
		return "", fmt.Errorf("%w: 无法生成注册载荷", ErrInvalidMaterial)
	}
	endpoint := strings.TrimRight(r.baseURL, "/") + agentRegisterPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("%w: 无法创建注册请求", ErrTaskUnavailable)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(in.AccessToken))
	// 以第一方 codex 客户端身份出站,否则服务端拒发 agent registry。
	req.Header.Set("originator", codexOriginator)
	req.Header.Set("User-Agent", codexUserAgent(in.ABOM.AgentVersion))
	if in.IsFedRAMP {
		req.Header.Set(fedRAMPRegistrationFlag, "true")
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: 注册请求失败", ErrTaskUnavailable)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("%w: 注册服务返回状态 %d", ErrTaskUnavailable, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return "", fmt.Errorf("%w: 注册服务响应无效", ErrTaskUnavailable)
	}
	var result struct {
		RuntimeSnake string `json:"agent_runtime_id"`
		RuntimeCamel string `json:"agentRuntimeId"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("%w: 注册服务响应不是有效 JSON", ErrTaskUnavailable)
	}
	runtimeID := firstNonEmpty(result.RuntimeSnake, result.RuntimeCamel)
	if err := validateRuntimeID(runtimeID); err != nil {
		return "", err
	}
	return runtimeID, nil
}

// codexUserAgent 拼出与官方 codex CLI 同形态的 User-Agent:originator/版本 (os; arch)。
func codexUserAgent(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		version = defaultCodexVersion
	}
	return fmt.Sprintf("%s/%s (%s; %s)", codexOriginator, version, runtime.GOOS, runtime.GOARCH)
}

func capabilitiesOrEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
