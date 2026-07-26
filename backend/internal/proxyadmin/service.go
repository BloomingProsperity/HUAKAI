package proxyadmin

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/proxysecret"
	"github.com/BloomingProsperity/HUAKAI/internal/ssrfpolicy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Querier interface {
	CreateProxy(context.Context, admindb.CreateProxyParams) (admindb.CreateProxyRow, error)
	UpdateProxy(context.Context, admindb.UpdateProxyParams) (admindb.UpdateProxyRow, error)
	GetProxy(context.Context, admindb.GetProxyParams) (admindb.GetProxyRow, error)
	GetProxyDeleteImpact(context.Context, admindb.GetProxyDeleteImpactParams) (admindb.GetProxyDeleteImpactRow, error)
	ListProxiesByTenant(context.Context, int64) ([]admindb.ListProxiesByTenantRow, error)
	SetProxyStatus(context.Context, admindb.SetProxyStatusParams) (int64, error)
	DeleteProxyIfUnused(context.Context, admindb.DeleteProxyIfUnusedParams) (admindb.DeleteProxyIfUnusedRow, error)
}

type Service struct {
	q         Querier
	keys      credentialstore.KeyProvider
	mutations mutationStore
}

func New(q Querier, keys credentialstore.KeyProvider) *Service {
	return &Service{q: q, keys: keys}
}

// NewPostgres 构造管理写入口使用的服务。所有写操作由 mutationStore 将业务行与
// 操作日志放进同一事务；普通 New 仍供只读、探测和受控内部流程复用。
func NewPostgres(pool *pgxpool.Pool, keys credentialstore.KeyProvider) *Service {
	if pool == nil {
		return &Service{keys: keys}
	}
	return &Service{
		q:         admindb.New(pool),
		keys:      keys,
		mutations: newPostgresMutationStore(pool),
	}
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Proxy, error) {
	params, err := s.createParams(ctx, in)
	if err != nil {
		return Proxy{}, err
	}
	if s == nil || s.q == nil {
		return Proxy{}, ErrStoreNotConfigured
	}
	row, err := s.q.CreateProxy(ctx, params)
	if err != nil {
		return Proxy{}, mapErr(err)
	}
	return fromCreate(row), nil
}

// CreateWithAudit 是管理 HTTP 面唯一允许调用的新增入口。
func (s *Service) CreateWithAudit(ctx context.Context, in CreateInput, audit MutationAudit) (Proxy, error) {
	params, err := s.createParams(ctx, in)
	if err != nil {
		return Proxy{}, err
	}
	if err := validateMutationAudit(audit); err != nil {
		return Proxy{}, err
	}
	if s == nil || s.mutations == nil {
		return Proxy{}, ErrStoreNotConfigured
	}
	row, err := s.mutations.Create(ctx, params, audit)
	if err != nil {
		return Proxy{}, mapErr(err)
	}
	return fromCreate(row), nil
}

func (s *Service) createParams(ctx context.Context, in CreateInput) (admindb.CreateProxyParams, error) {
	if err := validateCreate(in); err != nil {
		return admindb.CreateProxyParams{}, err
	}
	secret, err := s.encryptAuthSecret(ctx, in.TenantID, in.AuthSecret)
	if err != nil {
		return admindb.CreateProxyParams{}, err
	}
	return admindb.CreateProxyParams{
		TenantID:     in.TenantID,
		Name:         strings.TrimSpace(in.Name),
		Protocol:     strings.TrimSpace(in.Protocol),
		Host:         strings.TrimSpace(in.Host),
		Port:         in.Port,
		AuthUsername: cleanPtr(in.AuthUsername),
		AuthSecret:   secret,
		GroupID:      normalizeGroupID(in.GroupID),
		Status:       statusOrActive(in.Status),
	}, nil
}

func (s *Service) Update(ctx context.Context, in UpdateInput) (Proxy, error) {
	if err := validateUpdate(in); err != nil {
		return Proxy{}, err
	}
	return s.Patch(ctx, PatchInput{
		TenantID:     in.TenantID,
		ID:           in.ID,
		Name:         PatchField[string]{Set: true, Value: in.Name},
		Protocol:     PatchField[string]{Set: true, Value: in.Protocol},
		Host:         PatchField[string]{Set: true, Value: in.Host},
		Port:         PatchField[int32]{Set: true, Value: in.Port},
		AuthUsername: PatchField[*string]{Set: true, Value: in.AuthUsername},
		AuthSecret:   PatchField[*string]{Set: true, Value: in.AuthSecret},
		GroupID:      PatchField[*string]{Set: true, Value: in.GroupID},
	})
}

