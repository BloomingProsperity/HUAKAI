// Package main is the HUAKAI gateway entry point.
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

// smokeBuildStamp is overridden via -ldflags during smoke test builds to
// produce a unique binary hash per run, dodging Smart App Control's
// per-hash block cache. Empty in normal/production builds.
var smokeBuildStamp string

func main() {
	_ = smokeBuildStamp // referenced only by the smoke build to defeat dead-code elimination
	loggerCfg := zap.NewProductionConfig()
	loggerCfg.Level = loglevel.Level // runtime-adjustable via /admin/v1/loglevel
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
