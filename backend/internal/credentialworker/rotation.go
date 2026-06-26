package credentialworker

import (
	"context"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

// RotationCandidate 是一个年龄足够、值得轮换的 provider-account 凭据
// (CRED-288):目前没有任何机制按计划把 key 因年龄而推入轮换,因此一个长效凭据
// 可能悄悄过期并使账号陷入降级(brownout)。定时扫描会对这类凭据分类,并把每一个
// 路由进一个恢复动作,使账号自动自愈(CRED-288c),而不是被搁置不管。
type RotationCandidate struct {
	TenantID          int64
	ProviderAccountID int64
	CredentialID      int64
	LastRefreshAt     time.Time
	// Vendor/AuthMode 驱动可刷新性分类:OAuth 式凭据可以由现有 refresh 流自愈,
	// 而静态 API key 无法刷新,必须保留在服务中(仅告警),以免它陷入无路可回的
	// 降级。
	Vendor   string
	AuthMode string
}

// RotationStore 是 rotation-due 扫描所需的最小持久化接口。刻意保持精简,使扫描
// 逻辑可以针对一个 fake 做单元测试,而生产实现是一个薄薄的 raw-pgx adapter
// (在已提交的生成码相对干净重生成已漂移期间,刻意避免新增一条 sqlc query)。
type RotationStore interface {
	// DueForRotation 返回至多 limit 条 active 的 provider-account 凭据,这些凭据
	// 上一次成功刷新严格早于 olderThan。
	DueForRotation(ctx context.Context, olderThan time.Time, limit int) ([]RotationCandidate, error)
	// MarkForRefreshRecovery 把一个可刷新(OAuth)凭据带回现有的 refresh 流,而不
	// 将其下线:它保持 'active'(因此在其 access token 仍有效期间请求继续被服务),
	// 但其 refresh_before_at 被拉到当前时刻,使 refresh 扫描在下一个 tick 接手它,
	// 并通过经过审计的 SaveRefreshSuccess 路径重新铸造 token。这就是针对"年龄大但
	// 仍可刷新"凭据的 CRED-288c 恢复闭环——它仅触碰这类凭据。
	MarkForRefreshRecovery(ctx context.Context, c RotationCandidate, refreshBeforeAt time.Time) error
	// FlagNeedsRotation 把一个候选转入 needs_rotation 状态。保留给那些无法靠一次
	// 刷新自动自愈、必须下线等待 operator 介入的凭据(为显式的 operator
	// "force-rotate" 语义保留);年龄扫描不会把静态 key 路由到这里,以免使一个仅
	// 凭年龄从未失效的 key 陷入降级。
	FlagNeedsRotation(ctx context.Context, c RotationCandidate) error
}

// RotationAlert 对每一个被扫描到的候选调用一次,使 operator 得知某个凭据因年龄
// 过期(复用 provider-account 通知管线)。可选。
type RotationAlert func(ctx context.Context, c RotationCandidate)

// RefreshClassifier 报告一个 (vendor, auth_mode) 凭据是否能由现有的 OAuth refresh
// 流自愈。返回 false 表示该凭据是 refresh 流无法重新铸造的静态密钥(API key、
// AWS SigV4 等)——这类凭据绝不能仅凭年龄就被标记下线。
type RefreshClassifier func(vendor, authMode string) bool

// DefaultRefreshClassifier 依据 canonical 的 credentialstore mode-handler registry
// 判定可刷新性:当且仅当某个 mode 的 handler 声明了 Refreshable()(每个 OAuth/session
// mode)时它才可刷新——静态 api_key / bedrock / aistudio_api_key 等 mode 不可刷新。
// 未知 mode 一律当作不可刷新(保守选择:绝不自动触碰一个我们无法分类的凭据)。
func DefaultRefreshClassifier() RefreshClassifier {
	registry := credentialstore.DefaultHandlerRegistry()
	return func(vendor, authMode string) bool {
		handler, ok := registry.Lookup(vendor, authMode)
		if !ok {
			return false
		}
		return handler.Refreshable()
	}
}

// ScanRotationDue 找出上一次刷新早于 maxAge 的凭据,把每一个路由进一个恢复动作,
// 然后告警。maxAge <= 0(或 store 为 nil)会完全禁用该扫描——它属于 opt-in 且默认
// 关闭,因此现有部署保持其确切的当前行为。返回已处理的数量。
//
// 恢复闭环(CRED-288c):一个到期的凭据不再仅仅被标记并搁置。分类器把它拆成两个
// 安全结果:
//   - 可刷新(OAuth/session):MarkForRefreshRecovery 让它保持 'active' 并把
//     refresh_before_at 拉到当前时刻,使现有的 refresh 扫描在下一个 tick 重新铸造
//     token(SaveRefreshSuccess → 新 token)。无降级,并且服务层在 access token
//     实际过期时仍会 fail-close,因此绝不会仅因为该行保持 'active' 就把一个失效
//     token 投入服务。
//   - 不可刷新(静态 API key):保留在服务中,仅告警。年龄本身绝不会使一个静态
//     key 失效,因此把它下线会使账号陷入无自动回路的降级;由 operator 带外轮换它。
//
// classifier 为 nil 时回退到 DefaultRefreshClassifier。扫描在第一个 store 错误处
// 停止,使一次瞬态 DB 故障不被吞掉。
func ScanRotationDue(ctx context.Context, store RotationStore, classifier RefreshClassifier, alert RotationAlert, maxAge time.Duration, now time.Time, limit int) (int, error) {
	if store == nil || maxAge <= 0 {
		return 0, nil
	}
	if classifier == nil {
		classifier = DefaultRefreshClassifier()
	}
	if limit <= 0 {
		limit = 100
	}
	olderThan := now.Add(-maxAge)
	cands, err := store.DueForRotation(ctx, olderThan, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, c := range cands {
		if classifier(c.Vendor, c.AuthMode) {
			// 可刷新:经由 refresh 流自愈,保留在服务中。
			if err := store.MarkForRefreshRecovery(ctx, c, now); err != nil {
				return processed, err
			}
		}
		// 不可刷新的静态 key 在此不做状态变更(仅告警)——见函数文档:
		// 避免一次无恢复路径的降级。
		processed++
		if alert != nil {
			alert(ctx, c)
		}
	}
	return processed, nil
}
