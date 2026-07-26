// HUAKAI · iKun

package routeadmin

import (
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// 守操作日志不能接受零值、伪造来源或未知角色。
// mutation: 只检查 ActorID 非空 → admin_user:0 和 arbitrary:9 会被放行，本测试转红。
func TestValidateMutationLogRejectsUntraceableActor(t *testing.T) {
	valid := []MutationLog{
		{ActorID: "admin_token:7", ActorRole: admin.RolePlatformAdmin},
		{ActorID: "admin_user:8", ActorRole: admin.RoleTenantOperator},
	}
	for _, log := range valid {
		if err := validateMutationLog(log); err != nil {
			t.Fatalf("valid operation actor rejected: %+v err=%v", log, err)
		}
	}

	invalid := []MutationLog{
		{ActorID: "", ActorRole: admin.RolePlatformAdmin},
		{ActorID: "admin_user:0", ActorRole: admin.RolePlatformAdmin},
		{ActorID: "arbitrary:9", ActorRole: admin.RolePlatformAdmin},
		{ActorID: "admin_token:not-a-number", ActorRole: admin.RolePlatformAdmin},
		{ActorID: "admin_user:8", ActorRole: "user"},
	}
	for _, log := range invalid {
		if err := validateMutationLog(log); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid operation actor accepted: %+v err=%v", log, err)
		}
	}
}
