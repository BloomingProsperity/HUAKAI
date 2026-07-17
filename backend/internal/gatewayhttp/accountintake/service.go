package accountintake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func (s *Service) Plan(ctx context.Context, in PlanInput) (PlanResult, error) {
	prepared, err := s.prepare(ctx, in)
	if err != nil {
		return PlanResult{}, err
	}
	defer zeroizeCandidates(prepared.candidates)
	return prepared.result, nil
}

func (s *Service) PlanCandidate(ctx context.Context, in CandidatePlanInput) (PlanResult, error) {
	prepared, err := s.prepareCandidate(ctx, in)
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

func (s *Service) prepareCandidate(ctx context.Context, in CandidatePlanInput) (preparedPlan, error) {
	if s == nil || s.pool == nil || s.credentials == nil {
		return preparedPlan{}, ErrNotConfigured
	}
	base := normalizeInput(PlanInput{TenantID: in.TenantID, SourceKind: in.SourceKind, Account: in.Account, Now: in.Now})
	if err := validateAccountInput(base); err != nil {
		return preparedPlan{}, err
	}
	switch in.SourceKind {
	case intake.SourceClaudeCookie, intake.SourceCRSSync, intake.SourceAccountRecovery:
	default:
		return preparedPlan{}, fmt.Errorf("%w: unsupported server-side source_kind", ErrInvalidInput)
	}
	if !validPlanHash(strings.TrimSpace(in.SourceCommitment)) {
		return preparedPlan{}, fmt.Errorf("%w: source commitment must be 64 lowercase hexadecimal characters", ErrInvalidInput)
	}
	candidate := in.Candidate
	if candidate.TenantID != 0 && candidate.TenantID != in.TenantID {
		return preparedPlan{}, fmt.Errorf("%w: candidate tenant mismatch", ErrInvalidInput)
	}
	candidate.TenantID = in.TenantID
	candidate.Vendor = credentialstore.Normalize(candidate.Vendor)
	candidate.AuthMode = credentialstore.Normalize(candidate.AuthMode)
	candidate.Payload = append([]byte(nil), candidate.Payload...)
	if candidate.Vendor == "" || candidate.AuthMode == "" || len(candidate.Payload) == 0 {
		zeroizeCandidates([]credentialacq.CredentialCandidate{candidate})
		return preparedPlan{}, fmt.Errorf("%w: candidate credential is incomplete", ErrInvalidInput)
	}
	q := admindb.New(s.pool)
	family, err := q.GetProviderProtocolForAccountCreate(ctx, admindb.GetProviderProtocolForAccountCreateParams{
		TenantID: base.TenantID, ProviderID: base.Account.ProviderID,
	})
	if err != nil {
		zeroizeCandidates([]credentialacq.CredentialCandidate{candidate})
		return preparedPlan{}, err
	}
	inventory, err := s.credentials.ListIdentityInventory(ctx, base.TenantID, "")
	if err != nil {
		zeroizeCandidates([]credentialacq.CredentialCandidate{candidate})
		return preparedPlan{}, err
	}
	built := intake.BuildCandidates(intake.BuildInput{
		TenantID: base.TenantID, SourceKind: base.SourceKind,
		Existing: intake.ExistingFromIdentityMetadata(inventory), Now: base.Now,
	}, []credentialacq.CredentialCandidate{candidate})
	peers, err := q.ListProviderAccountRiskPeers(ctx, admindb.ListProviderAccountRiskPeersParams{
		TenantID: base.TenantID, ChannelID: base.Account.ChannelID,
	})
	if err != nil {
		zeroizeCandidates(built.Candidates)
		return preparedPlan{}, err
	}
	enrichPlan(&built.Plan, built.Candidates, family, base.Account, riskPeers(peers))
	hash, err := candidatePlanHash(base, strings.TrimSpace(in.SourceCommitment), built.Plan)
	if err != nil {
		zeroizeCandidates(built.Candidates)
		return preparedPlan{}, err
	}
	return preparedPlan{
		result: PlanResult{PlanHash: hash, Plan: built.Plan}, input: base,
		candidates: built.Candidates, providerFamily: family,
	}, nil
}

func normalizeInput(in PlanInput) PlanInput {
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}
	in.DefaultVendor = credentialstore.Normalize(in.DefaultVendor)
	in.DefaultAuthMode = credentialstore.Normalize(in.DefaultAuthMode)
	in.Account.Name = strings.TrimSpace(in.Account.Name)
	in.Account.NamePrefix = strings.TrimSpace(in.Account.NamePrefix)
	in.Account.AccountType = strings.TrimSpace(in.Account.AccountType)
	in.Account.ProbeModel = cleanOptionalString(in.Account.ProbeModel)
	in.Account.Tags = cleanList(in.Account.Tags)
	in.Account.ModelAllowList = cleanList(in.Account.ModelAllowList)
	in.Account.CapabilityFlags = cleanList(in.Account.CapabilityFlags)
	if len(in.Account.Extra) > 0 {
		in.Account.Extra = append(json.RawMessage(nil), in.Account.Extra...)
	}
	return in
}

