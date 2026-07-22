package hermesrecovery

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

func TestReplay错误只有结构性失败可终结(t *testing.T) {
	terminal := []error{
		dlq.ErrUnretryable,
		fmt.Errorf("包装错误：%w", dlq.ErrNoHandler),
	}
	for _, err := range terminal {
		if !isTerminalReplayError(err) {
			t.Fatalf("结构性错误应终结恢复：%v", err)
		}
	}

	transient := []error{
		context.DeadlineExceeded,
		context.Canceled,
		errors.New("临时数据库故障"),
	}
	for _, err := range transient {
		if isTerminalReplayError(err) {
			t.Fatalf("瞬时错误必须保留重试：%v", err)
		}
	}
}
