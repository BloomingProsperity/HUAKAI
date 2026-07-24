// Phase L0 最小实现: 基于数据表的入站鉴权 resolver。
// 取代 Phase C v0.1 期间使用的 SmokeAuthResolver 路径。
//
// 当前入站 Key 流水线如下：
//
//	解析 Bearer header → 推导 16 字符 key_prefix → LookupAPIKeysByPrefix
//	(<= 5 个候选) → 对每个候选执行 bcrypt.CompareHashAndPassword → 检查
//	status + expires_at → 返回 Identity{TenantID, APIKeyID, UserID}
//
// 跨模块边界以 docs/HUAKAI工程设计手册.md 为准：
// 这是 Auth 层; 分层调用顺序为
//     Auth → Registry → Router。Resolver 不 import router, 也不调用
//     Pool/Adapter/Ledger。
// 明文 bearer 永不记录日志。出错时只返回
//     key_prefix (绝不返回后缀或完整 token) 供调试。
// 本包唯一的写操作是尽力而为的鉴权遥测:
//     成功验证后会 touch last_used_at, 且 touch
//     失败不得拒绝原本有效的凭证。
//
// 所有鉴权失败都收敛为同一个 ErrUnauthorized 返回
// (综合方案中的 D10), 这样 handler 能映射成 HTTP 401, 而不会
// 泄露枚举信号 (已吊销 vs 已过期 vs 未找到)。

package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/apikeyipallow"
	"github.com/BloomingProsperity/HUAKAI/internal/apikeyipdeny"
	"github.com/BloomingProsperity/HUAKAI/internal/apikeyns"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

// Identity 是 Resolve 产出的、已解析的入站鉴权上下文。
// 与 chat handler 填充 router.RequestContext + ledger ReserveRequest
// 所需的字段对应。字段仅在成功时填充; 绝不返回半成品值。
type Identity struct {
	TenantID int64
	APIKeyID int64
	UserID   int64
	// AllowedModels 是逗号分隔的 per-key 模型 allowlist 原始串。
	// Nil/空白表示不限制; 带模型的 ingress handler 在解析出请求模型后
	// 再执行该限制。
	AllowedModels *string
	// AllowedPoolGroupID 是服务端签发的单池约束。普通用户 Key 永远为 nil；
	// 内部运维主体可用它把一次请求钉在管理员配置的池内。
	AllowedPoolGroupID *int64
	// UserGroup 是该用户当前订阅档位 (users.user_group, 默认 'default')。
	// 供 R-SUB-WIRE-1 分组→路由的 GroupPolicyGate 在 pool 选择时限制可用渠道。
	// 空字符串视同无限制 (向后兼容老链路)。
	UserGroup string
}

// APIKeyPrefixLen 是 bearer token 中按原样存入 api_keys.key_prefix
// 并用于索引查找的前导字符数。16 字符 (覆盖 "hk_live_" 或 "hk_test_"
// 加 8 字符随机部分) 让查找足够有选择性, 从而限制前缀碰撞引发的
// bcrypt-verify-fanout。
const APIKeyPrefixLen = 16

// MaxBcryptFanout 限制单次 Resolve 调用最多对多少个候选行做 bcrypt
// 比对。SQL 查询同样 LIMIT 到该值; 这个常量的存在是为了让该上限
// 在 resolver 层也可见。
const MaxBcryptFanout = 5

// lastUsedTouchTimeout 让尽力而为的遥测不把鉴权可用性
// 耦合到 api_keys 上的行锁或慢写。
const lastUsedTouchTimeout = 100 * time.Millisecond

// ErrUnauthorized 对任何凭证级失败返回: header 错误、
// bearer 格式错误、前缀未命中、bcrypt 不匹配、key 已吊销、
// key 已过期、用户被禁用。handler 将其映射为 HTTP 401。
//
// 对外区分凭证失败的具体模式会泄露账号
// 枚举信号 (综合方案中的 D10)。运营者只能在审计日志中
// 看到这种区别。
var ErrUnauthorized = errors.New("auth: unauthorized")

// ErrForbidden 在凭证有效、但已鉴权的 key 的策略禁止该请求时返回,
// 例如 IP allowlist 未命中。handler 将其映射为
// HTTP 403。
var ErrForbidden = errors.New("auth: forbidden")

// ErrAuthMisconfigured 表示 resolver 构造时未传入
// 有效的 dbauth.Queries 句柄。handler 将其映射为 HTTP 503 (D9)。
var ErrAuthMisconfigured = errors.New("auth: resolver not configured")

// ErrAuthBackend 表示鉴权查找期间数据存储发生暂时性故障
// (PG 连接断开、查询中途 context 被取消、表缺失)。
// handler 将其映射为 HTTP 503 —— 而非 401 —— 这样在
// 基础设施中断期间, 合法客户端不会被告知其有效凭证无效。
var ErrAuthBackend = errors.New("auth: backend datastore error")

// APIKeyResolver 对照 api_keys 表对入站请求做鉴权。
// 通过 NewAPIKeyResolver 构造。
type APIKeyResolver struct {
	q                apiKeyQueries
	clientIPResolver *clientip.Resolver
}

