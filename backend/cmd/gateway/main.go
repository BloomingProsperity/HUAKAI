// Package main 是 HUAKAI 网关的入口。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/BloomingProsperity/HUAKAI/internal/buildinfo"
	"github.com/BloomingProsperity/HUAKAI/internal/logfacade"
	"github.com/BloomingProsperity/HUAKAI/internal/loglevel"
	"github.com/BloomingProsperity/HUAKAI/internal/logsink"

	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

// smokeBuildStamp 在 smoke 测试构建期间通过 -ldflags 覆盖,
// 用于让每次运行产生唯一的二进制哈希,从而绕开 Smart App Control 的
// 按哈希拦截缓存。在常规/生产构建中为空。
var smokeBuildStamp string

func main() {
	_ = smokeBuildStamp // 仅供 smoke 构建引用,用于规避死代码消除
	loggerCfg := zap.NewProductionConfig()
	loggerCfg.Level = loglevel.Level // 可通过 /admin/v1/loglevel 在运行时调整
	logger, err := loggerCfg.Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger init failed: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = logger.Sync() }()
	// 运行日志入库 sink:两栈(zap+slog)的 warn+ 旁路采集,DB 就绪后
	// (buildGatewayRuntime)开始落库;此前先积压在有界队列。
	sink := logsink.New()
	logger = logger.WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
		return logsink.NewZapCore(c, sink)
	}))
	// 必须在 buildGatewayRuntime 之前装配:多个 worker 构造器在构造期捕获 slog.Default(),
	// 晚装配它们会永远拿旧的文本 handler。
	setupSlogFacade(sink)

	if err := run(logger, sink); err != nil {
		logger.Fatal("gateway exited with error", zap.Error(err))
	}
}

// setupSlogFacade 把全局 slog 默认 logger 接到 logfacade 门面:与 zap 共享
// loglevel.Level(/admin/v1/loglevel 热调对两栈同时生效),输出与 zap 同为
// stderr JSON,attr 值经 privacy 禁写扫描。全部存量 slog 调用点零改动升级。
func setupSlogFacade(sink *logsink.Sink) {
	slog.SetDefault(logfacade.New(logfacade.Options{
		Service: "huakai-gateway",
		Env:     strings.ToLower(strings.TrimSpace(os.Getenv("HUAKAI_RELEASE_MODE"))),
		Version: buildinfo.Version,
		Tap:     logsink.SlogTap(sink),
	}))
	// slog.SetDefault 会顺手把标准库 log 包改道到 slog handler,并固定按 Info 级
	// 过 loglevel 闸门(Go 源码 log/slog/logger.go 的 SetDefault:
	// log.SetOutput(handlerWriter)+log.SetFlags(0))。后果:/loglevel=warn 降噪时,
	// http.Server 的 "http: panic serving"+栈、trust-chain fail-open 警告等走 log 包
	// 的输出会整条静默消失;且 log.Printf 的动态文本进 message(门面不扫消息),
	// 成脱敏破口。这里刻意退回该隐式桥接,恢复 log 包基底行为(无条件直写 stderr、
	// 标准时间前缀);log 通道统一留后续片(先迁调用点到 slog 才能安全桥)。
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags)
}

func run(logger *zap.Logger, sink *logsink.Sink) error {
	// 启动即校验 release mode,拒绝遗漏或拼错 env 后静默跑 dev 的降级。
	if err := validateReleaseMode(); err != nil {
		return err
	}
	// 生产环境禁止开启 dev 令牌回显开关,否则注册/重置响应会泄露明文一次性令牌;带病配置直接拒启。
	if err := validateDevAuthTokenFlag(); err != nil {
		return err
	}
	cfg, err := loadGatewayConfig(logger)
	if err != nil {
		return err
	}
	mimicryRegistry, err := loadMimicryTemplateRegistry(logger)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	runtime, err := buildGatewayRuntime(ctx, cfg, mimicryRegistry, logger, sink)
	if err != nil {
		return err
	}
	defer runtime.close()

	router := newRouter(runtime.deps, logger)
	srv := newGatewayServer(cfg.Listen, router)
	return serveGateway(ctx, srv, runtime, cancel, logger)
}

func loadMimicryTemplateRegistry(logger *zap.Logger) (*mimicry.TemplateRegistry, error) {
	candidates := []string{
		"tools/fingerprint-collector/templates",
		"../tools/fingerprint-collector/templates",
	}
	for _, dir := range candidates {
		registry := mimicry.NewTemplateRegistry()
		err := registry.LoadFromDirectory(dir)
		if err == nil {
			logger.Info("mimicry template registry loaded",
				zap.String("dir", dir),
				zap.Int("mode_count", len(registry.Modes())),
			)
			return registry, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		return nil, fmt.Errorf("load mimicry template registry from %s: %w", dir, err)
	}
	logger.Warn("mimicry template registry not found; using Phase A default template fallback")
	return nil, nil
}