// Patch 原子更新请求中出现的字段。认证秘密未出现时由 SQL 保留原密文，只有显式
// null 或空串才清空，避免管理页面修改名称时意外擦除代理凭据。
func (s *Service) Patch(ctx context.Context, in PatchInput) (Proxy, error) {
	params, err := s.patchParams(ctx, in)
	if err != nil {
		return Proxy{}, err
	}
	if s == nil || s.q == nil {
		return Proxy{}, ErrStoreNotConfigured
	}
	row, err := s.q.UpdateProxy(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return Proxy{}, ErrNotFound
	}
	if err != nil {
		return Proxy{}, mapErr(err)
	}
	return fromUpdate(row), nil
}

// PatchWithAudit 是管理 HTTP 面唯一允许调用的字段级更新入口。
func (s *Service) PatchWithAudit(ctx context.Context, in PatchInput, audit MutationAudit) (Proxy, error) {
	params, err := s.patchParams(ctx, in)
	if err != nil {
		return Proxy{}, err
	}
	if err := validateMutationAudit(audit); err != nil {
		return Proxy{}, err
	}
	if s == nil || s.mutations == nil {
		return Proxy{}, ErrStoreNotConfigured
	}
	row, err := s.mutations.Update(ctx, params, patchFieldNames(in), audit)
	if errors.Is(err, pgx.ErrNoRows) {
		return Proxy{}, ErrNotFound
	}
	if err != nil {
		return Proxy{}, mapErr(err)
	}
	return fromUpdate(row), nil
}

func (s *Service) patchParams(ctx context.Context, in PatchInput) (admindb.UpdateProxyParams, error) {
	if err := validatePatch(in); err != nil {
		return admindb.UpdateProxyParams{}, err
	}
	var secret *string
	var err error
	if in.AuthSecret.Set {
		secret, err = s.encryptAuthSecret(ctx, in.TenantID, in.AuthSecret.Value)
	}
	if err != nil {
		return admindb.UpdateProxyParams{}, err
	}
	return admindb.UpdateProxyParams{
		TenantID:        in.TenantID,
		ID:              in.ID,
		NameSet:         in.Name.Set,
		Name:            strings.TrimSpace(in.Name.Value),
		ProtocolSet:     in.Protocol.Set,
		Protocol:        strings.TrimSpace(in.Protocol.Value),
		HostSet:         in.Host.Set,
		Host:            strings.TrimSpace(in.Host.Value),
		PortSet:         in.Port.Set,
		Port:            in.Port.Value,
		AuthUsernameSet: in.AuthUsername.Set,
		AuthUsername:    cleanPtr(in.AuthUsername.Value),
		AuthSecretSet:   in.AuthSecret.Set,
		AuthSecret:      secret,
		GroupIDSet:      in.GroupID.Set,
		GroupID:         normalizeGroupID(in.GroupID.Value),
	}, nil
}

