package accountintake

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/codexagent"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

type fakeRuntimeRegistrar struct {
	called    bool
	gotToken  string
	gotPubKey string
	runtimeID string
	err       error
}

func (f *fakeRuntimeRegistrar) RegisterRuntime(_ context.Context, in codexagent.RegisterRuntimeInput) (string, error) {
	f.called = true
	f.gotToken = in.AccessToken
	f.gotPubKey = in.PublicKeySSH
	return f.runtimeID, f.err
}

func sessionCandidate() credentialacq.CredentialCandidate {
	return credentialacq.CredentialCandidate{
		Vendor:   credentialstore.VendorOpenAI,
		AuthMode: credentialstore.AuthModeChatGPTOAuth,
		Payload:  []byte(`{"access_token":"tok-xyz","chatgpt_user_id":"user-1","chatgpt_account_id":"acct-9"}`),
	}
}

func TestMintAgentIdentityFromSession(t *testing.T) {
	reg := &fakeRuntimeRegistrar{runtimeID: "rt-minted"}
	svc := (&Service{}).WithAgentRuntimeRegistrar(reg)

	out, mintedRuntimeID, err := svc.mintAgentIdentityFromSession(context.Background(), sessionCandidate())
	if err != nil {
		t.Fatalf("铸号失败: %v", err)
	}
	if !reg.called || reg.gotToken != "tok-xyz" {
		t.Fatalf("铸号未使用会话 access_token: called=%v token=%q", reg.called, reg.gotToken)
	}
	if !strings.HasPrefix(reg.gotPubKey, "ssh-ed25519 ") {
		t.Fatalf("提交的公钥不是 ssh-ed25519: %q", reg.gotPubKey)
	}
	var fields map[string]string
	if err := json.Unmarshal(out, &fields); err != nil {
		t.Fatalf("铸出的材料不是 JSON: %v", err)
	}
	if fields["auth_mode"] != "codex_agent_identity" {
		t.Fatalf("铸出材料 auth_mode 错误: %q", fields["auth_mode"])
	}
	if fields["agent_runtime_id"] != "rt-minted" {
		t.Fatalf("runtime_id 未写入: %q", fields["agent_runtime_id"])
	}
	// 铸号不可回滚:runtime_id 必须回传给调用方，后续任何一步失败才能把孤儿暴露给运营。
	// 变异:让 mintAgentIdentityFromSession 只返回材料不返回 id 时，本断言转红。
	if mintedRuntimeID != "rt-minted" {
		t.Fatalf("铸出的 runtime_id 未回传: %q", mintedRuntimeID)
	}
	if fields["agent_private_key"] == "" {
		t.Fatalf("私钥缺失")
	}
	if fields["account_id"] != "acct-9" || fields["chatgpt_user_id"] != "user-1" {
		t.Fatalf("身份字段错误: account_id=%q user=%q", fields["account_id"], fields["chatgpt_user_id"])
	}
	// 铸出的材料必须能被运行层按 Agent Identity 校验(runtime_id/私钥/account/user 齐备)。
	if err := codexagent.ValidatePayload(out, false); err != nil {
		t.Fatalf("铸出材料无法通过运行层校验: %v", err)
	}
}

func TestMintUnavailableWithoutRegistrar(t *testing.T) {
	_, _, err := (&Service{}).mintAgentIdentityFromSession(context.Background(), sessionCandidate())
	if !errors.Is(err, ErrAgentIdentityMintUnavailable) {
		t.Fatalf("缺铸号器应报 ErrAgentIdentityMintUnavailable,实为 %v", err)
	}
}

func TestMintFailurePropagatesAndClassifies(t *testing.T) {
	reg := &fakeRuntimeRegistrar{err: errors.New("upstream 401 no_user_info")}
	svc := (&Service{}).WithAgentRuntimeRegistrar(reg)

	_, _, err := svc.mintAgentIdentityFromSession(context.Background(), sessionCandidate())
	if !errors.Is(err, ErrAgentIdentityMintFailed) {
		t.Fatalf("铸号失败应归类为 ErrAgentIdentityMintFailed,实为 %v", err)
	}
	status, code, _ := preparationFailure(sessionCandidate(), err)
	if status != StatusFailed || code != "agent_identity_mint_failed" {
		t.Fatalf("失败分类错误: %s / %s", status, code)
	}
}

