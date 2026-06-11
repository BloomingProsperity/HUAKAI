// cancel.go — Replicate prediction 取消请求的纯构造(不发送)。
//
// Prefer: wait 超窗(status=starting/processing)时上游 prediction 仍在跑且按
// 产出向平台计费;本侧 abort 给用户退款后若不取消上游任务,平台单边吃成本,
// 客户端重试每轮还会再开新 prediction 叠加烧钱。构造与发送分离:adapter 契约
// 禁止 adapter 内发子请求,发送由调用 lane(imageshttp)以 best-effort 语义做,
// 取消失败绝不阻断 abort 主路径。
package replicate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// defaultCancelEndpointTemplate 是 prediction 取消端点模板;{id} 由 PathEscape
// 后的 prediction id 替换。passthrough base_url 覆盖与 SSRF 守卫走
// EndpointForCredential 统一通道,与 BuildRequest 同口径。
const defaultCancelEndpointTemplate = "https://api.replicate.com/v1/predictions/{id}/cancel"

// PredictionMeta 是取消/审计所需的最小 prediction 元数据。
type PredictionMeta struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// PredictionMetaFromResponse 从 prediction JSON 提取 id/status。解析失败返回
// 零值——调用方按「无可取消项」处理,绝不因审计提取失败阻断 abort 主路径。
func PredictionMetaFromResponse(raw []byte) PredictionMeta {
	var meta PredictionMeta
	_ = json.Unmarshal(raw, &meta)
	meta.ID = strings.TrimSpace(meta.ID)
	meta.Status = strings.TrimSpace(meta.Status)
	return meta
}

// CancelWorthwhile 报告该 prediction 状态是否值得发 cancel:仅非终态(starting/
// processing,即 Prefer: wait 超窗形态)需要;failed/canceled/succeeded 已终止,
// 再发只是徒增上游调用。未知/缺失状态保守发——宁多一次幂等 cancel,不留计费泄漏。
func CancelWorthwhile(status string) bool {
	switch status {
	case "failed", "canceled", "succeeded":
		return false
	}
	return true
}

// predictionIDPattern 白名单校验 prediction id(上游 id 是 URL-safe token)。
// 不用逐字符 escape:EndpointForCredential 对 passthrough base_url 会重组 URL
// (url.Parse 解码 %2F 再 String() 重编码,转义斜杠塌缩),escape 不可依赖;
// 白名单一次根除路径段注入。
var predictionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// NewCancelRequest 构造 POST predictions/{id}/cancel 请求。endpoint 与鉴权都
// 复用 BuildRequest 的同一口径(EndpointForCredential + applyCredentialAuth),
// 自托管/代理 base_url 凭据照常生效。注意:本函数只做静态构造;发送方必须再做
// 运行时守卫(passthrough 凭据 ValidatePassthroughEndpointTarget + dial 时刻
// wrap),与主出站 dispatcher 同口径。
func NewCancelRequest(ctx context.Context, cred provider.Credential, predictionID string) (*http.Request, error) {
	id := strings.TrimSpace(predictionID)
	if id == "" {
		return nil, errors.New("replicate: prediction id 为空,无可取消项")
	}
	if !predictionIDPattern.MatchString(id) {
		return nil, fmt.Errorf("replicate: prediction id %q 含非法字符,拒绝构造 cancel", id)
	}
	if cred.Value == "" {
		return nil, errors.New("replicate: 凭据 Value 为空")
	}
	substituted := strings.ReplaceAll(defaultCancelEndpointTemplate, "{id}", id)
	endpoint, err := provider.EndpointForCredential(substituted, cred)
	if err != nil {
		return nil, fmt.Errorf("replicate: cancel endpoint rejected: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("replicate: 构造 cancel 请求失败: %w", err)
	}
	applyCredentialAuth(req, cred)
	req.Header.Set("Accept", "application/json")
	return req, nil
}
