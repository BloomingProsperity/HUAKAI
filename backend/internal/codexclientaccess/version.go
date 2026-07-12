package codexclientaccess

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var engineVersionHeadPattern = regexp.MustCompile(`^(\d+\.\d+\.\d+)`)

// ParseEngineVersion 从客户端 User-Agent 中提取三段式引擎版本。
// 解析只取第一个 "/" 后到首个空白或 "(" 前的片段,再从片段开头提取 X.Y.Z;
// 预发布后缀会被忽略,缺少三段数字则视为不可检测。
func ParseEngineVersion(ua string) (string, bool) {
	value := strings.TrimSpace(ua)
	slash := strings.Index(value, "/")
	if slash < 0 {
		return "", false
	}

	part := value[slash+1:]
	if end := strings.IndexFunc(part, func(r rune) bool {
		return unicode.IsSpace(r) || r == '('
	}); end >= 0 {
		part = part[:end]
	}
	part = strings.TrimSpace(part)

	matches := engineVersionHeadPattern.FindStringSubmatch(part)
	if len(matches) < 2 {
		return "", false
	}
	return matches[1], true
}

// CompareVersions 按数字点分段比较两个宽松版本字符串。
// 可选前缀 v 会被忽略,第一个 "-" 或 "+" 之后的后缀会被截断;
// 非数字段按 0 处理,缺失分段补 0。
func CompareVersions(a, b string) int {
	left := versionPartsForCompare(a)
	right := versionPartsForCompare(b)

	n := len(left)
	if len(right) > n {
		n = len(right)
	}
	for i := 0; i < n; i++ {
		lv := versionPartAt(left, i)
		rv := versionPartAt(right, i)
		switch {
		case lv < rv:
			return -1
		case lv > rv:
			return 1
		}
	}
	return 0
}

func versionPartsForCompare(v string) []string {
	value := strings.TrimSpace(v)
	value = strings.TrimPrefix(value, "v")
	value = strings.TrimPrefix(value, "V")
	if cut := strings.IndexAny(value, "-+"); cut >= 0 {
		value = value[:cut]
	}
	if value == "" {
		return nil
	}
	return strings.Split(value, ".")
}

func versionPartAt(parts []string, index int) int {
	if index >= len(parts) {
		return 0
	}
	n, err := strconv.Atoi(parts[index])
	if err != nil {
		return 0
	}
	return n
}
