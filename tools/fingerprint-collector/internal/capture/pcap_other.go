//go:build (!linux && !windows) || !cgo

// 包 capture — 无 CGO 或不支持平台的存根实现。
//
// 此文件在以下两种情况下被编译：
//   1. 非 Linux/Windows 平台（如 macOS 交叉编译）
//   2. CGO 被禁用时（CGO_ENABLED=0，常见于交叉编译场景）
//
// 实时捕获需要 CGO + 平台原生 pcap 库（libpcap / npcap）。
// 在目标平台上本地编译并启用 CGO 即可获得完整功能。
//
// Linux 本地编译：sudo apt-get install libpcap-dev && go build ./cmd/
// Windows 本地编译：安装 npcap（WinPcap 兼容模式）后 go build ./cmd/
package capture

import (
	"errors"
	"os"

	"github.com/google/gopacket"
	"github.com/google/gopacket/pcapgo"
)

// platformName 标识当前平台
const platformName = "stub/no-cgo"

// errNoCGO 是在 CGO 不可用或不支持平台时的错误提示。
var errNoCGO = errors.New(
	"实时 pcap 捕获需要 CGO 支持和平台原生 pcap 库（libpcap/npcap）。\n" +
		"请在目标平台上本地编译：\n" +
		"  Linux:   sudo apt-get install libpcap-dev && go build ./cmd/\n" +
		"  Windows: 安装 npcap（WinPcap 兼容模式）后 go build ./cmd/",
)

// Collector 在无 CGO 环境下的存根实现。
type Collector struct {
	pcapFile *os.File
	pcapW    *pcapgo.Writer
	outPath  string
}

// NewCollector 在无 CGO 环境下返回错误，提示操作员如何正确编译。
func NewCollector(iface, bpfFilter, pcapOutPath string) (*Collector, error) {
	return nil, errNoCGO
}

// PacketSource 在存根实现中不可用（NewCollector 已返回错误，此方法不应被调用）。
func (c *Collector) PacketSource() *gopacket.PacketSource {
	return nil
}

// WritePacket 存根实现。
func (c *Collector) WritePacket(packet gopacket.Packet) error {
	return errNoCGO
}

// Close 存根实现（空操作）。
func (c *Collector) Close() {}

// PcapOutputPath 返回配置的输出路径（即使无法捕获）。
func (c *Collector) PcapOutputPath() string {
	return c.outPath
}