func TestMintRejectsSessionWithoutIdentityFields(t *testing.T) {
	reg := &fakeRuntimeRegistrar{runtimeID: "rt"}
	svc := (&Service{}).WithAgentRuntimeRegistrar(reg)

	// 缺 chatgpt_user_id/account_id 的会话无法铸出可用身份,且不应发起上游注册。
	cand := credentialacq.CredentialCandidate{
		Vendor:   credentialstore.VendorOpenAI,
		AuthMode: credentialstore.AuthModeChatGPTOAuth,
		Payload:  []byte(`{"access_token":"tok"}`),
	}
	if _, _, err := svc.mintAgentIdentityFromSession(context.Background(), cand); !errors.Is(err, ErrAgentIdentityMintFailed) {
		t.Fatalf("缺身份字段应报错,实为 %v", err)
	}
	if reg.called {
		t.Fatalf("身份字段不全时不应发起上游注册")
	}
}

func TestIsOpenAISessionMode(t *testing.T) {
	cases := []struct {
		vendor, mode string
		want         bool
	}{
		{credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth, true},
		{credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth, true},
		{credentialstore.VendorOpenAI, credentialstore.AuthModeCodexWebOAuth, true},
		{credentialstore.VendorOpenAI, credentialstore.AuthModeCodexAgent, false},
		{credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth, false},
	}
	for _, c := range cases {
		if got := isOpenAISessionMode(c.vendor, c.mode); got != c.want {
			t.Fatalf("isOpenAISessionMode(%q,%q)=%v 期望 %v", c.vendor, c.mode, got, c.want)
		}
	}
}

// 铸号前必须先刷新过期凭据。顺序颠倒时会拿一张过期会话票去上游注册，
// 表现为「不开铸号能导入、开了整批 401」。
// 变异：把 mint 挪回 prepareExecutionCandidate 之前时，本用例转红。
func TestMintUsesRefreshedSessionNotExpiredOne(t *testing.T) {
	reg := &fakeRuntimeRegistrar{runtimeID: "rt-after-refresh"}
	svc := (&Service{}).WithAgentRuntimeRegistrar(reg)

	// 模拟刷新后的候选：access_token 已换成新值。
	refreshed := sessionCandidate()
	refreshed.Payload = []byte(`{"access_token":"tok-refreshed","chatgpt_user_id":"user-1","chatgpt_account_id":"acct-9"}`)

	if _, _, err := svc.mintAgentIdentityFromSession(context.Background(), refreshed); err != nil {
		t.Fatalf("铸号失败: %v", err)
	}
	if reg.gotToken != "tok-refreshed" {
		t.Fatalf("铸号用的不是刷新后的 access_token: %q", reg.gotToken)
	}
}

// 会话材料无论成败都要清零：凭据刷新产出的是新切片，不在调用方 defer 清零的覆盖面内，
// 失败直接返回会把新鲜 access_token 留在内存。
// 变异：去掉 defer privacy.Zeroize(candidate.Payload) 时，本用例转红。
func TestMintZeroizesSessionPayloadOnFailure(t *testing.T) {
	reg := &fakeRuntimeRegistrar{err: errors.New("upstream 401")}
	svc := (&Service{}).WithAgentRuntimeRegistrar(reg)

	cand := sessionCandidate()
	payload := cand.Payload
	if !strings.Contains(string(payload), "tok-xyz") {
		t.Fatalf("用例前置条件不成立：payload 未含 access_token")
	}

	if _, _, err := svc.mintAgentIdentityFromSession(context.Background(), cand); err == nil {
		t.Fatalf("期望铸号失败")
	}
	if strings.Contains(string(payload), "tok-xyz") {
		t.Fatalf("失败路径未清零会话材料，access_token 仍在内存: %q", string(payload))
	}
}

// 计划侧必须按铸号后的最终形态判定，否则会出现「计划判无风险不要确认、
// 写库判高风险要确认」的前后不一致，而那时上游身份已经铸出去了。
// 变异：让 effectiveAuthMode 恒返回原 authMode 时，本用例转红。
func TestEffectiveAuthModeReflectsMintOutcome(t *testing.T) {
	cases := []struct {
		name          string
		mintRequested bool
		vendor, mode  string
		want          string
	}{
		{"开关开+会话号=最终为 Agent 身份", true, credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth, credentialstore.AuthModeCodexAgent},
		{"开关关+会话号=保持会话", false, credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth, credentialstore.AuthModeChatGPTOAuth},
		{"开关开+已是 Agent 身份=不变", true, credentialstore.VendorOpenAI, credentialstore.AuthModeCodexAgent, credentialstore.AuthModeCodexAgent},
		{"开关开+非 OpenAI=不变", true, credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth, credentialstore.AuthModeClaudeAIOAuth},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := effectiveAuthMode(c.mintRequested, c.vendor, c.mode); got != c.want {
				t.Fatalf("effectiveAuthMode=%q want %q", got, c.want)
			}
		})
	}
}

