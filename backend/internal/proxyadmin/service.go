package proxyadmin

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/proxysecret"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Querier interface {
	CreateProxy(context.Context, admindb.CreateProxyParams) (admindb.CreateProxyRow, error)
	UpdateProxy(context.Context, admindb.UpdateProxyParams) (admindb.UpdateProxyRow, error)
	GetProxy(context.Context, admindb.GetProxyParams) (admindb.GetProxyRow, error)
	ListProxiesByTenant(context.Context, int64) ([]admindb.ListProxiesByTenantRow, error)
	SetProxyStatus(context.Context, admindb.SetProxyStatusParams) error
	SoftDeleteProxy(context.Context, admindb.SoftDeleteProxyParams) error
}

type Service struct {
	q    Querier
	keys credentialstore.KeyProvider
}

func New(q Querier, keys credentialstore.KeyProvider) *Service {
	return &Service{q: q, keys: keys}
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Proxy, error) {
	if err := validateCreate(in); err != nil {
		return Proxy{}, err
	}
	secret, err := s.encryptAuthSecret(ctx, in.TenantID, in.AuthSecret)
	if err != nil {
		return Proxy{}, err
	}
	row, err := s.q.CreateProxy(ctx, admindb.CreateProxyParams{
		TenantID:     in.TenantID,
		Name:         strings.TrimSpace(in.Name),
		Protocol:     strings.TrimSpace(in.Protocol),
		Host:         strings.TrimSpace(in.Host),
		Port:         in.Port,
		AuthUsername: cleanPtr(in.AuthUsername),
		AuthSecret:   secret,
		GroupID:      normalizeGroupID(in.GroupID),
		Status:       statusOrActive(in.Status),
	})
	if err != nil {
		return Proxy{}, mapErr(err)
	}
	return fromCreate(row), nil
}

func (s *Service) Update(ctx context.Context, in UpdateInput) (Proxy, error) {
	if err := validateUpdate(in); err != nil {
		return Proxy{}, err
	}
	secret, err := s.encryptAuthSecret(ctx, in.TenantID, in.AuthSecret)
	if err != nil {
		return Proxy{}, err
	}
	row, err := s.q.UpdateProxy(ctx, admindb.UpdateProxyParams{
		TenantID:     in.TenantID,
		ID:           in.ID,
		Name:         strings.TrimSpace(in.Name),
		Protocol:     strings.TrimSpace(in.Protocol),
		Host:         strings.TrimSpace(in.Host),
		Port:         in.Port,
		AuthUsername: cleanPtr(in.AuthUsername),
		AuthSecret:   secret,
		GroupID:      normalizeGroupID(in.GroupID),
	})
	if err != nil {
		return Proxy{}, mapErr(err)
	}
	return fromUpdate(row), nil
}

// List 返回某租户全部未删除的代理,不含凭据。底层行上加密的 auth_secret
// 永远不会被映射进结果。
func (s *Service) List(ctx context.Context, tenantID int64) ([]Proxy, error) {
	if tenantID <= 0 {
		return nil, ErrInvalidInput
	}
	rows, err := s.q.ListProxiesByTenant(ctx, tenantID)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Proxy, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromList(r))
	}
	return out, nil
}

// Get 返回单个按租户收敛的代理,不含凭据。不存在或跨租户的 id
// 会得到 ErrNotFound(查询本身按 tenant_id 过滤)。
func (s *Service) Get(ctx context.Context, tenantID, id int64) (Proxy, error) {
	if tenantID <= 0 || id <= 0 {
		return Proxy{}, ErrInvalidInput
	}
	row, err := s.q.GetProxy(ctx, admindb.GetProxyParams{TenantID: tenantID, ID: id})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Proxy{}, ErrNotFound
		}
		return Proxy{}, mapErr(err)
	}
	return fromGet(row), nil
}

// Delete 对按租户收敛的代理执行软删除。底层 UPDATE 同时按租户与"尚未删除"收敛;
// 它是幂等的(再删一次是 no-op)。
func (s *Service) Delete(ctx context.Context, tenantID, id int64) error {
	if tenantID <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	if err := s.q.SoftDeleteProxy(ctx, admindb.SoftDeleteProxyParams{TenantID: tenantID, ID: id}); err != nil {
		return mapErr(err)
	}
	return nil
}

// SetStatus 为某租户翻转代理的生命周期状态(active/disabled/dead)并打上
// last_check_at 时间戳。非法的 status 值在写入前即被拒绝。
func (s *Service) SetStatus(ctx context.Context, tenantID, id int64, status string) error {
	if tenantID <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	status = strings.TrimSpace(status)
	if !validStatus(status) {
		return ErrInvalidStatus
	}
	if err := s.q.SetProxyStatus(ctx, admindb.SetProxyStatusParams{Status: status, TenantID: tenantID, ID: id}); err != nil {
		return mapErr(err)
	}
	return nil
}

func (s *Service) encryptAuthSecret(ctx context.Context, tenantID int64, raw *string) (*string, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	stored, err := proxysecret.Encode(ctx, s.keys, tenantID, *raw)
	if err != nil {
		return nil, fmt.Errorf("%w: encrypt proxy auth_secret: %v", ErrBackend, err)
	}
	return &stored, nil
}

func validateCreate(in CreateInput) error {
	if err := validateCommon(in.TenantID, 1, in.Name, in.Protocol, in.Host, in.Port, in.Status); err != nil {
		return err
	}
	return validateGroupID(in.GroupID)
}

// ValidateCreateInput 供需要与其他领域写入保持同一事务的受控导入器复用代理校验。
func ValidateCreateInput(in CreateInput) error {
	return validateCreate(in)
}

