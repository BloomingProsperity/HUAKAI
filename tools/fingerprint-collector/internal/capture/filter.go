// 包 capture 负责网络接口选择和 BPF 过滤器配置。
package capture

import (
	"fmt"
	"net"
	"strings"

	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
)

// ValidateBPF 校验操作员提供的原始 BPF 表达式是否非空且可由 libpcap 编译。
func ValidateBPF(expr string) error {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return fmt.Errorf("BPF 表达式不能为空")
	}
	if _, err := pcap.CompileBPFFilter(layers.LinkTypeEthernet, 65535, expr); err != nil {
		return fmt.Errorf("BPF 表达式无法编译: %w", err)
	}
	return nil
}

// BuildBPFFilter 为给定的目标主机和端口构建 BPF 过滤表达式。
// 过滤规则：只捕获到/来自目标主机 IP 的 443 端口 TCP 流量。
// 若主机名无法解析，则只按端口过滤（并记录警告）。
func BuildBPFFilter(host string, port int) (string, []string, error) {
	// 尝试解析主机名为 IP 列表，以构建精确过滤器
	ips, err := net.LookupHost(host)
	if err != nil || len(ips) == 0 {
		// 解析失败：退回到只过滤端口（范围更宽，但仍有用）
		filter := fmt.Sprintf("tcp port %d", port)
		return filter, nil, nil
	}

	// 去重（部分主机有多个 A/AAAA 记录）
	seen := make(map[string]bool)
	var uniqueIPs []string
	for _, ip := range ips {
		parsed := net.ParseIP(ip)
		if parsed == nil || seen[ip] {
			continue
		}
		seen[ip] = true
		uniqueIPs = append(uniqueIPs, ip)
	}

	// 构建 "host <ip1> or host <ip2>" 形式的表达式，并限定端口
	hostParts := make([]string, len(uniqueIPs))
	for i, ip := range uniqueIPs {
		hostParts[i] = fmt.Sprintf("host %s", ip)
	}

	var filter string
	if len(hostParts) == 1 {
		filter = fmt.Sprintf("tcp port %d and %s", port, hostParts[0])
	} else {
		filter = fmt.Sprintf("tcp port %d and (%s)", port, strings.Join(hostParts, " or "))
	}

	return filter, uniqueIPs, nil
}

// ListInterfaces 返回系统上所有可用的网络接口名称列表，供操作员选择。
func ListInterfaces() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("列举网络接口失败: %w", err)
	}
	names := make([]string, 0, len(ifaces))
	for _, iface := range ifaces {
		names = append(names, iface.Name)
	}
	return names, nil
}

// ValidateInterface 验证给定名称的网络接口是否存在且处于 UP 状态。
func ValidateInterface(name string) error {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return fmt.Errorf("接口 %q 不存在: %w", name, err)
	}
	if iface.Flags&net.FlagUp == 0 {
		return fmt.Errorf("接口 %q 未处于 UP 状态（当前标志: %v）", name, iface.Flags)
	}
	return nil
}
