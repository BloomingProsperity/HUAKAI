package accountintake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountcreate"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

type Service struct {
	pool        *pgxpool.Pool
	credentials *credentialstore.Store
	health      ChannelHealthInitializer
	agentTasks  AgentTaskRegistrar
	proxies     ProxyResolver
}

func (s *Service) WithProxyResolver(resolver ProxyResolver) *Service {
	if s != nil {
		s.proxies = resolver
	}
	return s
}

type preparedPlan struct {
	result         PlanResult
	input          PlanInput
	candidates     []credentialacq.CredentialCandidate
	providerFamily string
}

func NewService(pool *pgxpool.Pool, credentials *credentialstore.Store, health ChannelHealthInitializer) *Service {
	return &Service{pool: pool, credentials: credentials, health: health}
}

// WithAgentTaskRegistrar 接入 Agent Identity 执行期任务登记；预检阶段保持纯本地。
func (s *Service) WithAgentTaskRegistrar(registrar AgentTaskRegistrar) *Service {
	if s != nil {
		s.agentTasks = registrar
	}
	return s
}

func (s *Service) Plan(ctx context.Context, in PlanInput) (PlanResult, error) {
	prepared, err := s.prepare(ctx, in)
	if err != nil {
		return PlanResult{}, err
	}
	defer zeroizeCandidates(prepared.candidates)
	return prepared.result, nil
}

func (s *Service) prepare(ctx context.Context, in PlanInput) (preparedPlan, error) {
	if s == nil || s.pool == nil || s.credentials == nil {
		return preparedPlan{}, ErrNotConfigured
	}
	in = normalizeInput(in)
	if isCodexIntake(in) {
		in = applyCodexAccountDefaults(in)
		var err error
		in.Account, err = s.resolveCodexLane(ctx, in.TenantID, in.Account)
		if err != nil {
			return preparedPlan{}, err
		}
	}
	if err := validateInput(in); err != nil {
		return preparedPlan{}, err
	}
	q := admindb.New(s.pool)
	family, err := q.GetProviderProtocolForAccountCreate(ctx, admindb.GetProviderProtocolForAccountCreateParams{
		TenantID: in.TenantID, ProviderID: in.Account.ProviderID,
	})
	if err != nil {
		return preparedPlan{}, err
	}
	inventory, err := s.credentials.ListIdentityInventory(ctx, in.TenantID, "")
	if err != nil {
		return preparedPlan{}, err
	}
	built, err := intake.Build(intake.BuildInput{
		TenantID: in.TenantID, SourceKind: in.SourceKind,
		DefaultVendor: in.DefaultVendor, DefaultAuthMode: in.DefaultAuthMode,
		Content: in.Content, Existing: intake.ExistingFromIdentityMetadata(inventory), Now: in.Now,
	})
	if err != nil {
		return preparedPlan{}, err
	}
	peers, err := q.ListProviderAccountRiskPeers(ctx, admindb.ListProviderAccountRiskPeersParams{
		TenantID: in.TenantID, ChannelID: in.Account.ChannelID,
	})
	if err != nil {
		zeroizeCandidates(built.Candidates)
		return preparedPlan{}, err
	}
	enrichPlan(&built.Plan, built.Candidates, family, in.Account, riskPeers(peers))
	if err := enrichUpdateCompatibility(ctx, q, in.TenantID, &built.Plan, built.Candidates); err != nil {
		zeroizeCandidates(built.Candidates)
		return preparedPlan{}, err
	}
	if isCodexIntake(in) {
		if err := s.rejectUnrunnableCodexUpdates(ctx, q, in.TenantID, &built.Plan); err != nil {
			zeroizeCandidates(built.Candidates)
			return preparedPlan{}, err
		}
	}
	hash, err := planHash(in, built.Plan)
	if err != nil {
		zeroizeCandidates(built.Candidates)
		return preparedPlan{}, err
	}
	return preparedPlan{
		result: PlanResult{PlanHash: hash, Plan: built.Plan},
		input:  in, candidates: built.Candidates, providerFamily: family,
	}, nil
}

