package alerting

import (
	"context"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu          sync.Mutex
	nextRuleID  int64
	nextEventID int64
	nextMuteID  int64
	rules       map[int64]AlertRule
	events      map[int64]AlertEvent
	silences    map[int64]AlertSilence
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		nextRuleID:  1,
		nextEventID: 1,
		nextMuteID:  1,
		rules:       make(map[int64]AlertRule),
		events:      make(map[int64]AlertEvent),
		silences:    make(map[int64]AlertSilence),
	}
}

func (s *MemoryStore) CreateRule(_ context.Context, rule AlertRule) (AlertRule, error) {
	if s == nil {
		return AlertRule{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ruleNameExists(s.rules, rule.TenantID, 0, rule.Name) {
		return AlertRule{}, ErrRuleExists
	}
	rule.ID = s.nextRuleID
	s.nextRuleID++
	s.rules[rule.ID] = cloneRule(rule)
	return cloneRule(rule), nil
}

func (s *MemoryStore) UpdateRule(_ context.Context, rule AlertRule) (AlertRule, error) {
	if s == nil {
		return AlertRule{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.rules[rule.ID]
	if !ok || current.TenantID != rule.TenantID {
		return AlertRule{}, ErrNotFound
	}
	if ruleNameExists(s.rules, rule.TenantID, rule.ID, rule.Name) {
		return AlertRule{}, ErrRuleExists
	}
	rule.CreatedAt = current.CreatedAt
	rule.LastTriggeredAt = timePtr(rule.LastTriggeredAt)
	s.rules[rule.ID] = cloneRule(rule)
	return cloneRule(rule), nil
}

func (s *MemoryStore) DeleteRule(_ context.Context, tenantID, id int64) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rule, ok := s.rules[id]
	if !ok || rule.TenantID != tenantID {
		return ErrNotFound
	}
	delete(s.rules, id)
	return nil
}

func (s *MemoryStore) GetRule(_ context.Context, tenantID, id int64) (AlertRule, error) {
	if s == nil {
		return AlertRule{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rule, ok := s.rules[id]
	if !ok || rule.TenantID != tenantID {
		return AlertRule{}, ErrNotFound
	}
	return cloneRule(rule), nil
}

func (s *MemoryStore) ListRules(_ context.Context, in ListRulesInput) ([]AlertRule, error) {
	if s == nil {
		return nil, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AlertRule, 0, len(s.rules))
	for _, rule := range s.rules {
		if rule.TenantID == in.TenantID {
			out = append(out, cloneRule(rule))
		}
	}
	sortRules(out)
	return pageRules(out, in.Limit, in.Offset), nil
}

func (s *MemoryStore) ListEnabledRules(_ context.Context, tenantID int64) ([]AlertRule, error) {
	if s == nil {
		return nil, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AlertRule, 0, len(s.rules))
	for _, rule := range s.rules {
		if rule.TenantID == tenantID && rule.Enabled {
			out = append(out, cloneRule(rule))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *MemoryStore) ListTenantsWithEnabledRules(_ context.Context) ([]int64, error) {
	if s == nil {
		return nil, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[int64]struct{}{}
	for _, rule := range s.rules {
		if rule.Enabled {
			seen[rule.TenantID] = struct{}{}
		}
	}
	out := make([]int64, 0, len(seen))
	for tenantID := range seen {
		out = append(out, tenantID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func (s *MemoryStore) UpsertFiringEvent(_ context.Context, in UpsertFiringEventInput) (AlertEvent, bool, error) {
	if s == nil {
		return AlertEvent{}, false, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, event := range s.events {
		if event.TenantID == in.TenantID && event.RuleID == in.RuleID && event.State == EventStateFiring {
			event.ObservedValue = in.ObservedValue
			event.ThresholdValue = float64Ptr(in.ThresholdValue)
			event.MetricValue = float64Ptr(in.MetricValue)
			event.Dimensions = normalizeStringMap(in.Dimensions)
			s.events[id] = cloneEvent(event)
			return cloneEvent(event), false, nil
		}
	}
	event := AlertEvent{
		ID:             s.nextEventID,
		TenantID:       in.TenantID,
		RuleID:         in.RuleID,
		State:          EventStateFiring,
		ObservedValue:  in.ObservedValue,
		ThresholdValue: float64Ptr(in.ThresholdValue),
		MetricValue:    float64Ptr(in.MetricValue),
		Dimensions:     normalizeStringMap(in.Dimensions),
		FiredAt:        in.FiredAt.UTC(),
	}
	s.nextEventID++
	s.events[event.ID] = cloneEvent(event)
	return cloneEvent(event), true, nil
}

func (s *MemoryStore) ResolveFiringEvent(_ context.Context, tenantID, ruleID int64, now time.Time) (AlertEvent, bool, error) {
	if s == nil {
		return AlertEvent{}, false, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, event := range s.events {
		if event.TenantID == tenantID && event.RuleID == ruleID && event.State == EventStateFiring {
			resolvedAt := now.UTC()
			event.State = EventStateResolved
			event.ResolvedAt = &resolvedAt
			s.events[id] = cloneEvent(event)
			return cloneEvent(event), true, nil
		}
	}
	return AlertEvent{}, false, nil
}

func (s *MemoryStore) ManualResolveEvent(_ context.Context, tenantID, eventID int64, now time.Time) (AlertEvent, error) {
	if s == nil {
		return AlertEvent{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	event, ok := s.events[eventID]
	if !ok || event.TenantID != tenantID {
		return AlertEvent{}, ErrNotFound
	}
	resolvedAt := now.UTC()
	event.State = EventStateManualResolved
	event.ResolvedAt = &resolvedAt
	s.events[eventID] = cloneEvent(event)
	return cloneEvent(event), nil
}

func (s *MemoryStore) MarkEventEmailSent(_ context.Context, tenantID, eventID int64) (AlertEvent, error) {
	if s == nil {
		return AlertEvent{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	event, ok := s.events[eventID]
	if !ok || event.TenantID != tenantID {
		return AlertEvent{}, ErrNotFound
	}
	event.EmailSent = true
	s.events[eventID] = cloneEvent(event)
	return cloneEvent(event), nil
}

func (s *MemoryStore) MarkRuleTriggered(_ context.Context, tenantID, ruleID int64, now time.Time) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rule, ok := s.rules[ruleID]
	if !ok || rule.TenantID != tenantID {
		return ErrNotFound
	}
	triggeredAt := now.UTC()
	rule.LastTriggeredAt = &triggeredAt
	rule.UpdatedAt = triggeredAt
	s.rules[ruleID] = cloneRule(rule)
	return nil
}

func (s *MemoryStore) ListEvents(_ context.Context, in ListEventsInput) ([]AlertEvent, error) {
	if s == nil {
		return nil, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AlertEvent, 0, len(s.events))
	for _, event := range s.events {
		if event.TenantID != in.TenantID {
			continue
		}
		if in.RuleID != nil && event.RuleID != *in.RuleID {
			continue
		}
		if in.State != "" && event.State != in.State {
			continue
		}
		out = append(out, cloneEvent(event))
	}
	sortEvents(out)
	return pageEvents(out, in.Limit, in.Offset), nil
}

func (s *MemoryStore) CreateSilence(_ context.Context, silence AlertSilence) (AlertSilence, error) {
	if s == nil {
		return AlertSilence{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	silence.ID = s.nextMuteID
	s.nextMuteID++
	s.silences[silence.ID] = cloneSilence(silence)
	return cloneSilence(silence), nil
}

func (s *MemoryStore) DeleteSilence(_ context.Context, tenantID, id int64) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	silence, ok := s.silences[id]
	if !ok || silence.TenantID != tenantID {
		return ErrNotFound
	}
	delete(s.silences, id)
	return nil
}

func (s *MemoryStore) ListSilences(_ context.Context, in ListSilencesInput) ([]AlertSilence, error) {
	if s == nil {
		return nil, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AlertSilence, 0, len(s.silences))
	for _, silence := range s.silences {
		if silence.TenantID == in.TenantID {
			out = append(out, cloneSilence(silence))
		}
	}
	sortSilences(out)
	return pageSilences(out, in.Limit, in.Offset), nil
}

func (s *MemoryStore) ListActiveSilences(_ context.Context, tenantID int64, now time.Time) ([]AlertSilence, error) {
	if s == nil {
		return nil, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AlertSilence, 0, len(s.silences))
	for _, silence := range s.silences {
		if silence.TenantID != tenantID {
			continue
		}
		if !silence.StartsAt.After(now) && !silence.EndsAt.Before(now) {
			out = append(out, cloneSilence(silence))
		}
	}
	sortSilences(out)
	return out, nil
}

func ruleNameExists(rules map[int64]AlertRule, tenantID, excludeID int64, name string) bool {
	for _, rule := range rules {
		if rule.ID != excludeID && rule.TenantID == tenantID && rule.Name == name {
			return true
		}
	}
	return false
}

func sortRules(items []AlertRule) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
}

func sortEvents(items []AlertEvent) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].FiredAt.Equal(items[j].FiredAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].FiredAt.After(items[j].FiredAt)
	})
}

func sortSilences(items []AlertSilence) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].EndsAt.Equal(items[j].EndsAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].EndsAt.After(items[j].EndsAt)
	})
}

func pageRules(items []AlertRule, limit, offset int) []AlertRule {
	if offset >= len(items) {
		return []AlertRule{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	out := make([]AlertRule, end-offset)
	copy(out, items[offset:end])
	return out
}

func pageEvents(items []AlertEvent, limit, offset int) []AlertEvent {
	if offset >= len(items) {
		return []AlertEvent{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	out := make([]AlertEvent, end-offset)
	copy(out, items[offset:end])
	return out
}

func pageSilences(items []AlertSilence, limit, offset int) []AlertSilence {
	if offset >= len(items) {
		return []AlertSilence{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	out := make([]AlertSilence, end-offset)
	copy(out, items[offset:end])
	return out
}

func cloneEvent(event AlertEvent) AlertEvent {
	event.ResolvedAt = timePtr(event.ResolvedAt)
	event.ThresholdValue = float64PtrFromPtr(event.ThresholdValue)
	event.MetricValue = float64PtrFromPtr(event.MetricValue)
	event.Dimensions = normalizeStringMap(event.Dimensions)
	return event
}

func cloneSilence(silence AlertSilence) AlertSilence {
	silence.RuleID = int64Ptr(silence.RuleID)
	return silence
}

func cloneRule(rule AlertRule) AlertRule {
	rule.Filters = normalizeStringMap(rule.Filters)
	rule.LastTriggeredAt = timePtr(rule.LastTriggeredAt)
	return rule
}

func timePtr(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	t := in.UTC()
	return &t
}

func float64Ptr(in float64) *float64 {
	v := in
	return &v
}

func float64PtrFromPtr(in *float64) *float64 {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}
