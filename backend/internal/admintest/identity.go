// Package admintest 为跨包测试提供经受控入口构造的运维身份。
// 生产代码不得依赖本包；它只用于替换测试里对身份私有作用域的直接赋值。
package admintest

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// Platform 返回平台全域测试身份。使用函数值可避免生产 deadcode 扫描把
// 仅由外部测试包调用的辅助入口误判为生产死代码。
var Platform = func(tokenID int64) admin.AdminIdentity {
	return mustIdentity(admin.IdentityClaims{TokenID: tokenID, Role: admin.RolePlatformAdmin}, false)
}

// TenantOperator 返回根租户运维身份，保持既有单租户部署的控制面语义。
var TenantOperator = func(tokenID, tenantID int64) admin.AdminIdentity {
	return mustIdentity(admin.IdentityClaims{
		TokenID: tokenID, Role: admin.RoleTenantOperator, ScopeTenantID: tenantID,
	}, false)
}

// Reseller 返回子租户分销商身份；descendants 是允许访问的后代租户。
var Reseller = func(tokenID, tenantID int64, descendants ...int64) admin.AdminIdentity {
	return mustIdentity(admin.IdentityClaims{
		TokenID: tokenID, Source: admin.AdminSourceToken,
		Role: admin.RoleTenantOperator, ScopeTenantID: tenantID,
	}, true, descendants...)
}

// ResellerSession 返回子租户分销商的用户会话身份。
var ResellerSession = func(userID, tenantID int64, descendants ...int64) admin.AdminIdentity {
	return mustIdentity(admin.IdentityClaims{
		UserID: userID, Source: admin.AdminSourceSession,
		Role: admin.RoleTenantOperator, ScopeTenantID: tenantID,
	}, true, descendants...)
}

var mustIdentity = func(claims admin.IdentityClaims, rootIsChild bool, descendants ...int64) admin.AdminIdentity {
	nodes := []admin.TenantScopeNode{{
		TenantID: claims.ScopeTenantID, Depth: 0, ScopeRootIsChild: rootIsChild,
	}}
	for index, tenantID := range descendants {
		nodes = append(nodes, admin.TenantScopeNode{
			TenantID: tenantID, Depth: int32(index + 1), ScopeRootIsChild: rootIsChild,
		})
	}
	var loader admin.TenantScopeLoader
	if claims.ScopeTenantID > 0 {
		loader = func(context.Context, int64) ([]admin.TenantScopeNode, error) {
			return nodes, nil
		}
	}
	identity, err := admin.NewAdminIdentity(context.Background(), claims, loader)
	if err != nil {
		panic("构造测试运维身份失败: " + err.Error())
	}
	return identity
}
