package trustreceipt

import (
	"crypto/sha256"
	"strings"
	"testing"
)

// TestReceiptIDFormat
//
// 守 lookup id 格式：request_id 与 sequence 必须用冒号拼接。
// Mutation 自检：把分隔符改成 "/" 或省略 sequence，本测试会 red。
func TestReceiptIDFormat(t *testing.T) {
	if got, want := ReceiptID("req_abc", 3), "req_abc:3"; got != want {
		t.Fatalf("ReceiptID=%q want %q", got, want)
	}
}

// TestReceiptIDRejectsEmptyRequestID
//
// 守 request_id 为空不得产生可 lookup 的 receipt id。
// Mutation 自检：空 request_id 仍返回 ":0"，本测试会 red。
func TestReceiptIDRejectsEmptyRequestID(t *testing.T) {
	if got := ReceiptID("", 0); got != "" {
		t.Fatalf("ReceiptID empty request_id=%q want empty", got)
	}
}

// TestDisplayReceiptIDLength32
//
// 守 display id 只暴露 sha256 前 32 hex chars，前缀固定 receipt_。
// Mutation 自检：改前缀或 hex 截断长度，本测试会 red。
func TestDisplayReceiptIDLength32(t *testing.T) {
	hash := sha256.Sum256([]byte("canonical payload fixture"))
	got := DisplayReceiptID(hash)
	if len(got) != 40 {
		t.Fatalf("DisplayReceiptID length=%d want 40: %q", len(got), got)
	}
	if !strings.HasPrefix(got, "receipt_") {
		t.Fatalf("DisplayReceiptID prefix=%q want receipt_: %q", got[:8], got)
	}
	if got != "receipt_217024fe8323d824a141f0f275c5b301" {
		t.Fatalf("DisplayReceiptID=%q want fixed first 32 hex chars", got)
	}
}