func enrichUpdateCompatibility(ctx context.Context, lookup accountcreate.CredentialCompatLookup, tenantID int64, plan *intake.Plan, candidates []credentialacq.CredentialCandidate) error {
	if plan == nil {
		return nil
	}
	for index := range plan.Items {
		item := &plan.Items[index]
		if item.Action != intake.ActionUpdate || index >= len(candidates) {
			continue
		}
		candidate := candidates[index]
		err := accountcreate.ValidateCredentialCompatibility(
			ctx, lookup, tenantID, item.ExistingAccountID, candidate.Vendor, candidate.AuthMode,
		)
		if err == nil {
			continue
		}
		if !errors.Is(err, accountcreate.ErrProtocolIncompatible) {
			return err
		}
		item.Action = intake.ActionFail
		item.Code = "provider_protocol_incompatible"
		item.Message = "已有账号的类型或 provider 协议与待导入凭据不兼容"
		item.FieldChanges = nil
		item.RequiredConfirmations = nil
	}
	recountPlan(plan)
	return nil
}

func normalizeInput(in PlanInput) PlanInput {
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}
	in.DefaultVendor = credentialstore.Normalize(in.DefaultVendor)
	in.DefaultAuthMode = credentialstore.Normalize(in.DefaultAuthMode)
	in.Account.NamePrefix = strings.TrimSpace(in.Account.NamePrefix)
	in.Account.ExactName = strings.TrimSpace(in.Account.ExactName)
	in.Account.AccountType = strings.TrimSpace(in.Account.AccountType)
	in.Account.ProbeModel = cleanOptionalString(in.Account.ProbeModel)
	in.Account.Tags = cleanList(in.Account.Tags)
	in.Account.ModelAllowList = cleanList(in.Account.ModelAllowList)
	in.Account.CapabilityFlags = cleanList(in.Account.CapabilityFlags)
	if len(in.Account.Extra) > 0 {
		in.Account.Extra = append(json.RawMessage(nil), in.Account.Extra...)
	}
	if len(in.Account.TempUnschedulableRules) > 0 {
		in.Account.TempUnschedulableRules = append(json.RawMessage(nil), in.Account.TempUnschedulableRules...)
	}
	if in.Account.Proxy != nil {
		copy := *in.Account.Proxy
		in.Account.Proxy = &copy
	}
	return in
}

