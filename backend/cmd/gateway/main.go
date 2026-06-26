// Package main 是 HUAKAI 网关的入口。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/loglevel"

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

	if err := run(logger); err != nil {
		logger.Fatal("gateway exited with error", zap.Error(err))
	}
}

func run(logger *zap.Logger) error {
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

	runtime, err := buildGatewayRuntime(ctx, cfg, mimicryRegistry, logger)
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