type apiKeyQueries interface {
	LookupAPIKeysByPrefix(context.Context, string) ([]dbauth.LookupAPIKeysByPrefixRow, error)
	TouchAPIKeyLastUsed(context.Context, int64) error
}

// NewAPIKeyResolver 封装一个 sqlc.Queries 句柄。Pool/连接的
// 生命周期由调用方负责。
func NewAPIKeyResolver(q *dbauth.Queries) *APIKeyResolver {
	return &APIKeyResolver{q: q}
}

func NewAPIKeyResolverWithClientIPResolver(q *dbauth.Queries, resolver *clientip.Resolver) *APIKeyResolver {
	return &APIKeyResolver{q: q, clientIPResolver: resolver}
}

// Resolve 解析 Authorization header 并对请求做鉴权。
// 成功时返回由匹配的 api_keys 行填充的 Identity。
// 任何失败都返回 ErrUnauthorized —— 由 handler 选择
// HTTP 状态码 (ErrUnauthorized 对应 401, ErrAuthMisconfigured 对应 503)。
func (r *APIKeyResolver) Resolve(ctx context.Context, req *http.Request) (Identity, error) {
	if r == nil || r.q == nil {
		return Identity{}, ErrAuthMisconfigured
	}
	bearer, ok := parseBearer(req.Header.Get("Authorization"))
	if !ok {
		return Identity{}, ErrUnauthorized
	}
	if !validBearerFormat(bearer) {
		return Identity{}, ErrUnauthorized
	}
	if len(bearer) < APIKeyPrefixLen {
		return Identity{}, ErrUnauthorized
	}
	prefix := bearer[:APIKeyPrefixLen]

	rows, err := r.q.LookupAPIKeysByPrefix(ctx, prefix)
	if err != nil {
		// 不要把基础设施故障坍缩成凭证
		// 失败。handler 把 ErrAuthBackend 映射为 503。
		return Identity{}, fmt.Errorf("%w: lookup: %v", ErrAuthBackend, err)
	}
	now := time.Now().UTC()
	for _, row := range rows {
		if row.KeyStatus != "active" {
			continue
		}
		if row.ExpiresAt.Valid && !row.ExpiresAt.Time.After(now) {
			continue
		}
		// Tenant + user 状态通过 INNER JOIN 逐行检查
		// (deleted_at IS NULL 在 SQL 层过滤掉父记录; status 在这里
		// 强制)。总共一次 DB 往返。
		if row.UserStatus != "active" {
			continue
		}
		if row.TenantStatus != "active" {
			continue
		}
		if err := bcrypt.CompareHashAndPassword([]byte(row.KeyHash), []byte(bearer)); err != nil {
			continue
		}
		// KEY-016: deny 检查在 allowlist 之前执行 (deny 优先)。
		// ip_blacklist 为 NULL -> DeniesCSV 为 false -> 行为零变化。
		denied, err := apikeyipdeny.DeniesCSV(row.IpBlacklist, r.clientIPResolver.ClientIP(req))
		if err != nil {
			slog.WarnContext(ctx, "api_key_ip_blacklist_invalid",
				"tenant_id", row.TenantID,
				"api_key_id", row.ID,
				"error", err)
			return Identity{}, ErrForbidden
		}
		if denied {
			return Identity{}, ErrForbidden
		}
		allowed, err := apikeyipallow.AllowsCSV(row.IpAllowlist, r.clientIPResolver.ClientIP(req))
		if err != nil {
			slog.WarnContext(ctx, "api_key_ip_allowlist_invalid",
				"tenant_id", row.TenantID,
				"api_key_id", row.ID,
				"error", err)
			return Identity{}, ErrForbidden
		}
		if !allowed {
			return Identity{}, ErrForbidden
		}
		touchCtx, cancel := context.WithTimeout(ctx, lastUsedTouchTimeout)
		touchErr := r.q.TouchAPIKeyLastUsed(touchCtx, row.ID)
		cancel()
		if touchErr != nil {
			slog.WarnContext(ctx, "api_key_last_used_touch_failed",
				"tenant_id", row.TenantID,
				"api_key_id", row.ID,
				"error", touchErr)
		}
		return Identity{
			TenantID:      row.TenantID,
			APIKeyID:      row.ID,
			UserID:        row.UserID,
			AllowedModels: row.AllowedModels,
			UserGroup:     row.UserGroup,
		}, nil
	}
	return Identity{}, ErrUnauthorized
}

// parseBearer 从 "Authorization: Bearer <token>" 中提取 token。
// 当 header 缺失或格式错误时返回 ("", false)。
func parseBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	tok := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if tok == "" {
		return "", false
	}
	return tok, true
}

// validBearerFormat 校验入站 token 是否带合法客户前缀(<base>_live_/<base>_test_,
// base 默认 hk、可由 HUAKAI_API_KEY_PREFIX 覆盖,与 admin/keygen 签发同源)。
// 这是 DB 查询前的廉价过滤,拒掉明显异源 token(如 sk-...);真正鉴权是入库 bcrypt。
func validBearerFormat(token string) bool {
	return apikeyns.ValidCustomerFormat(token)
}
