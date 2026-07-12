package main

import (
	"os"
	"strings"
)

// autoMigrateEnabled 读 HUAKAI_AUTO_MIGRATE,决定启动时是否进程内自迁移。
//
// 默认 false:迁移保持外置(compose 的 migrate one-shot 受控跑,多副本时避免竞态)。
// 设 true(大小写不敏感的 "true")开启进程内自迁移,用于裸二进制单实例部署省去手动迁移步骤。
// 与 HUAKAI_REQUIRE_EMAIL_GATE 同款只认 "true" 的读法。
func autoMigrateEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("HUAKAI_AUTO_MIGRATE")), "true")
}
