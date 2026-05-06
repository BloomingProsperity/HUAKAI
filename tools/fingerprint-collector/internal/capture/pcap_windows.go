//go:build windows && cgo

// 包 capture — Windows 平台的 pcap 捕获实现（依赖 npcap）。
// 运行前需要：
//   1. 从 https://npcap.com/ 下载并安装 npcap，
//      安装时勾选 "WinPcap API-compatible Mode"。
//   2. 以管理员身份运行此工具（右键"以管理员身份运行"）。
package capture

import (
	"fmt"
	"os"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
	"github.com/google/gopacket/pcapgo"
)

// platformName 用于日志和错误消息中标识当前平台
const platformName = "windows/npcap"

// OpenLive 在 Windows 上通过 npcap（WinPcap API 兼容模式）打开接口。
// 注意：Windows 下接口名称格式通常为 \Device\NPF_{GUID}，
// gopacket/pcap.FindAllDevs() 可获取完整列表。
func OpenLive(iface string, snapLen int32, promisc bool, timeout time.Duration) (*pcap.Handle, error) {
	handle, err := pcap.OpenLive(iface, snapLen, promisc, timeout)
	if err != nil {
		return nil, fmt.Errorf(
			"打开接口 %q 失败（npcap）: %w\n"+
				"提示：请确认已安装 npcap（含 WinPcap 兼容模式）并以管理员身份运行。\n"+
				"使用 'fingerprint-collector -list-ifaces' 列出可用接口。",
			iface, err,
		)
	}
	return handle, nil
}

// Collector 封装了 pcap 捕获句柄和相关状态。
type Collector struct {
	handle   *pcap.Handle
	pcapFile *os.File
	pcapW    *pcapgo.Writer
	outPath  string
}

// NewCollector 创建并初始化一个 Collector。
// bpfFilter 是 BPF 过滤表达式，pcapOutPath 是原始 pcap 输出文件路径。
func NewCollector(iface, bpfFilter, pcapOutPath string) (*Collector, error) {
	// 打开接口（非混杂模式，超时 1 秒以允许定期检查退出条件）
	handle, err := OpenLive(iface, 65535, false, time.Second)
	if err != nil {
		return nil, err
	}

	// 设置 BPF 过滤器（只捕获目标主机的握手流量）
	if bpfFilter != "" {
		if err := handle.SetBPFFilter(bpfFilter); err != nil {
			handle.Close()
			return nil, fmt.Errorf("设置 BPF 过滤器 %q 失败: %w", bpfFilter, err)
		}
	}

	// 创建 pcap 输出文件（原始包转储，仅本地使用）
	f, err := os.Create(pcapOutPath)
	if err != nil {
		handle.Close()
		return nil, fmt.Errorf("创建 pcap 文件 %q 失败: %w", pcapOutPath, err)
	}

	w := pcapgo.NewWriter(f)
	if err := w.WriteFileHeader(65535, layers.LinkTypeEthernet); err != nil {
		f.Close()
		handle.Close()
		return nil, fmt.Errorf("写入 pcap 文件头失败: %w", err)
	}

	return &Collector{
		handle:   handle,
		pcapFile: f,
		pcapW:    w,
		outPath:  pcapOutPath,
	}, nil
}

// PacketSource 返回一个 gopacket.PacketSource，用于迭代捕获到的数据包。
func (c *Collector) PacketSource() *gopacket.PacketSource {
	return gopacket.NewPacketSource(c.handle, c.handle.LinkType())
}

// WritePacket 将数据包写入 pcap 文件（仅在捕获期间调用）。
func (c *Collector) WritePacket(packet gopacket.Packet) error {
	return c.pcapW.WritePacket(packet.Metadata().CaptureInfo, packet.Data())
}

// Close 关闭捕获句柄和 pcap 输出文件。
func (c *Collector) Close() {
	if c.handle != nil {
		c.handle.Close()
	}
	if c.pcapFile != nil {
		c.pcapFile.Close()
	}
}

// PcapOutputPath 返回原始 pcap 文件的输出路径（用于警告日志）。
func (c *Collector) PcapOutputPath() string {
	return c.outPath
}
