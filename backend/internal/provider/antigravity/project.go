package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/projectenrich"
	providergemini "github.com/BloomingProsperity/HUAKAI/internal/provider/gemini"
)

const (
	ProjectProfileAntigravity      = "antigravity"
	ProjectProfileGeminiCodeAssist = "gemini_code_assist"

	defaultAntigravityCloudCodeEndpoint = "https://cloudcode-pa.googleapis.com"
	defaultAntigravityDailyEndpoint     = "https://daily-cloudcode-pa.googleapis.com"
	defaultProjectPollAttempts          = 5
	defaultProjectPollInterval          = 250 * time.Millisecond
	maxProjectResponseBytes             = 1 << 20
)

var errAntigravityProjectMissing = errors.New("antigravity project: 上游未返回 project_id")

var errGeminiCodeAssistProjectMissing = errors.New("gemini code assist project: 上游未返回 project_id")

// ProjectResolver 通过 Cloud Code 初始化接口取得生成请求必需的 project_id。
// Endpoint 字段只供受控 wiring 与测试覆盖，不从凭据 payload 读取。
type ProjectResolver struct {
	Endpoint      string
	DailyEndpoint string
	HTTPClient    *http.Client
	PollAttempts  int
	// PollInterval 为零时使用温和默认间隔；负数仅供单元测试关闭等待。
	PollInterval time.Duration
}

// ResolveProjectID 保留只需要项目标识的旧合同。
func (r *ProjectResolver) ResolveProjectID(ctx context.Context, accessToken string) (string, error) {
	projectID, _, err := r.ResolveProjectMetadata(ctx, accessToken)
	return projectID, err
}

// ResolveProjectMetadata 先查询现有 Code Assist 配置，同时提取套餐层级；尚未分配
// project 时，转到 daily 端点执行 onboardUser，并在有限次数内轮询结果。
func (r *ProjectResolver) ResolveProjectMetadata(ctx context.Context, accessToken string) (string, string, error) {
	return r.ResolveProjectMetadataForProfile(ctx, ProjectProfileAntigravity, accessToken)
}

// ResolveProjectMetadataForProfile 按账号模式使用对应的 Cloud Code 初始化合同。
func (r *ProjectResolver) ResolveProjectMetadataForProfile(ctx context.Context, profile, accessToken string) (string, string, error) {
	return r.ResolveProjectMetadataForProfileAndProject(ctx, profile, accessToken, "")
}

// ResolveProjectMetadataForProfileAndProject 把部署者提供的项目标识纳入
// 上游验证与初始化，不会用其他账号或其他模式的项目代替。
func (r *ProjectResolver) ResolveProjectMetadataForProfileAndProject(ctx context.Context, profile, accessToken, projectRef string) (string, string, error) {
	accessToken = strings.TrimSpace(accessToken)
	projectRef = strings.TrimSpace(projectRef)
	if accessToken == "" {
		return "", "", errors.New("cloud code project: access_token 不能为空")
	}
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case ProjectProfileAntigravity:
		return r.resolveAntigravityProjectMetadata(ctx, accessToken)
	case ProjectProfileGeminiCodeAssist:
		return r.resolveGeminiCodeAssistProjectMetadata(ctx, accessToken, projectRef)
	default:
		return "", "", fmt.Errorf("cloud code project: 不支持的账号模式 %q", profile)
	}
}

func (r *ProjectResolver) resolveAntigravityProjectMetadata(ctx context.Context, accessToken string) (string, string, error) {
	loadBody := map[string]any{
		"metadata": map[string]string{"ideType": "ANTIGRAVITY"},
	}
	raw, err := r.post(ctx, r.primaryEndpoint(), "loadCodeAssist", accessToken, loadBody)
	if err != nil {
		return "", "", err
	}
	tier := subscriptionTierFromResponse(raw)
	if projectID := projectIDFromResponse(raw); projectID != "" {
		r.ensurePrivacyBestEffort(ctx, accessToken, projectID)
		return projectID, tier, nil
	}
	onboardBody := map[string]string{
		"ide_type":    "ANTIGRAVITY",
		"ide_version": antigravityIDEVersion,
		"ide_name":    "antigravity",
	}
	attempts := r.pollAttempts()
	for attempt := 0; attempt < attempts; attempt++ {
		raw, err = r.post(ctx, r.dailyEndpoint(), "onboardUser", accessToken, onboardBody)
		if err != nil {
			return "", tier, err
		}
		if observedTier := subscriptionTierFromResponse(raw); observedTier != "" {
			tier = observedTier
		}
		if projectID := projectIDFromResponse(raw); projectID != "" {
			r.ensurePrivacyBestEffort(ctx, accessToken, projectID)
			return projectID, tier, nil
		}
		if attempt+1 < attempts {
			if err := r.waitForNextPoll(ctx); err != nil {
				return "", tier, err
			}
		}
	}
	return "", tier, fmt.Errorf("%w: onboardUser 已轮询 %d 次", errAntigravityProjectMissing, attempts)
}

