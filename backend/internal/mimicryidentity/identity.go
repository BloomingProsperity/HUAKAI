// Package mimicryidentity 是 R7 身份改写【接线】层:把池中选定账号的身份
// 投影成 metadata.user_id,让上游看到的客户端身份与 HUAKAI 实际派发的账号
// 一致,避免身份不一致触发上游风控。
//
// 本包不重复实现 body 变换 —— 改写逻辑沉淀在 gateway 包已建的 6-step 强伪装
// 组合器 gateway.ApplyMimicryPlan(step5 = metadata.user_id),本包只负责:
//  1. 据账号上下文构造 gateway.MimicryPlan(仅启 step5);
//  2. 读运维开关决定是否启用(默认关 = 整管线 no-op = body 字节完全不变);
//  3. fail-open:external account id 为空时不带 plan、不改写;
//  4. 设备/会话指纹用 SHA256 确定性派生,免存储。
//
// 依赖方向:mimicryidentity → gateway(单向,gateway 不反向 import 本包,
// 无 import 环)。接线方(gatewayhttp)在 dispatch 之前、对"dispatch 专用
// body 拷贝"调用本包,绝不触碰参与缓存键计算的原始客户端 body。
//
// 默认行为:开关默认关。**翻默认(默认开)是 Owner-gated 二阶段,不在本切片。**
//
// 机制参考三镜(§16,仅描述做法,代码本仓独立编写):
//   - sub2api 有同类 metadata.user_id 身份重写(账号 UUID 投影 + 确定性 session
//     派生 + 空身份 fail-open + 只改单字段保留其余字节);为唯一有此能力者,
//     按 §16 默认 tiebreaker 取其机制。
//   - new-api 无等价(user_id 命中均为 vendor DTO 字段/channel-affinity 设置)。
//   - CLIProxyAPI 仅从 metadata.user_id 读 session 做账号亲和选择,不改写。
package mimicryidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

// envIdentityRewrite 是身份改写的运维开关环境变量名。**默认关**:
// 空 / 未配置 / 任意非 "true" 值一律视为关(整管线 no-op,body 字节不变);
// 仅显式设为 "true" 时才启用。读取惯例与 transport.MimicryEnabled 一致 ——
// 就地读 env,避免反向 import config 包。
//
// 注意与 transport.MimicryEnabled 的默认极性相反:那个开关管 TLS 指纹伪装、
// 默认开(关现网行为);本开关管请求体身份改写、**默认关**,因为它会改写
// 转发出去的请求体内容,翻默认属默认行为翻转 = Owner-gated。
const envIdentityRewrite = "HUAKAI_MIMICRY_IDENTITY_REWRITE"

// RewriteEnabled 报告身份改写运维开关是否显式开启。默认(空/未配置/非 "true")
// 返回 false。
func RewriteEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(envIdentityRewrite)), "true")
}

// AccountIdentity 是构造改写计划所需的账号上下文。由接线方据池中选定账号
// 填充。
type AccountIdentity struct {
	// AccountID 是池中账号主键,用于设备/会话指纹的确定性派生 seed。
	AccountID int64
	// ExternalAccountID 是上游账号的稳定标识(如 Anthropic account uuid),
	// 写入 metadata.user_id 的 account 组件。**为空时 fail-open:不改写。**
	ExternalAccountID string
	// ClientCLIVersion 是客户端 Claude Code CLI 版本(从 UA 解析),决定写回
	// user_id 用 JSON 新格式还是 legacy 拼接格式。空串按旧版处理。
	ClientCLIVersion string
}

// RewriteInboundBody 是本包对接线方暴露的唯一入口:对 dispatch 专用的请求
// body 拷贝施加 metadata.user_id 身份改写,返回新 body(或在任一 no-op/
// fail-open 条件下返回原 body 的拷贝)。
//
// serverSecret 是设备/会话指纹确定性派生的密钥来源,必须由接线方从一个
// **固定**配置项注入。注意:serverSecret 轮换会致派生指纹整体突变(同一
// 账号前后请求的 device_id/session 会变),故来源必须固定、不可每进程随机
// 生成。serverSecret 为空时无法安全派生 → fail-open 不改写。
//
// 短路/fail-open 条件(任一命中即返回原 body 拷贝,不改写):
//   - 运维开关未开(RewriteEnabled()==false)——默认关;
//   - body 为空;
//   - id.ExternalAccountID 为空(镜像 sub2api account_uuid==” 跳过);
//   - serverSecret 为空。
//
// 满足条件时构造仅启 step5 的 gateway.MimicryPlan 并调 gateway.ApplyMimicryPlan。
// ApplyMimicryPlan 本身在 metadata 缺失/不可解析等情形下也 fail-open
// (返回 body 拷贝),故本入口永不因身份相关原因阻断请求。
func RewriteInboundBody(body []byte, id AccountIdentity, serverSecret string) ([]byte, error) {
	if !RewriteEnabled() {
		return cloneBody(body), nil
	}
	if len(body) == 0 {
		return cloneBody(body), nil
	}
	// fail-open:缺 external account id 不改写(镜像 sub2 account_uuid=='' 跳过)。
	if strings.TrimSpace(id.ExternalAccountID) == "" {
		return cloneBody(body), nil
	}
	// serverSecret 为空无法确定性派生设备/会话指纹 → fail-open。
	if strings.TrimSpace(serverSecret) == "" {
		return cloneBody(body), nil
	}

	plan := BuildPlan(id, serverSecret)
	res, err := gateway.ApplyMimicryPlan(body, plan)
	if err != nil {
		// 改写出错时绝不阻断请求:回退到原 body 拷贝。缓存优化与身份伪装
		// 都不是请求的硬依赖。
		return cloneBody(body), err
	}
	return res.Body, nil
}

