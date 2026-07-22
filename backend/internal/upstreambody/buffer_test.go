package upstreambody

import (
	"errors"
	"strings"
	"testing"
)

func TestReadBounded(t *testing.T) {
	t.Run("恰好上限", func(t *testing.T) {
		want := strings.Repeat("a", MaxBufferedResponseBytes)
		got, oversized, err := ReadBounded(strings.NewReader(want))
		if err != nil {
			t.Fatalf("ReadBounded() error = %v", err)
		}
		if oversized {
			t.Fatal("恰好上限不应被标记为超限")
		}
		if string(got) != want {
			t.Fatalf("读取内容不一致：len(got)=%d len(want)=%d", len(got), len(want))
		}
	})

	t.Run("超出一字节", func(t *testing.T) {
		got, oversized, err := ReadBounded(strings.NewReader(strings.Repeat("b", MaxBufferedResponseBytes+1)))
		if err != nil {
			t.Fatalf("ReadBounded() error = %v", err)
		}
		if !oversized {
			t.Fatal("超出一字节必须被标记为超限")
		}
		if len(got) != MaxBufferedResponseBytes {
			t.Fatalf("len(got)=%d，want=%d", len(got), MaxBufferedResponseBytes)
		}
	})

	t.Run("读取失败", func(t *testing.T) {
		want := errors.New("读取失败")
		_, oversized, err := ReadBounded(errorReader{err: want})
		if !errors.Is(err, want) {
			t.Fatalf("err=%v，want=%v", err, want)
		}
		if oversized {
			t.Fatal("读取失败不能被伪装成超限")
		}
	})
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}