func validateUpdate(in UpdateInput) error {
	if err := validateCommon(in.TenantID, in.ID, in.Name, in.Protocol, in.Host, in.Port, "active"); err != nil {
		return err
	}
	return validateGroupID(in.GroupID)
}

var proxyGroupIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{0,64}$`)

// validateGroupID 把代理组标识限制在可安全比较、可稳定输入的 ASCII 子集内。
// nil 与空串都表示未分组；非空值必须完整匹配，不能靠截断或清洗掩盖非法输入。
func validateGroupID(groupID *string) error {
	if groupID == nil || proxyGroupIDPattern.MatchString(*groupID) {
		return nil
	}
	return ErrInvalidInput
}

// normalizeGroupID 在写入前把空串规格化为 NULL，避免同一“未分组”状态出现两种存储值。
func normalizeGroupID(groupID *string) *string {
	if groupID == nil || *groupID == "" {
		return nil
	}
	normalized := *groupID
	return &normalized
}

func validateCommon(tenantID, id int64, name, protocol, host string, port int32, status string) error {
	if tenantID <= 0 || id <= 0 || strings.TrimSpace(name) == "" || strings.TrimSpace(protocol) == "" || strings.TrimSpace(host) == "" || port <= 0 || port > 65535 {
		return ErrInvalidInput
	}
	if !validStatus(statusOrActive(status)) {
		return ErrInvalidStatus
	}
	// SSRF 静态防护:挡管理员把租户代理 host 指向【绝不可能是合法代理】的目标
	// (云 metadata 端点 / loopback / link-local / unspecified / multicast)。
	// 刻意【放行】RFC1918 私网与 .internal 类主机名——企业/内网出口代理本就常驻
	// 私网,封死会误伤正常配置。Create/Update 两条写路径单点覆盖。
	if !proxyHostSafe(host) {
		return ErrUnsafeHost
	}
	return nil
}

// 永不可能是合法代理目标的 metadata / 本机主机名(精确匹配,不做后缀封禁,
// 以免误伤 proxy.internal 这类合法企业代理主机名)。
var blockedProxyHostnames = map[string]bool{
	"localhost":                  true,
	"localhost.localdomain":      true,
	"metadata":                   true,
	"metadata.google.internal":   true,
	"metadata.goog":              true,
	"instance-data":              true,
	"instance-data.ec2.internal": true,
}

// 落在私网段、逃过 link-local 检测、但确是真实可达云 metadata 端点的 IP。
// IPv4 169.254.169.254 已被 IsLinkLocalUnicast 覆盖;此处补 IPv6 ULA 形式——
// fd00:ec2::254 是 AWS IMDS-over-IPv6,落在 fc00::/7 私网段、IsPrivate=true、
// 非 link-local,故不在通用私网放行里特判挡掉(挡 metadata 是本守卫核心目标,
// 且无任何合法代理会驻该地址,零误伤)。
var blockedMetadataIPs = []netip.Addr{
	netip.MustParseAddr("fd00:ec2::254"),
}

// proxyHostSafe 判定一个【裸主机名/IP】能否作为租户代理目标。
// 阻断面刻意收窄成"绝无合法用途"的集合:
//   - IP 字面量:loopback(127/8、::1)、link-local(169.254/16 含云 metadata IP、
//     fe80::/10)、unspecified(0.0.0.0、::)、multicast,外加 blockedMetadataIPs
//     里的非 link-local 云 metadata 地址。私网(10/172.16/192.168、fc00::/7)与
//     公网一律放行。
//   - 主机名:仅精确匹配 metadata/本机名单(见 blockedProxyHostnames),其余放行。
//
// 注:这是写时静态校验,不做 DNS 解析(无法挡 rebinding);代理目标本就 admin-gated,
// 此处是纵深防御。若未来允许租户管理员自行配置代理,是否进一步【默认封私网 /
// CGNAT 100.64.0.0/10 等 special-use】属于租户能力授权与网络边界决策,不在本切片。
func proxyHostSafe(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	if h == "" {
		return false
	}
	if addr, err := netip.ParseAddr(h); err == nil {
		addr = addr.Unmap()
		for _, m := range blockedMetadataIPs {
			if addr == m {
				return false
			}
		}
		if addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
			addr.IsMulticast() || addr.IsUnspecified() {
			return false
		}
		return true
	}
	return !blockedProxyHostnames[h]
}

func validStatus(status string) bool {
	switch status {
	case "active", "disabled", "dead":
		return true
	default:
		return false
	}
}

func statusOrActive(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "active"
	}
	return status
}

func cleanPtr(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrBackend, err)
}

func fromCreate(r admindb.CreateProxyRow) Proxy {
	return Proxy{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Protocol: r.Protocol, Host: r.Host, Port: r.Port,
		AuthUsername: r.AuthUsername, GroupID: r.GroupID, Status: r.Status, LastCheckAt: tsPtr(r.LastCheckAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}

func fromUpdate(r admindb.UpdateProxyRow) Proxy {
	return Proxy{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Protocol: r.Protocol, Host: r.Host, Port: r.Port,
		AuthUsername: r.AuthUsername, GroupID: r.GroupID, Status: r.Status, LastCheckAt: tsPtr(r.LastCheckAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}

func fromGet(r admindb.GetProxyRow) Proxy {
	return Proxy{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Protocol: r.Protocol, Host: r.Host, Port: r.Port,
		AuthUsername: r.AuthUsername, GroupID: r.GroupID, Status: r.Status, LastCheckAt: tsPtr(r.LastCheckAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}

func fromList(r admindb.ListProxiesByTenantRow) Proxy {
	return Proxy{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Protocol: r.Protocol, Host: r.Host, Port: r.Port,
		AuthUsername: r.AuthUsername, GroupID: r.GroupID, Status: r.Status, LastCheckAt: tsPtr(r.LastCheckAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}

func ts(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
