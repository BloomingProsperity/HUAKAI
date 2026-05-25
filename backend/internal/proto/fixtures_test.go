package proto

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixturesRoot 是相对于 proto 包的 fixtures 根目录；测试 cwd 即包目录。
const fixturesRoot = "fixtures"

// invalidFixturePrefix 标识故意构造的负向 fixture：文件名（basename）以该前缀开头时，
// 其期望行为是 ValidateEnvelope 报错；P-0 暂不引入此类 fixture，但 walker 已支持。
const invalidFixturePrefix = "_invalid_"

// walkFixtures 遍历 fixtures/ 下所有 .json 文件，回调每个文件的相对路径与原始字节。
// 路径自身相对于包目录（例如 "fixtures/envelope/text_minimal.json"），便于 t.Run 命名。
func walkFixtures(t *testing.T, fn func(t *testing.T, path string, raw []byte)) {
	t.Helper()
	root := fixturesRoot
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		t.Fatalf("fixtures root %q not found: %v", root, err)
	}
	count := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		count++
		fn(t, path, raw)
		return nil
	})
	if err != nil {
		t.Fatalf("walk fixtures: %v", err)
	}
	if count == 0 {
		t.Fatalf("no fixture .json found under %q", root)
	}
}

// isInvalidFixture 通过文件名（basename）判断是否为负向 fixture；任何非负向 fixture 都期望
// unmarshal + ValidateEnvelope 双绿。
func isInvalidFixture(path string) bool {
	return strings.HasPrefix(filepath.Base(path), invalidFixturePrefix)
}

// TestFixtures_AllValidate 遍历 fixtures/ 下所有 JSON：
//   - 必须是合法 JSON 文档
//   - 必须能 unmarshal 到 *HCSFEnvelope
//   - 非 _invalid_ 前缀文件必须通过 ValidateEnvelope
//   - _invalid_ 前缀文件必须 ValidateEnvelope 报错（保留负向 fixture 入口）
func TestFixtures_AllValidate(t *testing.T) {
	walkFixtures(t, func(t *testing.T, path string, raw []byte) {
		t.Run(path, func(t *testing.T) {
			if !json.Valid(raw) {
				t.Fatalf("fixture %s is not valid JSON", path)
			}
			var env HCSFEnvelope
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatalf("unmarshal %s: %v", path, err)
			}
			err := ValidateEnvelope(&env)
			if isInvalidFixture(path) {
				if err == nil {
					t.Fatalf("fixture %s expected ValidateEnvelope error (negative fixture), got nil", path)
				}
				return
			}
			if err != nil {
				t.Fatalf("fixture %s ValidateEnvelope failed: %v", path, err)
			}
		})
	})
}

// TestFixtures_RoundTripStable 验证每个非负向 fixture：
// marshal → unmarshal → marshal 字节序列稳定（INV-1 + INV-2 round-trip 不变）。
func TestFixtures_RoundTripStable(t *testing.T) {
	walkFixtures(t, func(t *testing.T, path string, raw []byte) {
		if isInvalidFixture(path) {
			return
		}
		t.Run(path, func(t *testing.T) {
			var env HCSFEnvelope
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatalf("unmarshal %s: %v", path, err)
			}
			first, err := json.Marshal(&env)
			if err != nil {
				t.Fatalf("marshal #1 %s: %v", path, err)
			}
			var env2 HCSFEnvelope
			if err := json.Unmarshal(first, &env2); err != nil {
				t.Fatalf("unmarshal #2 %s: %v", path, err)
			}
			second, err := json.Marshal(&env2)
			if err != nil {
				t.Fatalf("marshal #2 %s: %v", path, err)
			}
			if string(first) != string(second) {
				t.Fatalf("INV-1 round-trip drift in %s:\n#1=%s\n#2=%s", path, first, second)
			}
		})
	})
}
