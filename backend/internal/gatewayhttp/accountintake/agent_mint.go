package accountintake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/codexagent"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

// 铸号时提交给上游的 agent 自描述:模仿官方 codex CLI 客户端形态。
// 取值集中在此,后续要跟随官方客户端演进时只改这里。
const (
	mintAgentVersion   = "0.145.0"
	mintAgentHarnessID = "codex-cli"
)

// isOpenAISessionMode 判断候选是否为可铸 Agent Identity 的 OpenAI 会话号。
// 只有持有 access_token 的会话模式可以就地铸身份;已是 Agent Identity 或普通 API Key 不在此列。
func isOpenAISessionMode(vendor, authMode string) bool {
	if credentialstore.Normalize(vendor) != credentialstore.VendorOpenAI {
		return false
	}
	switch credentialstore.Normalize(authMode) {
	case credentialstore.AuthModeChatGPTOAuth,
		credentialstore.AuthModeCodexCLIOAuth,
		credentialstore.AuthModeCodexWebOAuth:
		return true
	default:
		return false
	}
}

// effectiveAuthMode 返回候选最终会落库的凭据模式:请求铸号且确为可铸会话号时,
// 最终形态是 codex_agent_identity。预检必须按最终形态判协议兼容与混合渠道风险,
// 否则会出现「计划按会话号判无风险、不要求确认,写库按 Agent 身份判高风险、要求确认」
// 的前后不一致——而那时上游身份已经铸出去了。
func effectiveAuthMode(mintRequested bool, vendor, authMode string) string {
	if mintRequested && isOpenAISessionMode(vendor, authMode) {
		return credentialstore.AuthModeCodexAgent
	}
	return authMode
}

// mintedCandidate 是铸号编排的产物。
type mintedCandidate struct {
	// Candidate 是最终用于写库的候选(铸号后已完成 Agent 任务登记)。
	Candidate credentialacq.CredentialCandidate
	// PlanCandidate 是预检据以成案的那份候选(铸号前)，只用于事务内稳定性复核；
	// payload 为深拷贝：结构体拷贝共享同一底层数组，而铸号会把会话材料清零，
	// 共享的话复核只会看到一串零字节，直接被判成无效凭据。
	PlanCandidate credentialacq.CredentialCandidate
	// RuntimeID 非空表示上游已产生不可回滚的注册副作用。
	RuntimeID string
}

// mintForExecution 把「留存复核用候选 -> 按需铸号 -> 铸后重新准备」这段编排收在一处。
//
// 铸号会把候选从会话模式换成 Agent Identity，因此必须再走一次准备：上一轮准备是按会话
// 模式跑的，Agent 任务登记分支根本没触发。漏掉这一步会落库一条没有 task_id 的凭据，
// 账号看着导入成功、首次转发必然失败。
func (s *Service) mintForExecution(ctx context.Context, mintRequested bool, candidate credentialacq.CredentialCandidate) (mintedCandidate, error) {
	out := mintedCandidate{Candidate: candidate, PlanCandidate: candidate}
	out.PlanCandidate.Payload = append([]byte(nil), candidate.Payload...)

	minted, runtimeID, err := s.applyAgentIdentityMint(ctx, mintRequested, candidate)
	out.Candidate, out.RuntimeID = minted, runtimeID
	if err != nil {
		return out, err
	}
	if runtimeID == "" {
		return out, nil
	}
	prepared, err := s.prepareExecutionCandidate(ctx, out.Candidate)
	if err != nil {
		// 保留铸号后的候选身份再返回:prepareExecutionCandidate 失败时会吐零值候选，
		// 直接覆盖会让失败分类看不出这已是 Agent Identity，把「任务登记失败」误报成
		// 笼统的「元数据准备失败」。而这条分支恰恰是上游已留下孤儿 runtime 的场景，
		// 运营最需要从错误码看出卡在哪一步。
		return out, err
	}
	out.Candidate = prepared
	return out, nil
}

// applyAgentIdentityMint 在导入显式请求铸号且候选确为可铸会话号时，把它就地铸成
// Agent Identity 候选，之后完全复用下游 EnsureTask 与转发链路。默认关，不改既有会话
// 导入行为。返回值第二项是铸出的 runtime id：铸号不可回滚，调用方失败时要靠它把上游
// 遗留的孤儿暴露给运营。
func (s *Service) applyAgentIdentityMint(ctx context.Context, mintRequested bool, candidate credentialacq.CredentialCandidate) (credentialacq.CredentialCandidate, string, error) {
	if !mintRequested || !isOpenAISessionMode(candidate.Vendor, candidate.AuthMode) {
		return candidate, "", nil
	}
	minted, runtimeID, err := s.mintAgentIdentityFromSession(ctx, candidate)
	if err != nil {
		return candidate, runtimeID, err
	}
	candidate.Payload = minted
	candidate.AuthMode = credentialstore.AuthModeCodexAgent
	return candidate, runtimeID, nil
}

