//go:build linux && cgo

// 包 capture — Linux 平台的实时 pcap 捕获实现（依赖 libpcap + CGO）。
//
// 编译前提：
//   - 已安装 libpcap-dev: sudo apt-get install libpcap-dev
//   - 已启用 CGO（默认）
//
// 权限要求：
//   - 以 root 运行，或为二进制文件授予 cap_net_raw:
//     sudo setcap cap_net_raw+ep ./fingerprint-collector
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
const platformName = "linux/libpcap"

// openLiveHandle 使用 libpcap 打开指定接口进行实时捕获。
func openLiveHandle(iface string, snapLen int32, promisc bool, timeout time.Duration) (*pcap.Handle, error) {
	handle, err := pcap.OpenLive(iface, snapLen, promisc, timeout)
	if err != nil {
		if os.Geteuid() != 0 {
			return nil, fmt.Errorf(
				"打开接口 %q 失败（libpcap）: %w\n"+
					"提示：请以 root 运行，或执行 'sudo setcap cap_net_raw+ep <binary>'",
				iface, err,
			)
		}
		return nil, fmt.Errorf("打开接口 %q 失败: %w", iface, err)
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
func NewCollector(iface, bpfFilter, pcapOutPath string) (*Collector, error) {
	handle, err := openLiveHandle(iface, 65535, false, time.Second)
	if err != nil {
		return nil, err
	}
	if bpfFilter != "" {
		if err := handle.SetBPFFilter(bpfFilter); err != nil {
			handle.Close()
			return nil, fmt.Errorf("设置 BPF 过滤器 %q 失败: %w", bpfFilter, err)
		}
	}
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
	return &Collector{handle: handle, pcapFile: f, pcapW: w, outPath: pcapOutPath}, nil
}

// PacketSource 返回一个 gopacket.PacketSource，用于迭代捕获到的数据包。
func (c *Collector) PacketSource() *gopacket.PacketSource {
	return gopacket.NewPacketSource(c.handle, c.handle.LinkType())
}

// WritePacket 将数据包写入 pcap 文件。
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

// PcapOutputPath 返回原始 pcap 文件的输出路径。
func (c *Collector) PcapOutputPath() string {
	return c.outPath
}