func (r *ProjectResolver) resolveGeminiCodeAssistProjectMetadata(ctx context.Context, accessToken, projectRef string) (string, string, error) {
	metadata := map[string]string{
		"ideType": "IDE_UNSPECIFIED", "platform": "PLATFORM_UNSPECIFIED", "pluginType": "GEMINI",
	}
	loadBody := map[string]any{"metadata": metadata}
	if projectRef != "" {
		metadata["duetProject"] = projectRef
		loadBody["cloudaicompanionProject"] = projectRef
	}
	raw, err := r.postForProfile(ctx, r.primaryEndpoint(), "loadCodeAssist", accessToken,
		loadBody, ProjectProfileGeminiCodeAssist)
	if err != nil {
		return "", "", err
	}
	tier := subscriptionTierFromResponse(raw)
	if projectID := projectIDFromResponse(raw); projectID != "" {
		return projectID, tier, nil
	}
	if currentTierPresent(raw) && projectRef != "" {
		return projectRef, tier, nil
	}
	onboardTier, requiresProject := onboardingTierContractFromResponse(raw)
	if onboardTier == "" {
		if projectRef == "" {
			return "", tier, fmt.Errorf("%w: 当前账号没有托管项目", projectenrich.ErrProjectInputRequired)
		}
		return "", tier, fmt.Errorf("%w: 当前账号需要明确的 Google Cloud 项目或没有可用套餐", errGeminiCodeAssistProjectMissing)
	}
	if requiresProject && projectRef == "" {
		return "", tier, fmt.Errorf("%w: 套餐 %s 要求自带 Google Cloud 项目", projectenrich.ErrProjectInputRequired, onboardTier)
	}
	if tier == "" {
		tier = onboardTier
	}
	onboardBody := map[string]any{"tierId": onboardTier, "metadata": metadata}
	if requiresProject {
		onboardBody["cloudaicompanionProject"] = projectRef
	}
	raw, err = r.postForProfile(ctx, r.primaryEndpoint(), "onboardUser", accessToken, onboardBody, ProjectProfileGeminiCodeAssist)
	if err != nil {
		return "", tier, err
	}
	if projectID := projectIDFromResponse(raw); projectID != "" {
		return projectID, tier, nil
	}
	if operationDone(raw) && projectRef != "" {
		return projectRef, tier, nil
	}
	operationName := operationNameFromResponse(raw)
	if operationName == "" {
		return "", tier, fmt.Errorf("%w: 初始化响应既没有项目也没有异步任务", errGeminiCodeAssistProjectMissing)
	}
	for attempt := 0; attempt < r.pollAttempts(); attempt++ {
		if err := r.waitForNextPoll(ctx); err != nil {
			return "", tier, err
		}
		raw, err = r.getOperation(ctx, r.primaryEndpoint(), operationName, accessToken, ProjectProfileGeminiCodeAssist)
		if err != nil {
			return "", tier, err
		}
		if observedTier := subscriptionTierFromResponse(raw); observedTier != "" {
			tier = observedTier
		}
		if projectID := projectIDFromResponse(raw); projectID != "" {
			return projectID, tier, nil
		}
		if operationDone(raw) {
			if projectRef != "" {
				return projectRef, tier, nil
			}
			break
		}
	}
	return "", tier, fmt.Errorf("%w: 异步初始化未返回项目", errGeminiCodeAssistProjectMissing)
}

func (r *ProjectResolver) post(ctx context.Context, base, action, accessToken string, payload any) ([]byte, error) {
	return r.postForProfile(ctx, base, action, accessToken, payload, ProjectProfileAntigravity)
}

func (r *ProjectResolver) postForProfile(ctx context.Context, base, action, accessToken string, payload any, profile string) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("antigravity project: %s 请求编码失败: %w", action, err)
	}
	endpoint := strings.TrimRight(strings.TrimSpace(base), "/") + "/v1internal:" + action
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("antigravity project: 构造 %s 请求失败: %w", action, err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	applyProjectHeaders(req.Header, profile)
	return r.do(req, action)
}

