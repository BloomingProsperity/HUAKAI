package proto

import "encoding/json"

// ApprovalState 标记 computer_use action 的审批状态。
type ApprovalState string

const (
	ApprovalRequired    ApprovalState = "required"
	ApprovalGranted     ApprovalState = "granted"
	ApprovalDenied      ApprovalState = "denied"
	ApprovalNotRequired ApprovalState = "not_required"
)

// ComputerUseNode 是 computer_use capability 的 payload。
type ComputerUseNode struct {
	// Environment 必填；browser/desktop/shell/mobile/other。
	Environment string `json:"environment"`

	// Action 必填；provider-native action 名或 HUAKAI normalized action。
	Action string `json:"action"`

	// Input 可选；action 参数 JSON。
	Input json.RawMessage `json:"input,omitempty"`

	// ScreenshotRef 可选；指向 image/file node id。
	ScreenshotRef string `json:"screenshot_ref,omitempty"`

	// Approval 必填；required/granted/denied/not_required。
	Approval ApprovalState `json:"approval"`

	// AuditLabel 可选；native/computer-use 操作审计标签。
	AuditLabel string `json:"audit_label,omitempty"`
}
