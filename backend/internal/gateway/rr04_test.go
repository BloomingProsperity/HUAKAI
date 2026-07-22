// RR-04 测试:EnforceCacheControlLimit
package gateway

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/cachecontrol"
)

// ---- RR-04 测试辅助函数 ----

// buildRR04Body 构造一个 Messages body,参数:
//
//	systemCC:  携带 cache_control 的 system block 数(总是排在最前)
//	msgCC:     携带 cache_control 的 message content block 数(按序追加)
//
// 总 CC 数 = systemCC + msgCC。
func buildRR04Body(t *testing.T, systemCC, systemTotal, msgCC, msgTotal int) []byte {
	t.Helper()

	// 构造 system 数组。
	systemBlocks := make([]interface{}, systemTotal)
	for i := 0; i < systemTotal; i++ {
		b := map[string]interface{}{"type": "text", "text": "sys"}
		if i < systemCC {
			b["cache_control"] = map[string]interface{}{"type": "ephemeral"}
		}
		systemBlocks[i] = b
	}

	// 构造一条带 msgTotal 个 content block 的 message。
	contentBlocks := make([]interface{}, msgTotal)
	for i := 0; i < msgTotal; i++ {
		b := map[string]interface{}{"type": "text", "text": "msg"}
		if i < msgCC {
			b["cache_control"] = map[string]interface{}{"type": "ephemeral"}
		}
		contentBlocks[i] = b
	}

	root := map[string]interface{}{
		"model":      "claude-opus-4-6",
		"max_tokens": 64,
		"system":     systemBlocks,
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": contentBlocks,
			},
		},
	}

	out, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("buildRR04Body: marshal: %v", err)
	}
	return out
}

// ---- 测试:5 个断点 -> 裁剪到 4 个,移除最后一个 ----

func TestEnforceCacheControlLimit_5to4(t *testing.T) {
	// 1 个 system CC + 4 个 message CC block = 共 5 个。
	// enforce 后:保留前 4 个(1 个 system + 3 个 msg),最后一个 msg block 失去 CC。
	body := buildRR04Body(t, 1, 1, 4, 4)

	// 校验前置条件。
	snap, err := cachecontrol.InspectCacheControl(body)
	if err != nil {
		t.Fatalf("precondition inspect: %v", err)
	}
	if snap.Count != 5 {
		t.Fatalf("precondition: want 5 CC blocks, got %d", snap.Count)
	}

	out, err := cachecontrol.EnforceCacheControlLimit(body, cachecontrol.CacheControlMaxAllowed)
	if err != nil {
		t.Fatalf("EnforceCacheControlLimit error: %v", err)
	}

	snapAfter, err := cachecontrol.InspectCacheControl(out)
	if err != nil {
		t.Fatalf("post-enforce inspect: %v", err)
	}
	if snapAfter.Count != 4 {
		t.Fatalf("after enforce: want 4 CC blocks, got %d", snapAfter.Count)
	}

	// 最后一个(第 4 个,0 基 index=3)message content block 应已失去 cache_control。
	// 直接解析并校验。
	var root map[string]interface{}
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal out: %v", err)
	}
	msgs := root["messages"].([]interface{})
	msg0 := msgs[0].(map[string]interface{})
	content := msg0["content"].([]interface{})
	if len(content) != 4 {
		t.Fatalf("expected 4 content blocks, got %d", len(content))
	}
	// block 0、1、2 应保留 CC(对应原始 5 个 CC 序列里的 index 1、2、3 ——
	// 因为那 1 个 system CC block 占用了 slot 0)。
	for i := 0; i < 3; i++ {
		blk := content[i].(map[string]interface{})
		if _, has := blk["cache_control"]; !has {
			t.Errorf("content[%d] should still have cache_control (kept)", i)
		}
	}
	// index 3 的 block 是被移除的那个。
	blkLast := content[3].(map[string]interface{})
	if _, has := blkLast["cache_control"]; has {
		t.Errorf("content[3] should have cache_control REMOVED (excess), but still has it")
	}
}

// ---- 测试:恰好 4 个 -> 逐字节相同 ----

func TestEnforceCacheControlLimit_Exactly4_ByteIdentical(t *testing.T) {
	body := buildRR04Body(t, 1, 1, 3, 3) // 1+3=4

	snap, err := cachecontrol.InspectCacheControl(body)
	if err != nil {
		t.Fatalf("precondition: %v", err)
	}
	if snap.Count != 4 {
		t.Fatalf("precondition: want 4, got %d", snap.Count)
	}

	out, err := cachecontrol.EnforceCacheControlLimit(body, cachecontrol.CacheControlMaxAllowed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Equal(body, out) {
		t.Fatalf("expected byte-identical output for exactly-4 input\nin:  %s\nout: %s", body, out)
	}
}

// ---- 测试:0 个断点 -> 逐字节相同 ----

func TestEnforceCacheControlLimit_Zero_ByteIdentical(t *testing.T) {
	body := buildRR04Body(t, 0, 2, 0, 2) // 0 个 CC

	out, err := cachecontrol.EnforceCacheControlLimit(body, cachecontrol.CacheControlMaxAllowed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(body, out) {
		t.Fatalf("expected byte-identical output for 0-CC input\nin:  %s\nout: %s", body, out)
	}
}

// ---- 测试:2 个断点 -> 逐字节相同 ----

func TestEnforceCacheControlLimit_Two_ByteIdentical(t *testing.T) {
	body := buildRR04Body(t, 0, 1, 2, 2) // 0+2=2

	out, err := cachecontrol.EnforceCacheControlLimit(body, cachecontrol.CacheControlMaxAllowed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(body, out) {
		t.Fatalf("expected byte-identical for 2-CC input\nin:  %s\nout: %s", body, out)
	}
}

// ---- 测试:畸形 JSON -> 原样透传不变 ----

func TestEnforceCacheControlLimit_MalformedJSON_Passthrough(t *testing.T) {
	bad := []byte(`{not valid json`)

	out, err := cachecontrol.EnforceCacheControlLimit(bad, cachecontrol.CacheControlMaxAllowed)
	// 必须原样返回原始字节。
	if !bytes.Equal(bad, out) {
		t.Fatalf("malformed JSON: expected original bytes back\ngot: %s", out)
	}
	// 错误必须非 nil。
	if err == nil {
		t.Fatalf("malformed JSON: expected non-nil error, got nil")
	}
}