func validateInput(in PlanInput) error {
	if err := validateAccountInput(in); err != nil {
		return err
	}
	if strings.TrimSpace(in.Content) == "" {
		return fmt.Errorf("%w: content is required", ErrInvalidInput)
	}
	if len(in.Content) > accountIntakeContentLimit {
		return fmt.Errorf("%w: content exceeds 2 MiB", ErrInvalidInput)
	}
	return nil
}

func validateAccountInput(in PlanInput) error {
	if in.TenantID <= 0 {
		return fmt.Errorf("%w: tenant_id must be positive", ErrInvalidInput)
	}
	if in.Account.ProviderID <= 0 || in.Account.ChannelID <= 0 || (in.Account.Name == "" && in.Account.NamePrefix == "") {
		return fmt.Errorf("%w: provider_id, channel_id, and account name are required", ErrInvalidInput)
	}
	if in.Account.Name != "" && in.Account.NamePrefix != "" {
		return fmt.Errorf("%w: name and name_prefix are mutually exclusive", ErrInvalidInput)
	}
	if len(in.Account.Name) > 200 {
		return fmt.Errorf("%w: name exceeds 200 bytes", ErrInvalidInput)
	}
	if len(in.Account.NamePrefix) > 200 {
		return fmt.Errorf("%w: name_prefix exceeds 200 bytes", ErrInvalidInput)
	}
	switch in.Account.AccountType {
	case "oauth", "api_key", "service_account", "upstream_static", "session", "aws_sigv4":
	default:
		return fmt.Errorf("%w: account_type is invalid", ErrInvalidInput)
	}
	if in.Account.CapConcurrency != nil && *in.Account.CapConcurrency <= 0 {
		return fmt.Errorf("%w: cap_concurrency must be positive", ErrInvalidInput)
	}
	if in.Account.StaticWeight != nil && *in.Account.StaticWeight <= 0 {
		return fmt.Errorf("%w: static_weight must be positive", ErrInvalidInput)
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
	payload := struct {
		ContractVersion string            `json:"contract_version"`
		TenantID        int64             `json:"tenant_id"`
		SourceKind      intake.SourceKind `json:"source_kind"`
		DefaultVendor   string            `json:"default_vendor"`
		DefaultAuthMode string            `json:"default_auth_mode"`
		ContentSHA256   string            `json:"content_sha256"`
		Account         AccountDefaults   `json:"account"`
		Plan            intake.Plan       `json:"plan"`
	}{
		ContractVersion: intake.ContractVersion,
		TenantID:        in.TenantID, SourceKind: in.SourceKind,
		DefaultVendor: in.DefaultVendor, DefaultAuthMode: in.DefaultAuthMode,
		ContentSHA256: hex.EncodeToString(contentSum[:]), Account: in.Account, Plan: plan,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func candidatePlanHash(in PlanInput, commitment string, plan intake.Plan) (string, error) {
	payload := struct {
		ContractVersion  string            `json:"contract_version"`
		TenantID         int64             `json:"tenant_id"`
		SourceKind       intake.SourceKind `json:"source_kind"`
		SourceCommitment string            `json:"source_commitment"`
		Account          AccountDefaults   `json:"account"`
		Plan             intake.Plan       `json:"plan"`
	}{
		ContractVersion: intake.ContractVersion, TenantID: in.TenantID,
		SourceKind: in.SourceKind, SourceCommitment: commitment, Account: in.Account, Plan: plan,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
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
