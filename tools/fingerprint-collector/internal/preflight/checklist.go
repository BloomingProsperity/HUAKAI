// 包 preflight 实现工具启动前的交互式确认检查清单。
// 这是一道强制伦理和法律门槛——每次运行都必须由操作员主动确认，
// 除非明确传入 -confirm-pre-flight 标志（用于 CI/自动化场景）。
package preflight

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

// ChecklistItem 表示需要操作员逐一确认的单个检查条目。
type ChecklistItem struct {
	// ID 是条目的简短标识符，用于日志
	ID string
	// Text 是向操作员展示的确认问题
	Text string
}

// defaultChecklist 是每次运行必须确认的法律和伦理检查清单。
// 顺序固定，不可删减。
var defaultChecklist = []ChecklistItem{
	{
		ID:   "own_machine",
		Text: "我正在一台我拥有或完全控制的机器上运行此工具。",
	},
	{
		ID:   "own_traffic",
		Text: "我即将捕获的流量由我自己合法持有的上游账户的第一方客户端产生。",
	},
	{
		ID:   "no_mitm",
		Text: "我所在的网络环境中不存在我未知晓的 TLS 拦截代理（MITM）。",
	},
	{
		ID:   "no_pcap_commit",
		Text: "我不会提交、上传或分享原始 pcap 文件。只有经过净化的指纹模板可存入私有模板库。",
	},
	{
		ID:   "tos_compliance",
		Text: "我使用捕获指纹的行为符合上游服务商的服务条款，并遵守我所在司法管辖区的适用法律。",
	},
}

// RunChecklist 执行交互式确认检查清单。
// autoConfirm 为 true 时跳过交互，直接记录自动确认日志（用于 CI）。
// 任一条目未通过确认则返回 error，工具应立即退出。
func RunChecklist(autoConfirm bool) error {
	if autoConfirm {
		// CI 场景：打印自动确认日志，不交互
		fmt.Println("[preflight] -confirm-pre-flight 已设置，自动确认所有检查项（CI 模式）。")
		fmt.Println("[preflight] 操作员通过传入此标志，以编程方式接受了以下所有条款：")
		for i, item := range defaultChecklist {
			fmt.Printf("[preflight]   %d. %s\n", i+1, item.Text)
		}
		fmt.Printf("[preflight] 自动确认时间：%s\n", time.Now().UTC().Format(time.RFC3339))
		return nil
	}

	// 交互模式：逐条提示操作员确认
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║          fingerprint-collector 启动前确认检查清单            ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("在开始捕获之前，请逐一阅读并确认以下每一项。")
	fmt.Println("每项均需输入 \"yes\" 确认（输入其他内容视为拒绝，工具将退出）。")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	for i, item := range defaultChecklist {
		fmt.Printf("[%d/%d] %s\n", i+1, len(defaultChecklist), item.Text)
		fmt.Print("      请确认 (yes/no): ")

		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("读取输入失败: %w", err)
		}
		answer := strings.TrimSpace(strings.ToLower(line))
		if answer != "yes" {
			fmt.Printf("\n[preflight] 条目 %d (%s) 未确认（输入: %q）。工具退出。\n",
				i+1, item.ID, answer)
			return fmt.Errorf("preflight 检查未通过：条目 %q 未获确认", item.ID)
		}
		fmt.Printf("      ✓ 已确认\n\n")
	}

	fmt.Println("[preflight] 所有检查项均已确认。正在启动捕获流程...")
	fmt.Printf("[preflight] 确认时间：%s\n\n", time.Now().UTC().Format(time.RFC3339))
	return nil
}