func (r *ProjectResolver) getOperation(ctx context.Context, base, name, accessToken, profile string) ([]byte, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(base), "/") + "/v1internal/" + strings.TrimLeft(strings.TrimSpace(name), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("cloud code project: 构造异步任务查询失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	applyProjectHeaders(req.Header, profile)
	return r.do(req, "getOperation")
}

func applyProjectHeaders(header http.Header, profile string) {
	if profile == ProjectProfileGeminiCodeAssist {
		providergemini.ApplyCodeAssistHeaders(header)
		return
	}
	ApplyCloudCodeHeaders(header)
}

func (r *ProjectResolver) do(req *http.Request, action string) ([]byte, error) {
	resp, err := r.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("antigravity project: %s 请求失败: %w", action, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxProjectResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("antigravity project: 读取 %s 响应失败: %w", action, err)
	}
	if len(raw) > maxProjectResponseBytes {
		return nil, fmt.Errorf("antigravity project: %s 响应超过 %d 字节", action, maxProjectResponseBytes)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("antigravity project: %s 返回 HTTP %d", action, resp.StatusCode)
	}
	return raw, nil
}

func (r *ProjectResolver) primaryEndpoint() string {
	if r != nil && strings.TrimSpace(r.Endpoint) != "" {
		return r.Endpoint
	}
	return defaultAntigravityCloudCodeEndpoint
}

func (r *ProjectResolver) dailyEndpoint() string {
	if r != nil && strings.TrimSpace(r.DailyEndpoint) != "" {
		return r.DailyEndpoint
	}
	return defaultAntigravityDailyEndpoint
}

func (r *ProjectResolver) httpClient() *http.Client {
	if r != nil && r.HTTPClient != nil {
		return r.HTTPClient
	}
	// 固定 Google 端点仍使用受保护客户端，避免代理环境外发 access token，
	// 并在拨号层拒绝解析漂移到私网或元数据地址。
	return auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
}

func (r *ProjectResolver) pollAttempts() int {
	if r != nil && r.PollAttempts > 0 {
		return r.PollAttempts
	}
	return defaultProjectPollAttempts
}

func (r *ProjectResolver) waitForNextPoll(ctx context.Context) error {
	interval := defaultProjectPollInterval
	if r != nil {
		if r.PollInterval < 0 {
			return nil
		}
		if r.PollInterval > 0 {
			interval = r.PollInterval
		}
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func projectIDFromResponse(raw []byte) string {
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		return ""
	}
	return projectIDFromNode(decoded)
}

func projectIDFromNode(node any) string {
	switch value := node.(type) {
	case map[string]any:
		for _, key := range []string{"cloudaicompanionProject", "project_id", "projectId"} {
			switch project := value[key].(type) {
			case string:
				if strings.TrimSpace(project) != "" {
					return strings.TrimSpace(project)
				}
			case map[string]any:
				if id, ok := project["id"].(string); ok && strings.TrimSpace(id) != "" {
					return strings.TrimSpace(id)
				}
			}
		}
		// onboardUser 可能把最终载荷置于异步操作 envelope 中，只沿已知
		// 容器字段下钻，避免把无关 metadata 的普通 id 误认成 project_id。
		for _, key := range []string{"response", "result", "operation"} {
			if nested, ok := value[key]; ok {
				if projectID := projectIDFromNode(nested); projectID != "" {
					return projectID
				}
			}
		}
	case []any:
		for _, item := range value {
			if projectID := projectIDFromNode(item); projectID != "" {
				return projectID
			}
		}
	}
	return ""
}

func subscriptionTierFromResponse(raw []byte) string {
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		return ""
	}
	return subscriptionTierFromNode(decoded)
}

func subscriptionTierFromNode(node any) string {
	switch value := node.(type) {
	case map[string]any:
		// 付费层级是更明确的订阅事实；没有时才回退当前运行层级。
		for _, key := range []string{"paidTier", "currentTier"} {
			if tier, ok := value[key].(map[string]any); ok {
				if id, ok := tier["id"].(string); ok && strings.TrimSpace(id) != "" {
					return strings.TrimSpace(id)
				}
			}
		}
		for _, key := range []string{"response", "result", "operation"} {
			if nested, ok := value[key]; ok {
				if tier := subscriptionTierFromNode(nested); tier != "" {
					return tier
				}
			}
		}
	case []any:
		for _, item := range value {
			if tier := subscriptionTierFromNode(item); tier != "" {
				return tier
			}
		}
	}
	return ""
}

func onboardingTierContractFromResponse(raw []byte) (string, bool) {
	var decoded map[string]any
	if json.Unmarshal(raw, &decoded) != nil {
		return "", false
	}
	tiers, _ := decoded["allowedTiers"].([]any)
	for _, item := range tiers {
		tier, _ := item.(map[string]any)
		isDefault, _ := tier["isDefault"].(bool)
		id, _ := tier["id"].(string)
		if isDefault && strings.TrimSpace(id) != "" {
			requiresProject, _ := tier["userDefinedCloudaicompanionProject"].(bool)
			return strings.TrimSpace(id), requiresProject
		}
	}
	return "", false
}

func currentTierPresent(raw []byte) bool {
	var decoded map[string]any
	if json.Unmarshal(raw, &decoded) != nil {
		return false
	}
	_, ok := decoded["currentTier"].(map[string]any)
	return ok
}

func operationNameFromResponse(raw []byte) string {
	var decoded map[string]any
	if json.Unmarshal(raw, &decoded) != nil {
		return ""
	}
	name, _ := decoded["name"].(string)
	return strings.TrimSpace(name)
}

func operationDone(raw []byte) bool {
	var decoded map[string]any
	if json.Unmarshal(raw, &decoded) != nil {
		return false
	}
	done, _ := decoded["done"].(bool)
	return done
}
