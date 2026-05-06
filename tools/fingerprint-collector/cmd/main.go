// fingerprint-collector — 操作员第一方 TLS/HTTP2 传输层指纹捕获工具。
// 只捕获未加密的 ClientHello 握手元数据；绝不解密任何 TLS 载荷。
//
// 用法示例：
//
//	fingerprint-collector -iface eth0 -host api.anthropic.com -duration 600 -min-samples 5 -out ./output/
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"

	"github.com/BloomingProsperity/HUAKAI/tools/fingerprint-collector/internal/capture"
	"github.com/BloomingProsperity/HUAKAI/tools/fingerprint-collector/internal/mitm"
	"github.com/BloomingProsperity/HUAKAI/tools/fingerprint-collector/internal/output"
	"github.com/BloomingProsperity/HUAKAI/tools/fingerprint-collector/internal/preflight"
	tlspkg "github.com/BloomingProsperity/HUAKAI/tools/fingerprint-collector/internal/tls"
)

// 命令行参数
var (
	flagIface            = flag.String("iface", "", "网络接口名称（必填，如 eth0 / \\Device\\NPF_{...}）")
	flagHost             = flag.String("host", "api.anthropic.com", "目标主机名（只捕获此主机的握手流量）")
	flagDuration         = flag.Int("duration", 600, "捕获持续时间（秒）")
	flagMinSamples       = flag.Int("min-samples", 5, "至少收集到此数量的 ClientHello 样本后才写入输出")
	flagOut              = flag.String("out", "./output/", "输出目录路径")
	flagConfirmPreflight = flag.Bool("confirm-pre-flight", false, "跳过交互式预检清单（CI 专用）")
	flagDisableMITM      = flag.Bool("disable-mitm-detection", false, "禁用 MITM 证书链检测（操作员明确知晓并接受风险时使用）")
	flagIncludeSNI       = flag.Bool("include-sni", false, "在 clienthello-template.json 中保留真实 SNI 值（默认已脱敏）")
	flagIncludeOpInfo    = flag.Bool("include-operator-info", false, "在 metadata.json 中包含操作员主机名（默认不收集）")
	flagListIfaces       = flag.Bool("list-ifaces", false, "列出所有可用网络接口后退出")
	flagConfig           = flag.String("config", "", "配置文件路径（TOML 或 YAML，可选）")
)

