package mimicryidentity

// 本文件是 R7 身份改写接进 dispatch 路径的便捷封装层:把"读派生密钥 env +
// 从 UA 抽 CLI 版本 + 调 RewriteInboundBody"收拢成单一入口 RewriteForDispatch,
// 供 gatewayhttp 的两条非流式 dispatch 路径直接内联调用 —— 避免在 gatewayhttp
// 大包里新增文件(codebudget 文件数预算),把逻辑留在本职责子包内。

import (
	"os"
	"regexp"
	"strings"
)

// envServerSecret 是设备/会话指纹确定性派生的密钥来源环境变量名。**必须固定**:
// 轮换会致同账号派生指纹整体突变(同账号前后请求的 device_id/session 变化)。
// 未配置(空)时 RewriteForDispatch fail-open 不改写。
const envServerSecret = "HUAKAI_MIMICRY_IDENTITY_SECRET"

// claudeCodeUAVersionRE 从 User-Agent 抽取 Claude Code CLI 版本号(形如
// "claude-cli/2.1.78 ...")。抽不到返回空串,按旧版 user_id 格式处理。
var claudeCodeUAVersionRE = regexp.MustCompile(`(?i)claude-cli/([0-9]+(?:\.[0-9]+)*)`)

// ExtractClaudeCodeVersion 从 UA 串抽 CLI 版本;抽不到返回空。
func ExtractClaudeCodeVersion(userAgent string) string {
	m := claudeCodeUAVersionRE.FindStringSubmatch(userAgent)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

// serverSecret 读固定来源的派生密钥(env);两端空白裁剪。
func serverSecret() string {
	return strings.TrimSpace(os.Getenv(envServerSecret))
}

// RewriteForDispatch 是 dispatch 接线方的单一入口:对 dispatch 专用 body 拷贝
// 施加 R7 身份改写。内部完成"读派生密钥 env + 调 RewriteInboundBody",版本由
// 调用方从 UA 抽好后以 cliVersion 传入(避免本层 import net/http)。
//
// 默认关 + fail-open 语义完全由 RewriteInboundBody 保证(开关默认关、external
// account id 空、serverSecret 空、改写出错均返回原 body 拷贝,永不阻断请求)。
//
// 入参 dispatchBody 必须是 dispatch 专用拷贝,**不得是参与缓存键计算的原始
// 客户端 body**。
func RewriteForDispatch(dispatchBody []byte, accountID int64, externalAccountID, clientSessionID, cliVersion string) []byte {
	id := AccountIdentity{
		AccountID:         accountID,
		ExternalAccountID: externalAccountID,
		ClientSessionID:   clientSessionID,
		ClientCLIVersion:  cliVersion,
	}
	out, _ := RewriteInboundBody(dispatchBody, id, serverSecret())
	// RewriteInboundBody 在出错时也返回原 body 拷贝(非 nil),故直接用其返回值。
	return out
}