// BuildPlan 据账号上下文构造仅启用 step5(metadata.user_id)的 6-step 计划。
// 其余 5 步保持关闭(nil/false),整管线只改 metadata 子树。
//
// 设备指纹与会话指纹都用 SHA256(serverSecret::accountID::scope) 确定性派生:
//   - device_id:免存储、同账号稳定,scope="device";
//   - session:scope="session",派生成 UUID 形态(8-4-4-4-12),契合 wire 协议
//     对会话组件的结构期望。
//
// account 组件直接用 ExternalAccountID(上游稳定账号标识)。
//
// Mode 用 rewrite:解析现有 user_id 并按组件替换;解析失败时回退到 fallback
// 整体写入,保证缺/坏 user_id 也能被投影成账号一致的身份。
func BuildPlan(id AccountIdentity, serverSecret string) gateway.MimicryPlan {
	device := deriveDeviceID(serverSecret, id.AccountID)
	session := deriveSessionUUID(serverSecret, id.AccountID)
	useNewFormat := gateway.IsNewMetadataFormatVersion(id.ClientCLIVersion)
	fallback := gateway.FormatMetadataUserID(device, id.ExternalAccountID, session, useNewFormat)

	return gateway.MimicryPlan{
		Enabled: true,
		MetadataUserID: &gateway.MetadataUserIDPlan{
			Mode:           gateway.MetadataInjectRewrite,
			DeviceID:       device,
			AccountUUID:    id.ExternalAccountID,
			SessionID:      session,
			UseNewFormat:   useNewFormat,
			FallbackUserID: fallback,
		},
	}
}

// deriveDeviceID 用 SHA256(serverSecret::accountID::device) 派生 64 位 hex 设备
// 指纹(契合 legacy 格式对 device 组件 64hex 的结构期望)。确定性、免存储。
func deriveDeviceID(serverSecret string, accountID int64) string {
	sum := deriveDigest(serverSecret, accountID, "device")
	return hex.EncodeToString(sum[:]) // 32 字节 → 64 hex
}

// deriveSessionUUID 用 SHA256(serverSecret::accountID::session) 派生成 UUID
// 形态(8-4-4-4-12)。确定性、免存储。取摘要前 16 字节投影成 UUID 串。
func deriveSessionUUID(serverSecret string, accountID int64) string {
	sum := deriveDigest(serverSecret, accountID, "session")
	h := hex.EncodeToString(sum[:16]) // 16 字节 → 32 hex
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// deriveDigest 计算 SHA256(serverSecret::accountID::scope)。account id 以
// 十进制字符串参与,与 scope 一起拼进 seed,保证不同 scope 派生不同指纹。
func deriveDigest(serverSecret string, accountID int64, scope string) [32]byte {
	var b strings.Builder
	b.WriteString(serverSecret)
	b.WriteString("::")
	b.WriteString(itoa(accountID))
	b.WriteString("::")
	b.WriteString(scope)
	return sha256.Sum256([]byte(b.String()))
}

// itoa 把 int64 转十进制字符串(避免 import strconv 仅为单次转换;负数带前导
// '-')。account id 实际恒为正,这里仍处理负数以防御。
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	var u uint64
	if neg {
		u = uint64(-n)
	} else {
		u = uint64(n)
	}
	var buf [20]byte
	i := len(buf)
	for u > 0 {
		i--
		buf[i] = byte('0' + u%10)
		u /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// cloneBody 返回 body 的独立拷贝(永不把入参切片回传给调用方)。
func cloneBody(body []byte) []byte {
	return append([]byte(nil), body...)
}
