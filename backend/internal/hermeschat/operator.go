package hermeschat

import "time"

// SessionOperator 是会话中真实管理员的已验证身份，不代表内部服务主体。
type SessionOperator struct {
	TenantID    int64
	ActorSource string
	ActorID     int64
	Role        string
	ExpiresAt   time.Time
}
