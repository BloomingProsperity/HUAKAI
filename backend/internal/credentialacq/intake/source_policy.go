package intake

import (
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

// SourceAllowedForMode 是账号接入来源的唯一策略入口。
// 通用导入继续服从模式计划；专用入口使用服务端固定矩阵，避免把迁移或转换结果
// 冒充成另一种交互式授权来源。
func SourceAllowedForMode(source SourceKind, vendor, authMode string) bool {
	vendor, authMode = credentialstore.CanonicalCredentialMode(vendor, authMode)
	switch source {
	case SourceCLI:
		return credentialacq.SourceAllowedForMode(vendor, authMode, credentialacq.FlowKindCLIImport)
	case SourceJSON:
		return credentialacq.SourceAllowedForMode(vendor, authMode, credentialacq.FlowKindJSONImport)
	case SourceCSV:
		return credentialacq.SourceAllowedForMode(vendor, authMode, credentialacq.FlowKindCSVImport)
	case SourceClaudeSetupToken:
		return vendor == credentialstore.VendorAnthropic &&
			authMode == credentialstore.AuthModeClaudeSetupToken
	case SourceClaudeCookie:
		return vendor == credentialstore.VendorAnthropic &&
			authMode == credentialstore.AuthModeClaudeAIOAuth
	case SourceClaudeSetupCookie:
		return vendor == credentialstore.VendorAnthropic &&
			authMode == credentialstore.AuthModeClaudeSetupToken
	case SourceCodexAgent:
		return vendor == credentialstore.VendorOpenAI &&
			authMode == credentialstore.AuthModeCodexAgent
	case SourceCRSSync:
		return crsModeAllowed(vendor, authMode)
	case SourceAccountBundle:
		// 账号迁移包是强权限、同租户的恢复入口。是否已发布以及是否有生产处理器，
		// 仍由计划主链的发布闸和处理器注册表统一判定。
		return true
	case SourceOAuth:
		return credentialacq.SourceAllowedForMode(vendor, authMode, credentialacq.FlowKindOAuth)
	default:
		return false
	}
}

func crsModeAllowed(vendor, authMode string) bool {
	switch vendor {
	case credentialstore.VendorAnthropic:
		return authMode == credentialstore.AuthModeAPIKey ||
			authMode == credentialstore.AuthModeClaudeAIOAuth ||
			authMode == credentialstore.AuthModeClaudeSetupToken
	case credentialstore.VendorOpenAI:
		return authMode == credentialstore.AuthModeAPIKey ||
			authMode == credentialstore.AuthModeChatGPTOAuth
	case credentialstore.VendorGemini:
		return authMode == credentialstore.AuthModeAIStudioAPIKey ||
			authMode == credentialstore.AuthModeCodeAssist
	default:
		return false
	}
}