// mintAgentIdentityFromSession 用会话号的 access_token 就地铸一个 Agent Identity,
// 返回转换后的 codex_agent_identity 材料与铸出的 runtime_id。私钥只留在返回的材料里,
// access_token 不落日志。
//
// 注册是不可回滚的上游副作用:一旦 RegisterRuntime 成功,对方就多了一个 runtime,
// 而后续任何一步失败都无法把它收回。因此注册成功后立刻打一条带 runtime_id 的日志,
// 并把 id 回传给调用方——失败时它会进结果告警,运营据此清理孤儿,而不是只能靠猜。
func (s *Service) mintAgentIdentityFromSession(ctx context.Context, candidate credentialacq.CredentialCandidate) ([]byte, string, error) {
	if s == nil || s.agentRegistrar == nil {
		return nil, "", ErrAgentIdentityMintUnavailable
	}
	// 无论成败都要清零本函数看到的会话材料:凭据刷新会产出新的 payload 切片,
	// 它不在调用方 defer 清零的覆盖面内,失败直接返回会把新鲜 access_token 留在内存。
	defer privacy.Zeroize(candidate.Payload)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(candidate.Payload, &fields); err != nil || fields == nil {
		return nil, "", fmt.Errorf("%w: 会话材料不是 JSON 对象", ErrAgentIdentityMintFailed)
	}
	accessToken := jsonStringField(fields, "access_token")
	if accessToken == "" {
		accessToken = jsonStringField(fields, "session_token")
	}
	if accessToken == "" {
		return nil, "", fmt.Errorf("%w: 会话缺少 access_token", ErrAgentIdentityMintFailed)
	}
	// 身份字段优先取会话 payload,回落到 token 交换时解出的候选字段。
	chatgptUserID := firstNonEmptyString(jsonStringField(fields, "chatgpt_user_id"), candidate.ExternalSubjectID)
	accountID := firstNonEmptyString(
		jsonStringField(fields, "chatgpt_account_id"),
		jsonStringField(fields, "account_id"),
		candidate.ExternalAccountID,
	)
	if chatgptUserID == "" || accountID == "" {
		return nil, "", fmt.Errorf("%w: 会话缺少 chatgpt_user_id 或 account_id", ErrAgentIdentityMintFailed)
	}

	keyMaterial, err := codexagent.GenerateKeyMaterial()
	if err != nil {
		return nil, "", errors.Join(ErrAgentIdentityMintFailed, err)
	}
	runtimeID, err := s.agentRegistrar.RegisterRuntime(ctx, codexagent.RegisterRuntimeInput{
		AccessToken:  accessToken,
		PublicKeySSH: keyMaterial.PublicKeySSH,
		ABOM: codexagent.AgentBillOfMaterials{
			AgentVersion:    mintAgentVersion,
			AgentHarnessID:  mintAgentHarnessID,
			RunningLocation: fmt.Sprintf("cli-%s", runtime.GOOS),
		},
		Capabilities: []string{"responsesapi"},
	})
	if err != nil {
		// 注册失败=上游没建成东西,无孤儿可清;密钥对随函数返回被丢弃。
		return nil, "", errors.Join(ErrAgentIdentityMintFailed, err)
	}
	// 上游已产生不可回滚副作用,先留痕再继续:后面任何一步失败,运营都能凭这条日志
	// 找到该清理的 runtime。
	slog.InfoContext(ctx, "会话号已铸出 Agent Identity",
		"event_type", "account_intake.agent_identity_minted",
		"target_type", "agent_runtime",
		"target_ref", runtimeID,
		"chatgpt_account_id", accountID,
		"vendor", candidate.Vendor,
		"source_auth_mode", candidate.AuthMode,
	)

	// 铸成的 Agent Identity 材料形状与专用导入格式一致,后续完全复用 EnsureTask 与转发链路。
	minted := map[string]any{
		"auth_mode":          "codex_agent_identity",
		"agent_runtime_id":   runtimeID,
		"agent_private_key":  keyMaterial.PrivateKeyPKCS8Base64,
		"account_id":         accountID,
		"chatgpt_account_id": accountID,
		"chatgpt_user_id":    chatgptUserID,
	}
	// 保留可透传的运行时元数据,便于出站指纹一致。
	for _, key := range []string{"originator", "oai_device_id", "user_agent"} {
		if value := jsonStringField(fields, key); value != "" {
			minted[key] = value
		}
	}
	out, err := json.Marshal(minted)
	if err != nil {
		return nil, runtimeID, fmt.Errorf("%w: 无法生成 Agent Identity 材料", ErrAgentIdentityMintFailed)
	}
	return out, runtimeID, nil
}

func jsonStringField(fields map[string]json.RawMessage, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// appendOrphanRuntimeWarning 在铸号已成功但本项最终没能落库时，把上游遗留的
// runtime id 追加进结果告警。铸号不可回滚，运营需要据此手工清理孤儿。
func appendOrphanRuntimeWarning(warnings []string, runtimeID string) []string {
	runtimeID = strings.TrimSpace(runtimeID)
	if runtimeID == "" {
		return warnings
	}
	return append(warnings, "agent_identity_orphan_runtime:"+runtimeID)
}
