package main

import (
	"os"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/toolpricing"
)

// toolSurchargeEnabledEnv 控制服务端工具调用(web_search / file_search /
// image_generation)的上游附加费是否对租户计费。
//
// 计费默认翻转(重要):默认【开】。未设 / 空 → 启用,按官方平台默认价计费
// (web_search $10/1000、file_search $2.5/1000、image_generation $0)。
// 之前生产价表恒 nil 导致工具调用加 $0 = 漏钱(我方付了上游成本却没向租户收);
// 本开关默认 ON 即修复该漏洞。显式 "false" / "0" → 关闭,退回旧 $0 行为给运维留退路。
const toolSurchargeEnabledEnv = "HUAKAI_TOOL_SURCHARGE_ENABLED"

// toolSurchargeRuntimeEnabled 解析 HUAKAI_TOOL_SURCHARGE_ENABLED。
//
// 默认 true(unset / 空 → 启用)。可解析的布尔值按字面处理("false" / "0" 关闭,
// "true" / "1" 开启)。无法解析的非法值不报错而是回落默认 true:本开关只在
// 「关掉计费」方向有副作用(漏钱),误把非法值当关闭反而会重新引入漏洞,故对
// 非法值保持启用更安全(fail-safe 朝着「不漏钱」一侧)。
//
// 复用 wiring.go 现有 default-true 解析风格(strconv.ParseBool),只是把
// malformed 的处理从 fail-loud 改为 fail-safe-on,因为本开关关闭=漏钱而非加固。
func toolSurchargeRuntimeEnabled() bool {
	raw := strings.TrimSpace(os.Getenv(toolSurchargeEnabledEnv))
	if raw == "" {
		return true
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		// 非法值:回落默认启用(不漏钱一侧)。
		return true
	}
	return enabled
}

// buildToolPriceSource 按运维开关构建工具附加费价表来源。
//
// 启用 → platformSource(平台默认价 + 无 override),让生产环境对工具调用按官方价计费。
// 关闭 → nil,gatewayhttp 消费点据此跳过附加费,退回旧 $0 行为。
func buildToolPriceSource() toolpricing.Source {
	if !toolSurchargeRuntimeEnabled() {
		return nil
	}
	return toolpricing.NewPlatformSource(toolpricing.DefaultToolPrices(), nil)
}
