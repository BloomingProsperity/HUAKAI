package hermeschat

import (
	"sync"
	"time"
)

// session_binding.go 解决会话式只读工具循环(WAVE H3b)的核心安全问题:runner 的
// internal_token 只携带 tenant|user|request_id|exp——并不包含 operator 的 admin role
// 或 token id。而 RBAC role 下限与 operator 归属审计行都需要这些信息。
//
// 与其加宽 internal_token(那会改变 runner 契约 + Python 验证器并扩大信任面),gateway
// 改为维护一个进程内绑定,以 internal_token 中已有的 request_id 为键。PrepareRequest 在
// 同一步骤里铸造 request_id 和与之匹配的 internal_token;startChat 在调用 runner 之前把
// operator 身份绑定到该 request_id。当 runner 调用内部只读 tool-execute 端点时,它出示
// internal_token(证明它是本会话的 runner);端点验证该 token、提取 request_id,并在此
// 解析出已绑定的 operator 身份。
//
// 因此,token 认证的是会话,绑定提供的是 operator 的 role + 范围。该绑定是 fail-closed 的:
// 没有绑定(或绑定已过期)意味着内部调用被拒绝——工具绝不会在缺少已解析 operator 身份的
// 情况下运行,因而也绝不会越过该 operator 的 role 下限或 tenant 范围。

// SessionOperator 是绑定到单个聊天会话的 operator 身份。它镜像 H3 HTTP 路径从 admin token
// 推导出的 admin actor,使内部 tool-execute 路径强制执行与显式 operator 驱动端点相同的
// role 下限 + tenant 范围 + 审计归属。
type SessionOperator struct {
	// TenantID 是会话经范围校验后的 tenant(H1 中间件的 CanIssueForTenant 已授权该
	// operator 可操作的 tenant)。每次会话式工具调用都被钉死在此 tenant 上——工具绝不
	// 能通过会话读取另一个 tenant 的数据。
	TenantID int64
	// ActorUserID 是 operator 在其 Hermes ops 上下文中所代表操作的 tenant 用户(来自 H1
	// 中间件的 ?as_user_id)。记录为工具调用的 actor_user_id,使既有的 tenant 隔离语义
	// 得以延续。
	ActorUserID int64
	// AdminActorTokenID 是 operator 的 admin_tokens 行 id,记录为工具调用的
	// admin_actor_token_id 以做 operator 归属。0 表示未绑定任何 admin actor(此时绑定
	// 会被拒绝——会话式路径仅限 admin)。
	AdminActorTokenID int64
	// Role 是 operator 的 admin role(platform_admin / tenant_operator)。它是用于对照
	// 每个工具的 RequiredRole 的 RBAC role 下限。空 role 会让每个工具都失败(roleRank 0)。
	Role string
	// ExpiresAt 限定绑定的生命周期,使泄露的 request_id 无法被无限期重放。设置为
	// internal_token 的过期时间,使绑定与 token 同时过期。
	ExpiresAt time.Time
}

// SessionBindings 是一个进程内、以 request_id 为键的活跃聊天会话 operator 身份存储。
// 它是并发安全的,并在 lookup/insert 时自我清理。单个 gateway 进程同时拥有创建绑定的
// 聊天请求和读取它的内部 tool-execute 回调(runner 通过 internal_base_url 回调到同一个
// gateway),因此进程内存储已足够,且可避免持久化 operator 身份。
type SessionBindings struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]SessionOperator
}

// NewSessionBindings 构建一个空的绑定存储。now 默认使用 UTC 墙上时钟;测试会注入固定时钟。
func NewSessionBindings(now func() time.Time) *SessionBindings {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &SessionBindings{now: now, m: make(map[string]SessionOperator)}
}

// Bind 为一个会话 request_id 记录 operator 身份,返回是否成功绑定。
//
// request_id 受客户端影响(hermeshttp 路由的 requestID(r) 会回退到客户端 X-Request-Id 头),
// 因此“每次聊天 request_id 唯一”并非代码所能保证的前提。若无条件 last-writer-wins,同租户、
// 同 as_user_id 的两个 admin operator 用同一 request_id 并发开聊时,后写者会覆盖先写者的绑定;
// 由于 resolveOperator 的一致性检查只比对 tenant/user,先写者的有效 internal_token 便会解析出
// 后写者的 Role + AdminActorTokenID —— 跨越 role 下限与 operator 归属(B21 [S3])。
//
// 因此 Bind 对同一 request_id 上的【身份冲突】fail-closed:若已存在一条未过期、且属于【不同
// operator 身份】的绑定,则不覆盖,反而作废(poison)该键并返回 false —— 使任一会话的 token
// 都无法凭此 request_id 解析出 operator(双方的工具循环都被闸死,回落到无工具的普通聊天)。
// 同一 operator 身份的重绑(同会话重试 prepare)仍幂等允许,用于刷新过期时间。空白的
// request_id 会被忽略(调用方在上游已校验;这里是纵深防御)。
func (s *SessionBindings) Bind(requestID string, op SessionOperator) bool {
	if s == nil || requestID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	if existing, ok := s.m[requestID]; ok && !sameOperatorIdentity(existing, op) {
		// 身份冲突:同一 request_id 被不同 operator 抢占。既不能保留先写者(会让后写者的
		// token 骑在先写者身份上),也不能覆盖(会让先写者的 token 骑在后写者身份上)——
		// 唯一安全的选择是作废该键,让双方都 fail-closed。
		delete(s.m, requestID)
		return false
	}
	s.m[requestID] = op
	return true
}

// sameOperatorIdentity 判断两条绑定是否属于同一 operator 身份。用于区分“同会话幂等重绑”
// (允许,刷新过期时间)与“不同 operator 的 request_id 冲突”(fail-closed 作废)。比较承载
// 越权风险的全部身份字段:tenant 范围、actor 用户、admin actor token id、以及 RBAC role。
// 不比较 ExpiresAt —— 过期时间随 token 刷新而变,不属于身份。
func sameOperatorIdentity(a, b SessionOperator) bool {
	return a.TenantID == b.TenantID &&
		a.ActorUserID == b.ActorUserID &&
		a.AdminActorTokenID == b.AdminActorTokenID &&
		a.Role == b.Role
}

// Lookup 返回绑定到 requestID 的 operator,以及它是否存在且未过期。已过期的绑定被视为
// 不存在(并被移除),使陈旧的会话绝不能授权工具。空白的 request_id 永不匹配。
func (s *SessionBindings) Lookup(requestID string) (SessionOperator, bool) {
	if s == nil || requestID == "" {
		return SessionOperator{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.m[requestID]
	if !ok {
		return SessionOperator{}, false
	}
	if !op.ExpiresAt.IsZero() && !s.now().UTC().Before(op.ExpiresAt.UTC()) {
		delete(s.m, requestID)
		return SessionOperator{}, false
	}
	return op, true
}

// Release 移除 requestID 的绑定。startChat 在流结束时调用它,使绑定即便在过期之前也不会
// 比其会话存活更久。
func (s *SessionBindings) Release(requestID string) {
	if s == nil || requestID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, requestID)
}

// pruneLocked 丢弃已过期的绑定。在 insert 时持锁调用,使 map 不会因那些从未走到 Release
// 的会话(例如连接断开)而无限增长。复杂度 O(n),但 n 是并发活跃聊天的数量。
func (s *SessionBindings) pruneLocked() {
	now := s.now().UTC()
	for k, op := range s.m {
		if !op.ExpiresAt.IsZero() && !now.Before(op.ExpiresAt.UTC()) {
			delete(s.m, k)
		}
	}
}