// List 返回某租户全部未删除的代理,不含凭据。底层行上加密的 auth_secret
// 永远不会被映射进结果。
func (s *Service) List(ctx context.Context, tenantID int64) ([]Proxy, error) {
	if tenantID <= 0 {
		return nil, ErrInvalidInput
	}
	if s == nil || s.q == nil {
		return nil, ErrStoreNotConfigured
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
	if s == nil || s.q == nil {
		return Proxy{}, ErrStoreNotConfigured
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

func (s *Service) DeleteImpact(ctx context.Context, tenantID, id int64) (DeleteImpact, error) {
	if tenantID <= 0 || id <= 0 {
		return DeleteImpact{}, ErrInvalidInput
	}
	if s == nil || s.q == nil {
		return DeleteImpact{}, ErrStoreNotConfigured
	}
	row, err := s.q.GetProxyDeleteImpact(ctx, admindb.GetProxyDeleteImpactParams{
		TargetTenantID: tenantID,
		TargetProxyID:  id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return DeleteImpact{}, ErrNotFound
	}
	if err != nil {
		return DeleteImpact{}, mapErr(err)
	}
	return deleteImpactFromCounts(
		row.ID,
		row.DirectAccountCount,
		row.DefaultTenantCount,
		row.GroupAccountCount,
		row.GroupRemainingActiveCount,
	), nil
}

// Delete 在数据库持有目标代理行锁期间复核全部引用；删除会打断单账号绑定、
// 租户默认出口或最后一个可用代理组成员时返回 ErrInUse。
func (s *Service) Delete(ctx context.Context, tenantID, id int64) error {
	if tenantID <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	row, err := s.q.DeleteProxyIfUnused(ctx, admindb.DeleteProxyIfUnusedParams{
		TargetTenantID: tenantID,
		TargetProxyID:  id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return mapErr(err)
	}
	if !row.Deleted {
		return &InUseError{Impact: deleteImpactFromCounts(
			row.ID,
			row.DirectAccountCount,
			row.DefaultTenantCount,
			row.GroupAccountCount,
			row.GroupRemainingActiveCount,
		)}
	}
	return nil
}

// DeleteWithAudit 在数据库锁定引用关系后执行删除，并仅在实际删除时写操作日志。
func (s *Service) DeleteWithAudit(ctx context.Context, tenantID, id int64, audit MutationAudit) error {
	if tenantID <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	if err := validateMutationAudit(audit); err != nil {
		return err
	}
	if s == nil || s.mutations == nil {
		return ErrStoreNotConfigured
	}
	row, err := s.mutations.Delete(ctx, admindb.DeleteProxyIfUnusedParams{
		TargetTenantID: tenantID,
		TargetProxyID:  id,
	}, audit)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return mapErr(err)
	}
	if !row.Deleted {
		return &InUseError{Impact: deleteImpactFromCounts(
			row.ID,
			row.DirectAccountCount,
			row.DefaultTenantCount,
			row.GroupAccountCount,
			row.GroupRemainingActiveCount,
		)}
	}
	return nil
}

func deleteImpactFromCounts(proxyID, direct, tenantDefault, groupAccounts, groupRemaining int64) DeleteImpact {
	return DeleteImpact{
		ProxyID:                   proxyID,
		DirectAccountCount:        direct,
		DefaultTenantCount:        tenantDefault,
		GroupAccountCount:         groupAccounts,
		GroupRemainingActiveCount: groupRemaining,
	}
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
	affected, err := s.q.SetProxyStatus(ctx, admindb.SetProxyStatusParams{Status: status, TenantID: tenantID, ID: id})
	if err != nil {
		return mapErr(err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// SetStatusWithAudit 原子写入生命周期状态和操作日志。
func (s *Service) SetStatusWithAudit(ctx context.Context, tenantID, id int64, status string, audit MutationAudit) error {
	if tenantID <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	status = strings.TrimSpace(status)
	if !validStatus(status) {
		return ErrInvalidStatus
	}
	if err := validateMutationAudit(audit); err != nil {
		return err
	}
	if s == nil || s.mutations == nil {
		return ErrStoreNotConfigured
	}
	affected, err := s.mutations.SetStatus(ctx, admindb.SetProxyStatusParams{
		Status: status, TenantID: tenantID, ID: id,
	}, audit)
	if err != nil {
		return mapErr(err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func validateMutationAudit(audit MutationAudit) error {
	if strings.TrimSpace(audit.ActorID) == "" || strings.TrimSpace(audit.ActorRole) == "" {
		return ErrInvalidInput
	}
	return nil
}

func patchFieldNames(in PatchInput) []string {
	fields := make([]string, 0, 7)
	if in.Name.Set {
		fields = append(fields, "name")
	}
	if in.Protocol.Set {
		fields = append(fields, "protocol")
	}
	if in.Host.Set {
		fields = append(fields, "host")
	}
	if in.Port.Set {
		fields = append(fields, "port")
	}
	if in.AuthUsername.Set {
		fields = append(fields, "auth_username")
	}
	if in.AuthSecret.Set {
		fields = append(fields, "auth_secret")
	}
	if in.GroupID.Set {
		fields = append(fields, "group_id")
	}
	return fields
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

func validatePatch(in PatchInput) error {
	if in.TenantID <= 0 || in.ID <= 0 {
		return ErrInvalidInput
	}
	if !in.Name.Set && !in.Protocol.Set && !in.Host.Set && !in.Port.Set &&
		!in.AuthUsername.Set && !in.AuthSecret.Set && !in.GroupID.Set {
		return ErrInvalidInput
	}
	if in.Name.Set && strings.TrimSpace(in.Name.Value) == "" {
		return ErrInvalidInput
	}
	if in.Protocol.Set && strings.TrimSpace(in.Protocol.Value) == "" {
		return ErrInvalidInput
	}
	if in.Host.Set {
		if strings.TrimSpace(in.Host.Value) == "" {
			return ErrInvalidInput
		}
		if !proxyHostSafe(in.Host.Value) {
			return ErrUnsafeHost
		}
	}
	if in.Port.Set && (in.Port.Value <= 0 || in.Port.Value > 65535) {
		return ErrInvalidInput
	}
	if in.GroupID.Set {
		return validateGroupID(in.GroupID.Value)
	}
	return nil
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
	// SSRF 静态防护先拦字面量；域名在每次真实拨号时重新解析并绑定。
	// 私网代理必须由部署者按原始主机精确放行，租户管理员不能自行扩大服务器内网访问面。
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

// proxyHostSafe 判定一个【裸主机名/IP】能否作为租户代理目标。
// 公网地址直接允许；私网地址仅在部署者专用 allowlist 中时允许；loopback、
// link-local、metadata 和其它特殊用途地址永远拒绝。主机名在写入时只做静态
// 格式/高危名称校验，DNS 重绑定由实际拨号守卫负责。
func proxyHostSafe(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	if h == "" {
		return false
	}
	if addr, err := netip.ParseAddr(h); err == nil {
		policy, policyErr := ssrfpolicy.LoadProxyFromEnv()
		return policyErr == nil && policy.AllowsAddress(h, addr)
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
