package modulecatalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// embeddedCatalog 是已签入的生成产物。它被嵌入(而非在启动时从磁盘读取),
// 这样网关二进制在运行时对 docs/ 树没有任何依赖,并能从任意工作目录运行。
// 编辑 feature tree 后用 `go run ./cmd/modulecatalog-gen` 重新生成它;若此文件
// 与 feature tree 发生漂移,陈旧性守卫测试会失败。
//
//go:embed module-catalog.json
var embeddedCatalog []byte

var (
	loadOnce sync.Once
	loaded   Catalog
	loadErr  error
)

// Load 返回嵌入的静态 catalog,只解析一次并缓存。
func Load() (Catalog, error) {
	loadOnce.Do(func() {
		loadErr = json.Unmarshal(embeddedCatalog, &loaded)
		if loadErr != nil {
			loadErr = fmt.Errorf("modulecatalog: parse embedded catalog: %w", loadErr)
		}
	})
	return loaded, loadErr
}

// MustLoad 返回嵌入的 catalog,若解析失败则返回一个空 catalog。供接线使用,
// 在那里 catalog 解析失败不能中止启动 —— 即便没有静态覆盖层,实时 registry
// 仍能工作。
func MustLoad() Catalog {
	c, err := Load()
	if err != nil {
		return Catalog{}
	}
	return c
}