func validateInput(in PlanInput) error {
	if in.TenantID <= 0 {
		return fmt.Errorf("%w: tenant_id must be positive", ErrInvalidInput)
	}
	if in.Account.ProviderID <= 0 || in.Account.ChannelID <= 0 || (in.Account.NamePrefix == "" && in.Account.ExactName == "") {
		return fmt.Errorf("%w: provider_id, channel_id, and account name are required", ErrInvalidInput)
	}
	if len(in.Account.NamePrefix) > 200 || len(in.Account.ExactName) > 200 {
		return fmt.Errorf("%w: account name exceeds 200 bytes", ErrInvalidInput)
	}
	switch in.Account.AccountType {
	case "oauth", "api_key", "service_account", "upstream_static", "session", "aws_sigv4":
	default:
		return fmt.Errorf("%w: account_type is invalid", ErrInvalidInput)
	}
	if in.Account.CapConcurrency != nil && *in.Account.CapConcurrency <= 0 {
		return fmt.Errorf("%w: cap_concurrency must be positive", ErrInvalidInput)
	}
	if in.Account.CapQueueSticky != nil && *in.Account.CapQueueSticky < 0 {
		return fmt.Errorf("%w: cap_queue_sticky must not be negative", ErrInvalidInput)
	}
	if in.Account.CapQueueFallback != nil && *in.Account.CapQueueFallback < 0 {
		return fmt.Errorf("%w: cap_queue_fallback must not be negative", ErrInvalidInput)
	}
	if in.Account.StaticWeight != nil && *in.Account.StaticWeight <= 0 {
		return fmt.Errorf("%w: static_weight must be positive", ErrInvalidInput)
	}
	if in.Account.UpstreamCostRatio != nil && *in.Account.UpstreamCostRatio <= 0 {
		return fmt.Errorf("%w: upstream_cost_ratio must be positive", ErrInvalidInput)
	}
	for name, value := range map[string]*int64{
		"rpm_limit":               in.Account.RPMLimit,
		"tpm_limit":               in.Account.TPMLimit,
		"window_cost_limit_cents": in.Account.WindowCostLimitCents,
	} {
		if value != nil && *value < 0 {
			return fmt.Errorf("%w: %s must not be negative", ErrInvalidInput, name)
		}
	}
	if in.Account.MaxSessions != nil && *in.Account.MaxSessions < 0 {
		return fmt.Errorf("%w: max_sessions must not be negative", ErrInvalidInput)
	}
	if in.Account.RefreshLeadSeconds != nil && *in.Account.RefreshLeadSeconds <= 0 {
		return fmt.Errorf("%w: refresh_lead_seconds must be positive", ErrInvalidInput)
	}
	if len(in.Account.Extra) > 0 {
		if len(in.Account.Extra) > 64<<10 {
			return fmt.Errorf("%w: extra exceeds 64 KiB", ErrInvalidInput)
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(in.Account.Extra, &object) != nil || object == nil {
			return fmt.Errorf("%w: extra must be a JSON object", ErrInvalidInput)
		}
	}
	if len(in.Account.TempUnschedulableRules) > 0 {
		if len(in.Account.TempUnschedulableRules) > 64<<10 {
			return fmt.Errorf("%w: temp_unschedulable_rules exceeds 64 KiB", ErrInvalidInput)
		}
		var rules []json.RawMessage
		if json.Unmarshal(in.Account.TempUnschedulableRules, &rules) != nil {
			return fmt.Errorf("%w: temp_unschedulable_rules must be a JSON array", ErrInvalidInput)
		}
	}
	if len(in.Account.CustomErrorCodes) > 100 {
		return fmt.Errorf("%w: custom_error_codes exceeds 100 items", ErrInvalidInput)
	}
	seenErrorCodes := make(map[int32]struct{}, len(in.Account.CustomErrorCodes))
	for _, code := range in.Account.CustomErrorCodes {
		if code < 100 || code > 599 {
			return fmt.Errorf("%w: custom_error_codes must contain HTTP status codes", ErrInvalidInput)
		}
		if _, exists := seenErrorCodes[code]; exists {
			return fmt.Errorf("%w: custom_error_codes contains duplicates", ErrInvalidInput)
		}
		seenErrorCodes[code] = struct{}{}
	}
	if strings.TrimSpace(in.Content) == "" {
		return fmt.Errorf("%w: content is required", ErrInvalidInput)
	}
	if len(in.Content) > accountIntakeContentLimit {
		return fmt.Errorf("%w: content exceeds 2 MiB", ErrInvalidInput)
	}
	for name, values := range map[string][]string{
		"tags":             in.Account.Tags,
		"model_allow_list": in.Account.ModelAllowList,
		"capability_flags": in.Account.CapabilityFlags,
	} {
		if len(values) > 200 {
			return fmt.Errorf("%w: %s exceeds 200 items", ErrInvalidInput, name)
		}
		for _, value := range values {
			if len(value) > 200 {
				return fmt.Errorf("%w: %s item exceeds 200 bytes", ErrInvalidInput, name)
			}
		}
	}
	return nil
}

func enrichPlan(plan *intake.Plan, candidates []credentialacq.CredentialCandidate, family string, defaults AccountDefaults, peers []mixedchannelrisk.Account) {
	if plan == nil {
		return
	}
	for index := range plan.Items {
		item := &plan.Items[index]
		if item.Action != intake.ActionCreate || index >= len(candidates) {
			continue
		}
		candidate := candidates[index]
		if err := accountcreate.ValidateProtocolCompatibility(family, defaults.AccountType, candidate.Vendor, candidate.AuthMode); err != nil {
			item.Action = intake.ActionFail
			item.Code = "provider_protocol_incompatible"
			item.Message = "账号类型或凭据模式与 provider 协议不兼容"
			item.FieldChanges = nil
			item.RequiredConfirmations = nil
			continue
		}
		account := mixedchannelrisk.Account{
			ProviderID: defaults.ProviderID, ChannelID: defaults.ChannelID,
			AccountType: defaults.AccountType, Vendor: candidate.Vendor, AuthMode: candidate.AuthMode,
		}
		report := mixedchannelrisk.Evaluate(account, peers)
		if report.HighRisk {
			item.MixedChannelRisk = &report
			item.Warnings = appendUnique(item.Warnings, "mixed_channel_risk")
			item.RequiredConfirmations = appendUnique(item.RequiredConfirmations, "confirm_mixed_channel_risk")
		}
		peers = append(peers, account)
	}
	recountPlan(plan)
}

func riskPeers(rows []admindb.ProviderAccountRiskPeerRow) []mixedchannelrisk.Account {
	out := make([]mixedchannelrisk.Account, 0, len(rows))
	for _, row := range rows {
		out = append(out, mixedchannelrisk.Account{
			ID: row.ID, ProviderID: row.ProviderID, ChannelID: row.ChannelID,
			AccountType: row.AccountType, Vendor: row.CredentialVendor, AuthMode: row.CredentialAuthMode,
		})
	}
	return out
}

func recountPlan(plan *intake.Plan) {
	plan.Summary = intake.Summary{}
	for _, item := range plan.Items {
		switch item.Action {
		case intake.ActionCreate:
			plan.Summary.Create++
		case intake.ActionUpdate:
			plan.Summary.Update++
		case intake.ActionSkip:
			plan.Summary.Skip++
		case intake.ActionConflict:
			plan.Summary.Conflict++
		case intake.ActionFail:
			plan.Summary.Fail++
		}
	}
}

func planHash(in PlanInput, plan intake.Plan) (string, error) {
	contentSum := sha256.Sum256([]byte(in.Content))
	proxyHash, err := proxyPlanHash(in.Account.Proxy)
	if err != nil {
		return "", err
	}
	payload := struct {
		ContractVersion string            `json:"contract_version"`
		TenantID        int64             `json:"tenant_id"`
		SourceKind      intake.SourceKind `json:"source_kind"`
		DefaultVendor   string            `json:"default_vendor"`
		DefaultAuthMode string            `json:"default_auth_mode"`
		ContentSHA256   string            `json:"content_sha256"`
		ProxySHA256     string            `json:"proxy_sha256,omitempty"`
		Account         AccountDefaults   `json:"account"`
		Plan            intake.Plan       `json:"plan"`
	}{
		ContractVersion: intake.ContractVersion,
		TenantID:        in.TenantID, SourceKind: in.SourceKind,
		DefaultVendor: in.DefaultVendor, DefaultAuthMode: in.DefaultAuthMode,
		ContentSHA256: hex.EncodeToString(contentSum[:]), ProxySHA256: proxyHash,
		Account: in.Account, Plan: plan,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func proxyPlanHash(proxy *ProxyMaterial) (string, error) {
	if proxy == nil {
		return "", nil
	}
	secretSum := sha256.Sum256([]byte(proxy.AuthSecret))
	value := struct {
		Name             string `json:"name"`
		Protocol         string `json:"protocol"`
		Host             string `json:"host"`
		Port             int32  `json:"port"`
		AuthUsername     string `json:"auth_username"`
		AuthSecretSHA256 string `json:"auth_secret_sha256"`
		SourceRef        string `json:"source_ref"`
	}{
		Name: strings.TrimSpace(proxy.Name), Protocol: strings.ToLower(strings.TrimSpace(proxy.Protocol)),
		Host: strings.ToLower(strings.TrimSpace(proxy.Host)), Port: proxy.Port,
		AuthUsername: strings.TrimSpace(proxy.AuthUsername), AuthSecretSHA256: hex.EncodeToString(secretSum[:]),
		SourceRef: strings.ToLower(strings.TrimSpace(proxy.SourceRef)),
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	defer privacy.Zeroize(raw)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func zeroizeCandidates(candidates []credentialacq.CredentialCandidate) {
	for index := range candidates {
		privacy.Zeroize(candidates[index].Payload)
		candidates[index].Payload = nil
	}
}

func cleanOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	cleaned := strings.TrimSpace(*value)
	return &cleaned
}

func cleanList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if cleaned := strings.TrimSpace(value); cleaned != "" {
			out = appendUnique(out, cleaned)
		}
	}
	return out
}

func appendUnique(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}