func main() {
	flag.Parse()

	// 如果只是列出接口，执行后退出
	if *flagListIfaces {
		listInterfaces()
		return
	}

	// 验证必填参数
	if *flagIface == "" {
		fmt.Fprintln(os.Stderr, "错误：必须通过 -iface 指定网络接口。使用 -list-ifaces 查看可用接口。")
		flag.Usage()
		os.Exit(1)
	}
	if *flagHost == "" {
		fmt.Fprintln(os.Stderr, "错误：必须通过 -host 指定目标主机名。")
		os.Exit(1)
	}

	// 如果提供了配置文件，加载并覆盖命令行参数（简化实现：仅打印提示）
	if *flagConfig != "" {
		log.Printf("[config] 已指定配置文件 %q（v1 仅记录路径，命令行参数优先）", *flagConfig)
	}

	// 1. 预检清单
	if err := preflight.RunChecklist(*flagConfirmPreflight); err != nil {
		fmt.Fprintf(os.Stderr, "\n[abort] 预检未通过，工具退出：%v\n", err)
		os.Exit(1)
	}

	// 2. MITM 检测（主动 TLS 握手验证）
	mitmResult := "skipped"
	if !*flagDisableMITM {
		log.Printf("[mitm] 正在验证 %s 的证书链，超时 10s...", *flagHost)
		result, err := mitm.CheckHost(*flagHost, 10*time.Second)
		if err != nil {
			log.Printf("[mitm] 证书链检查出错（不阻止运行）: %v", err)
			mitmResult = "error"
		} else if !result.OK {
			// MITM 检测到异常：打印醒目警告后退出
			fmt.Fprintf(os.Stderr, "\n")
			fmt.Fprintf(os.Stderr, "╔══════════════════════════════════════════════════════════════╗\n")
			fmt.Fprintf(os.Stderr, "║              ⚠  可能检测到 MITM 代理  ⚠                      ║\n")
			fmt.Fprintf(os.Stderr, "╚══════════════════════════════════════════════════════════════╝\n")
			fmt.Fprintf(os.Stderr, "%s\n\n", result.Warning)
			fmt.Fprintf(os.Stderr, "如确认这是预期的环境（如你自己搭建的 TLS 终止代理），\n")
			fmt.Fprintf(os.Stderr, "请使用 -disable-mitm-detection 标志重新运行。\n\n")
			os.Exit(1)
		} else {
			log.Printf("[mitm] 证书链验证通过：叶证书 CN=%q，根 CA=%q", result.LeafCN, result.RootCN)
			mitmResult = "ok"
		}
	} else {
		log.Printf("[mitm] MITM 检测已禁用（-disable-mitm-detection）")
	}

	// 3. 构建 BPF 过滤器
	bpfFilter, resolvedIPs, err := capture.BuildBPFFilter(*flagHost, 443)
	if err != nil {
		log.Fatalf("[capture] 构建 BPF 过滤器失败: %v", err)
	}
	log.Printf("[capture] BPF 过滤器: %s", bpfFilter)
	if len(resolvedIPs) > 0 {
		log.Printf("[capture] 目标 IP: %v", resolvedIPs)
	}

	// 4. 确保输出目录存在
	w, err := output.NewWriter(*flagOut)
	if err != nil {
		log.Fatalf("[output] 初始化输出目录失败: %v", err)
	}
	w.IncludeSNI = *flagIncludeSNI
	w.IncludeOperatorInfo = *flagIncludeOpInfo

	// 5. 打开 pcap 捕获
	pcapOutPath := filepath.Join(*flagOut, "raw-pcap-snippet.pcap")
	collector, err := capture.NewCollector(*flagIface, bpfFilter, pcapOutPath)
	if err != nil {
		log.Fatalf("[capture] 初始化捕获失败: %v\n请检查接口名称和权限。", err)
	}
	defer collector.Close()

	// 始终打印 pcap 文件路径警告
	log.Printf("[警告] ══════════════════════════════════════════════════════════")
	log.Printf("[警告] 原始 pcap 文件写入: %s", collector.PcapOutputPath())
	log.Printf("[警告] 此文件包含原始网络包，切勿提交到版本库或对外分享！")
	log.Printf("[警告] ══════════════════════════════════════════════════════════")

	// 6. 开始捕获循环
	captureStart := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*flagDuration)*time.Second)
	defer cancel()

	// 捕获 Ctrl+C 信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			log.Printf("[capture] 收到信号 %v，正在停止捕获...", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	log.Printf("[capture] 开始在接口 %q 上捕获 %q 的 TLS ClientHello，超时 %ds，最少 %d 个样本",
		*flagIface, *flagHost, *flagDuration, *flagMinSamples)
	log.Printf("[capture] 请在另一个终端运行第一方客户端并发出 5-10 次请求...")

	var (
		samples []*tlspkg.ClientHello
		ja3s    []tlspkg.JA3Result
		ja4s    []tlspkg.JA4Result
	)

	pkgSrc := collector.PacketSource()
	pkgSrc.DecodeOptions.Lazy = true
	pkgSrc.DecodeOptions.NoCopy = true

