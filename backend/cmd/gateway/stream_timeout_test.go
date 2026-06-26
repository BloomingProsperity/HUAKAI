package main

import (
	"testing"
	"time"
)

// TestStreamDurationEnv 守护可经环境变量配置的流式超时:合法的 duration 字符串
// 会覆盖默认值,空/非法值则回退到默认值(永不 panic,永不把超时清零)。
func TestStreamDurationEnv(t *testing.T) {
	const key = "HUAKAI_STREAM_TEST_DUR"
	def := 600 * time.Second

	t.Setenv(key, "")
	if got := streamDurationEnv(key, def); got != def {
		t.Fatalf("unset must fall back to default, got %v", got)
	}
	t.Setenv(key, "90s")
	if got := streamDurationEnv(key, def); got != 90*time.Second {
		t.Fatalf("valid override must apply, got %v", got)
	}
	t.Setenv(key, "5m")
	if got := streamDurationEnv(key, def); got != 5*time.Minute {
		t.Fatalf("minute override must apply, got %v", got)
	}
	t.Setenv(key, "not-a-duration")
	if got := streamDurationEnv(key, def); got != def {
		t.Fatalf("invalid value must fall back to default (not zero/panic), got %v", got)
	}
}

// TestBuildStreamForwarderHasLongDefaults 在接线层守护 CF/长任务那项修复:
// production 流式超时的*默认值*必须长到足以承载推理/agentic 模型,且
// 绝不能回退到旧的硬编码 5s/10s/60s——那会在上游还没思考完之前就中止长时请求;
// keepalive 必须默认开启且短于代理的空闲窗口。
func TestBuildStreamForwarderHasLongDefaults(t *testing.T) {
	// 钉死的是"默认值",所以必须把所有 HUAKAI_STREAM_* override 清空,否则在某些有意设了更短
	// override 的开发/CI shell(如 HUAKAI_STREAM_TOTAL_TIMEOUT=60s)里会误红——那其实是 override
	// 生效的正确行为,不是默认回退缺陷。t.Setenv 在测试结束后自动还原。
	for _, k := range []string{
		"HUAKAI_STREAM_FIRST_TOKEN_TIMEOUT",
		"HUAKAI_STREAM_INTER_EVENT_TIMEOUT",
		"HUAKAI_STREAM_TOTAL_TIMEOUT",
		"HUAKAI_STREAM_DRAIN_MAX",
		"HUAKAI_STREAM_KEEPALIVE_INTERVAL",
		"HUAKAI_UPSTREAM_HEADER_TIMEOUT",
		"HUAKAI_UPSTREAM_REQUEST_TIMEOUT",
	} {
		t.Setenv(k, "")
	}
	f := buildStreamForwarder(nil, nil, nil)
	if f.Timeouts.TotalStreamTimeout < 300*time.Second {
		t.Fatalf("TotalStreamTimeout default too short for long-running AI: %v", f.Timeouts.TotalStreamTimeout)
	}
	if f.Timeouts.FirstTokenTimeout < 60*time.Second {
		t.Fatalf("FirstTokenTimeout default too short for reasoning-model TTFT: %v", f.Timeouts.FirstTokenTimeout)
	}
	if f.Timeouts.KeepAliveInterval <= 0 || f.Timeouts.KeepAliveInterval > 90*time.Second {
		t.Fatalf("KeepAliveInterval default must be ON and under proxy idle timeout: %v", f.Timeouts.KeepAliveInterval)
	}
	if f.Timeouts.HeaderToFirstByte != 15*time.Second {
		t.Fatalf("HeaderToFirstByte default=%v want 15s for non-streaming fast failover", f.Timeouts.HeaderToFirstByte)
	}
	if f.Timeouts.RequestTotalTimeout < 60*time.Second {
		t.Fatalf("RequestTotalTimeout default too short for buffered AI request: %v", f.Timeouts.RequestTotalTimeout)
	}
}

func TestBuildGatewayTimeoutConfigReadsNonStreamingEnv(t *testing.T) {
	t.Setenv("HUAKAI_UPSTREAM_HEADER_TIMEOUT", "40ms")
	t.Setenv("HUAKAI_UPSTREAM_REQUEST_TIMEOUT", "900ms")
	cfg := buildGatewayTimeoutConfig()
	if cfg.HeaderToFirstByte != 40*time.Millisecond {
		t.Fatalf("HeaderToFirstByte=%v want 40ms override", cfg.HeaderToFirstByte)
	}
	if cfg.RequestTotalTimeout != 900*time.Millisecond {
		t.Fatalf("RequestTotalTimeout=%v want 900ms override", cfg.RequestTotalTimeout)
	}
}
