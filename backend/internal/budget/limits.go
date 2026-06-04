package budget

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

func (p StaticLimitsProvider) Scopes(_ context.Context, req ReserveRequest) ([]ScopeLimit, error) {
	out := make([]ScopeLimit, 0, 4)
	model := strings.TrimSpace(req.RequestedModel)
	if req.APIKeyID > 0 {
		spec, hasSpec := p.Keys[req.APIKeyID]
		if modelLimits, ok := spec.modelLimits(model); ok {
			out = append(out, ScopeLimit{
				Scope:  Scope{TenantID: req.TenantID, Kind: ScopeAPIKey, ID: intString(req.APIKeyID), Model: model},
				Limits: modelLimits,
			})
		}
		if limits := limitsForSpec(spec, hasSpec, p.Default); !limits.isUnlimited() {
			out = append(out, ScopeLimit{
				Scope:  Scope{TenantID: req.TenantID, Kind: ScopeAPIKey, ID: intString(req.APIKeyID)},
				Limits: limits,
			})
		}
	}
	if req.UserID > 0 {
		spec, hasSpec := p.Users[req.UserID]
		if modelLimits, ok := spec.modelLimits(model); ok {
			out = append(out, ScopeLimit{
				Scope:  Scope{TenantID: req.TenantID, Kind: ScopeUser, ID: intString(req.UserID), Model: model},
				Limits: modelLimits,
			})
		}
		if limits := limitsForSpec(spec, hasSpec, p.Default); !limits.isUnlimited() {
			out = append(out, ScopeLimit{
				Scope:  Scope{TenantID: req.TenantID, Kind: ScopeUser, ID: intString(req.UserID)},
				Limits: limits,
			})
		}
	}
	if req.PoolGroupID > 0 {
		spec, hasSpec := p.PoolGroups[req.PoolGroupID]
		if limits := limitsForSpec(spec, hasSpec, p.Default); !limits.isUnlimited() {
			out = append(out, ScopeLimit{
				Scope:  Scope{TenantID: req.TenantID, Kind: ScopePoolGroup, ID: intString(req.PoolGroupID)},
				Limits: limits,
			})
		}
	}
	if len(out) == 0 && !p.Default.normalized().isUnlimited() && req.UserID <= 0 && req.APIKeyID <= 0 && req.PoolGroupID <= 0 {
		out = append(out, ScopeLimit{
			Scope:  Scope{TenantID: req.TenantID, Kind: ScopeUser, ID: "0"},
			Limits: p.Default.normalized(),
		})
	}
	return out, nil
}

func limitsForSpec(spec LimitSpec, exists bool, def LimitPair) LimitPair {
	if !exists {
		return def.normalized()
	}
	return spec.LimitPair.normalized()
}

func (s LimitSpec) modelLimits(model string) (LimitPair, bool) {
	if strings.TrimSpace(model) == "" || len(s.Models) == 0 {
		return LimitPair{}, false
	}
	pair, ok := s.Models[model]
	if !ok {
		return LimitPair{}, false
	}
	pair = pair.normalized()
	return pair, !pair.isUnlimited()
}

func (p LimitPair) isUnlimited() bool {
	p = p.normalized()
	return p.RPM == 0 && p.TPM == 0
}

type SettingsJSONProvider interface {
	BudgetLimitsJSON(context.Context) (string, error)
}

type MergedLimitsProvider struct {
	Static   StaticLimitsProvider
	Settings SettingsJSONProvider
}

func (p MergedLimitsProvider) Scopes(ctx context.Context, req ReserveRequest) ([]ScopeLimit, error) {
	if p.Settings == nil {
		return p.Static.Scopes(ctx, req)
	}
	raw, err := p.Settings.BudgetLimitsJSON(ctx)
	if err != nil || strings.TrimSpace(raw) == "" {
		return p.Static.Scopes(ctx, req)
	}
	doc, err := ParseLimitsJSON(raw)
	if err != nil {
		return p.Static.Scopes(ctx, req)
	}
	if doc.Default.isUnlimited() {
		doc.Default = p.Static.Default
	}
	return doc.Scopes(ctx, req)
}

func ParseLimitsJSON(raw string) (StaticLimitsProvider, error) {
	var doc struct {
		Default    LimitPair            `json:"default"`
		Users      map[string]LimitSpec `json:"users"`
		Keys       map[string]LimitSpec `json:"keys"`
		PoolGroups map[string]LimitSpec `json:"pool_groups"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return StaticLimitsProvider{}, err
	}
	return StaticLimitsProvider{
		Default:    doc.Default.normalized(),
		Users:      parseLimitSpecMap(doc.Users),
		Keys:       parseLimitSpecMap(doc.Keys),
		PoolGroups: parseLimitSpecMap(doc.PoolGroups),
	}, nil
}

func parseLimitSpecMap(in map[string]LimitSpec) map[int64]LimitSpec {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int64]LimitSpec, len(in))
	for raw, spec := range in {
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		spec.LimitPair = spec.LimitPair.normalized()
		for model, pair := range spec.Models {
			if spec.Models == nil {
				break
			}
			spec.Models[model] = pair.normalized()
		}
		out[id] = spec
	}
	return out
}
