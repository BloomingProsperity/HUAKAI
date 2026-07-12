package gatewayhttp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
)

// TestDefaultRatioAfterResolverError_NoRawErrorInLog 守护:pricing 比率解析失败的日志
// 只落 error_class/error_type,不落 err 原文(err 可能携带 SQL/后端细节)。
// 变异判别:把日志字段改回 "error", err → marker 出现在日志 → 本测试红。
func TestDefaultRatioAfterResolverError_NoRawErrorInLog(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const marker = "PRICING_RESOLVER_RAW_SECRET_MARKER"
	ex := &chatExecution{
		ctx:   context.Background(),
		ident: auth.Identity{TenantID: 7},
	}
	ex.attempt.PoolGroupID = 11

	ratio, pending := ex.defaultRatioAfterResolverError(errors.New("resolver blew up " + marker))

	if !ratio.Equal(decimal.NewFromInt(1)) || !pending {
		t.Fatalf("回退语义变了: ratio=%s pending=%v, 期望 ratio=1 pending=true", ratio, pending)
	}
	out := logs.String()
	if strings.Contains(out, marker) {
		t.Fatalf("err 原文泄进日志: %s", out)
	}
	if !strings.Contains(out, "error_class") {
		t.Fatalf("日志缺 error_class 字段: %s", out)
	}
}