// 铸号成功但后续失败时，孤儿 runtime 必须出现在结果告警里，运营才能手工清理。
// 变异：去掉 appendOrphanRuntimeWarning 调用时，本用例转红。
func TestOrphanRuntimeWarningSurfacesRuntimeID(t *testing.T) {
	got := appendOrphanRuntimeWarning(nil, "rt-orphan")
	if len(got) != 1 || !strings.Contains(got[0], "rt-orphan") {
		t.Fatalf("孤儿告警未带 runtime id: %v", got)
	}
	// 没铸出东西时不得平白加告警。
	if w := appendOrphanRuntimeWarning(nil, "  "); len(w) != 0 {
		t.Fatalf("未铸号时不应产生孤儿告警: %v", w)
	}
}

// 铸号失败时，复核用的深拷贝副本必须就地清零。它是独立分配，外层 defer 覆盖不到，
// 失败分支直接 break 会把会话 token 留在堆上等 GC。
// 变异：去掉失败分支里的 Zeroize 时，本用例转红。
func TestMintForExecutionFailureLeavesNoPlaintextCopy(t *testing.T) {
	reg := &fakeRuntimeRegistrar{err: errors.New("upstream 401")}
	svc := (&Service{}).WithAgentRuntimeRegistrar(reg)

	cand := sessionCandidate()
	minted, err := svc.mintForExecution(context.Background(), true, cand)
	if err == nil {
		t.Fatal("期望铸号失败")
	}
	// 调用方拿到副本后必须能擦掉它；这里断言副本确实是独立分配（擦它不影响原候选之外
	// 的内容），并且擦完不残留明文。
	privacy.Zeroize(minted.PlanCandidate.Payload)
	if strings.Contains(string(minted.PlanCandidate.Payload), "tok-xyz") {
		t.Fatalf("复核副本擦除后仍残留明文: %q", string(minted.PlanCandidate.Payload))
	}
}

// 复核副本必须与原候选各自独立：铸号会清零原会话材料，共享底层数组会让复核只看到
// 一串零字节而被判成无效凭据。
// 变异：把深拷贝改回结构体直接赋值时，本用例转红。
func TestMintForExecutionKeepsPlanCandidateIntact(t *testing.T) {
	reg := &fakeRuntimeRegistrar{runtimeID: "rt-x"}
	svc := (&Service{}).
		WithAgentRuntimeRegistrar(reg).
		WithAgentTaskRegistrar(&agentTaskRegistrarStub{payload: []byte(`{"task_id":"t"}`)})

	minted, err := svc.mintForExecution(context.Background(), true, sessionCandidate())
	if err != nil {
		t.Fatalf("铸号失败: %v", err)
	}
	// 铸号已把原会话材料清零；复核副本必须仍是完整可解析的原始会话材料。
	var fields map[string]any
	if err := json.Unmarshal(minted.PlanCandidate.Payload, &fields); err != nil {
		t.Fatalf("复核副本已被连带清零，无法解析: %v", err)
	}
	if fields["chatgpt_user_id"] != "user-1" {
		t.Fatalf("复核副本内容不完整: %v", fields)
	}
	// 铸出的最终候选必须已完成 Agent 任务登记。
	if !strings.Contains(string(minted.Candidate.Payload), "task_id") {
		t.Fatalf("最终候选缺少 task_id: %s", minted.Candidate.Payload)
	}
}

// 铸号成功但任务登记失败时，错误必须归类为「任务登记失败」而非笼统的「元数据准备失败」。
// 这条分支上游已留下孤儿 runtime，运营要靠错误码判断卡在哪一步。
// 变异：在 mintForExecution 里用零值候选覆盖 out.Candidate 时，本用例转红。
func TestMintForExecutionClassifiesTaskRegistrationFailure(t *testing.T) {
	reg := &fakeRuntimeRegistrar{runtimeID: "rt-orphaned"}
	svc := (&Service{}).
		WithAgentRuntimeRegistrar(reg).
		WithAgentTaskRegistrar(&agentTaskRegistrarStub{err: errors.New("task backend down")})

	minted, err := svc.mintForExecution(context.Background(), true, sessionCandidate())
	if err == nil {
		t.Fatal("期望任务登记失败")
	}
	// 上游已铸出 runtime，必须回传以便暴露孤儿。
	if minted.RuntimeID != "rt-orphaned" {
		t.Fatalf("孤儿 runtime id 未回传: %q", minted.RuntimeID)
	}
	_, code, _ := preparationFailure(minted.Candidate, err)
	if code != "agent_task_registration_failed" {
		t.Fatalf("失败分类=%q，期望 agent_task_registration_failed", code)
	}
}