captureLoop:
	for {
		select {
		case <-ctx.Done():
			log.Printf("[capture] 捕获超时/中止，已收集 %d 个样本", len(samples))
			break captureLoop
		default:
		}

		packet, err := pkgSrc.NextPacket()
		if err != nil {
			if err == pcap.NextErrorTimeoutExpired {
				continue
			}
			// io.EOF 或 pcap 句柄关闭时退出
			break captureLoop
		}

		// 写入原始包到 pcap 文件
		if werr := collector.WritePacket(packet); werr != nil {
			log.Printf("[capture] 写入 pcap 包失败（忽略）: %v", werr)
		}

		// 从 TCP 载荷中提取 TLS ClientHello
		tcpLayer := packet.Layer(layers.LayerTypeTCP)
		if tcpLayer == nil {
			continue
		}
		tcp, ok := tcpLayer.(*layers.TCP)
		if !ok || len(tcp.Payload) < 5 {
			continue
		}

		ch, err := tlspkg.ParseClientHelloFromRecord(tcp.Payload)
		if err != nil {
			// 不是 ClientHello 或解析错误，忽略（大多数 TCP 包不是握手包）
			continue
		}

		// 成功解析到 ClientHello
		ja3 := tlspkg.ComputeJA3(ch)
		ja4 := tlspkg.ComputeJA4(ch)

		log.Printf("[tls] ClientHello #%d: cipher_suites=%d exts=%d JA3=%s JA4=%s",
			len(samples)+1,
			len(ch.CipherSuites),
			len(ch.Extensions),
			ja3.Hash,
			ja4.Hash,
		)

		samples = append(samples, ch)
		ja3s = append(ja3s, ja3)
		ja4s = append(ja4s, ja4)

		// 达到最小样本数后提前通知（继续捕获到超时）
		if len(samples) == *flagMinSamples {
			log.Printf("[capture] 已达到最少样本数 %d，继续捕获直到超时...", *flagMinSamples)
		}
		if len(samples) >= *flagMinSamples {
			break captureLoop
		}
	}

	captureEnd := time.Now().UTC()

	// 7. 写入输出文件
	if len(samples) == 0 {
		log.Println("[output] 未捕获到任何 ClientHello 样本。")
		log.Println("[output] 请检查：")
		log.Println("[output]   1. 接口名称是否正确（-list-ifaces）")
		log.Println("[output]   2. 目标主机名是否能解析")
		log.Println("[output]   3. 客户端是否在捕获窗口内发出请求")
		os.Exit(1)
	}

	if len(samples) < *flagMinSamples {
		log.Printf("[output] 警告：只收集到 %d 个样本（要求 %d），将使用现有样本输出",
			len(samples), *flagMinSamples)
	}

	// 使用第一个样本作为代表性模板（所有样本应来自同一客户端）
	representative := samples[0]
	repJA3 := ja3s[0]
	repJA4 := ja4s[0]

	if err := w.WriteClientHelloTemplate(representative, repJA3, repJA4, len(samples)); err != nil {
		log.Printf("[output] 写入 clienthello-template.json 失败: %v", err)
	}
	if err := w.WriteJA3(ja3s); err != nil {
		log.Printf("[output] 写入 ja3-hashes.txt 失败: %v", err)
	}
	if err := w.WriteJA4(ja4s); err != nil {
		log.Printf("[output] 写入 ja4-hashes.txt 失败: %v", err)
	}
	if err := w.WriteHTTP2Settings(); err != nil {
		log.Printf("[output] 写入 http2-settings.json 失败: %v", err)
	}

	// metadata.json
	meta := output.Metadata{
		ToolVersion:          output.Version,
		CaptureStartTime:     captureStart.Format(time.RFC3339),
		CaptureEndTime:       captureEnd.Format(time.RFC3339),
		TargetHost:           *flagHost,
		SampleCount:          len(samples),
		MITMDetectionEnabled: !*flagDisableMITM,
		MITMCheckResult:      mitmResult,
		Note: "此文件不含 IP/MAC/主机名信息（除非使用了 -include-operator-info）。" +
			"提交前请确认不含敏感信息。",
	}
	if *flagIncludeOpInfo {
		hostname, _ := os.Hostname()
		meta.OperatorInfo = &output.OperatorInfo{Hostname: hostname}
	}
	if err := w.WriteMetadata(meta); err != nil {
		log.Printf("[output] 写入 metadata.json 失败: %v", err)
	}

	log.Printf("[done] 捕获完成，共 %d 个样本，输出目录: %s", len(samples), *flagOut)
	log.Printf("[done] 请勿提交 %s！", pcapOutPath)
}

// listInterfaces 打印所有可用网络接口并退出。
func listInterfaces() {
	ifaces, err := capture.ListInterfaces()
	if err != nil {
		fmt.Fprintf(os.Stderr, "列出接口失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("可用网络接口：")
	for _, name := range ifaces {
		fmt.Printf("  %s\n", name)
	}
}
