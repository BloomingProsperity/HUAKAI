package accountintake

// 候选准备阶段的失败分类：把凭据刷新、项目身份补全、Agent Identity 铸号与任务登记
// 各自的失败翻译成稳定的状态码与人话，供导入结果直接呈现给运营。

import (
	"errors"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/projectenrich"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func preparationFailure(candidate credentialacq.CredentialCandidate, err error) (ExecutionStatus, string, string) {
	switch {
	case errors.Is(err, ErrImportCredentialRefreshUnavailable):
		return StatusFailed, "credential_refresh_unavailable", "账号凭据已经过期，但导入刷新器不可用，账号未写入"
	case errors.Is(err, ErrImportCredentialRefreshFailed):
		return StatusFailed, "credential_refresh_failed", "账号凭据已经过期且刷新失败，账号未写入"
	case errors.Is(err, projectenrich.ErrProjectMetadataConflict):
		return StatusConflict, "project_metadata_conflict", "账号项目身份与上游识别结果冲突，需要人工消歧"
	case errors.Is(err, projectenrich.ErrProjectInputRequired):
		return StatusFailed, "project_id_required", "当前套餐要求部署者提供 Google Cloud project_id，账号未写入"
	case errors.Is(err, projectenrich.ErrProjectMetadataUnavailable):
		return StatusFailed, "project_metadata_unavailable", "账号项目身份无法确认，账号未写入"
	case errors.Is(err, ErrAgentIdentityMintUnavailable):
		return StatusFailed, "agent_identity_mint_unavailable", "请求从会话铸 Agent Identity，但铸号器不可用，账号未写入"
	case errors.Is(err, ErrAgentIdentityMintFailed):
		return StatusFailed, "agent_identity_mint_failed", "从会话铸 Agent Identity 失败，账号未写入"
	case credentialstore.Normalize(candidate.Vendor) == credentialstore.VendorOpenAI &&
		credentialstore.Normalize(candidate.AuthMode) == credentialstore.AuthModeCodexAgent:
		return StatusFailed, "agent_task_registration_failed", "Agent Identity 任务登记失败，账号未写入"
	default:
		return StatusFailed, "account_metadata_preparation_failed", "账号元数据准备失败，账号未写入"
	}
}
