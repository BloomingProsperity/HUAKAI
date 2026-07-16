package alerting

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

const (
	defaultListLimit = 50
	maxListLimit     = 500
	// MaxRuleWindow 限制单条规则可请求的聚合区间，避免误配置触发无界历史扫描。
	MaxRuleWindow = 24 * time.Hour
)

type Service struct {
	store                     Store
	now                       func() time.Time
	firingDeliverer           FiringDeliverer
	firingEmailDeliverer      FiringEmailDeliverer
	firingDeliveryErrRecorder FiringDeliveryErrorRecorder
	mu                        sync.Mutex
	breachStarts              map[ruleRuntimeKey]time.Time
	deliveryLimiter           *firingDeliveryLimiter
}

type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

func WithFiringDeliverer(deliverer FiringDeliverer) Option {
	return func(s *Service) {
		s.firingDeliverer = deliverer
	}
}

func WithFiringEmailDeliverer(deliverer FiringEmailDeliverer) Option {
	return func(s *Service) {
		s.firingEmailDeliverer = deliverer
	}
}

func WithFiringDeliveryErrorRecorder(record FiringDeliveryErrorRecorder) Option {
	return func(s *Service) {
		s.firingDeliveryErrRecorder = record
	}
}

func WithFiringDeliveryRateLimit(window time.Duration) Option {
	return func(s *Service) {
		if window > 0 {
			s.deliveryLimiter = newFiringDeliveryLimiter(window)
		}
	}
}

func NewService(store Store, opts ...Option) *Service {
	s := &Service{
		store:        store,
		now:          func() time.Time { return time.Now().UTC() },
		breachStarts: make(map[ruleRuntimeKey]time.Time),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) CreateRule(ctx context.Context, in CreateRuleInput) (AlertRule, error) {
	if s == nil || s.store == nil {
		return AlertRule{}, ErrStoreNotConfigured
	}
	now := s.now().UTC()
	rule := AlertRule{
		TenantID:         in.TenantID,
		Name:             strings.TrimSpace(in.Name),
		Metric:           strings.TrimSpace(in.Metric),
		MetricType:       MetricType(strings.TrimSpace(string(in.MetricType))),
		Comparator:       in.Comparator,
		Threshold:        in.Threshold,
		Severity:         in.Severity,
		WindowSeconds:    in.WindowSeconds,
		SustainedSeconds: in.SustainedSeconds,
		CooldownSeconds:  in.CooldownSeconds,
		NotifyEmail:      in.NotifyEmail,
		Filters:          normalizeStringMap(in.Filters),
		Enabled:          true,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if rule.Severity == "" {
		rule.Severity = SeverityInfo
	}
	if in.Enabled != nil {
		rule.Enabled = *in.Enabled
	}
	if err := validateRule(rule); err != nil {
		return AlertRule{}, err
	}
	return s.store.CreateRule(ctx, rule)
}

func (s *Service) UpdateRule(ctx context.Context, in UpdateRuleInput) (AlertRule, error) {
	if s == nil || s.store == nil {
		return AlertRule{}, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 || in.ID <= 0 {
		return AlertRule{}, ErrInvalidInput
	}
	rule, err := s.store.GetRule(ctx, in.TenantID, in.ID)
	if err != nil {
		return AlertRule{}, err
	}
	if in.Name != nil {
		rule.Name = strings.TrimSpace(*in.Name)
	}
	if in.Metric != nil {
		rule.Metric = strings.TrimSpace(*in.Metric)
	}
	if in.MetricType != nil {
		rule.MetricType = MetricType(strings.TrimSpace(string(*in.MetricType)))
	}
	if in.Comparator != nil {
		rule.Comparator = *in.Comparator
	}
	if in.Threshold != nil {
		rule.Threshold = *in.Threshold
	}
	if in.Severity != nil {
		rule.Severity = *in.Severity
	}
	if in.WindowSeconds != nil {
		rule.WindowSeconds = *in.WindowSeconds
	}
	if in.SustainedSeconds != nil {
		rule.SustainedSeconds = *in.SustainedSeconds
	}
	if in.CooldownSeconds != nil {
		rule.CooldownSeconds = *in.CooldownSeconds
	}
	if in.NotifyEmail != nil {
		rule.NotifyEmail = *in.NotifyEmail
	}
	if in.Filters != nil {
		rule.Filters = normalizeStringMap(*in.Filters)
	}
	if in.Enabled != nil {
		rule.Enabled = *in.Enabled
	}
	rule.UpdatedAt = s.now().UTC()
	if err := validateRule(rule); err != nil {
		return AlertRule{}, err
	}
	return s.store.UpdateRule(ctx, rule)
}

func (s *Service) DeleteRule(ctx context.Context, tenantID, id int64) error {
	if s == nil || s.store == nil {
		return ErrStoreNotConfigured
	}
	if tenantID <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	return s.store.DeleteRule(ctx, tenantID, id)
}

func (s *Service) GetRule(ctx context.Context, tenantID, id int64) (AlertRule, error) {
	if s == nil || s.store == nil {
		return AlertRule{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || id <= 0 {
		return AlertRule{}, ErrInvalidInput
	}
	return s.store.GetRule(ctx, tenantID, id)
}

func (s *Service) ListRules(ctx context.Context, in ListRulesInput) ([]AlertRule, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 {
		return nil, ErrInvalidInput
	}
	if err := normalizePage(&in.Limit, &in.Offset); err != nil {
		return nil, err
	}
	return s.store.ListRules(ctx, in)
}

func (s *Service) ListEvents(ctx context.Context, in ListEventsInput) ([]AlertEvent, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 {
		return nil, ErrInvalidInput
	}
	if in.RuleID != nil && *in.RuleID <= 0 {
		return nil, ErrInvalidInput
	}
	if in.State != "" && in.State != EventStateFiring && in.State != EventStateResolved && in.State != EventStateManualResolved {
		return nil, ErrInvalidInput
	}
	if err := normalizePage(&in.Limit, &in.Offset); err != nil {
		return nil, err
	}
	return s.store.ListEvents(ctx, in)
}

func (s *Service) CreateSilence(ctx context.Context, in CreateSilenceInput) (AlertSilence, error) {
	if s == nil || s.store == nil {
		return AlertSilence{}, ErrStoreNotConfigured
	}
	now := s.now().UTC()
	silence := AlertSilence{
		TenantID:  in.TenantID,
		RuleID:    int64Ptr(in.RuleID),
		Reason:    strings.TrimSpace(in.Reason),
		StartsAt:  in.StartsAt.UTC(),
		EndsAt:    in.EndsAt.UTC(),
		Platform:  strings.TrimSpace(in.Platform),
		GroupID:   strings.TrimSpace(in.GroupID),
		Region:    strings.TrimSpace(in.Region),
		CreatedAt: now,
	}
	if err := validateSilence(silence); err != nil {
		return AlertSilence{}, err
	}
	return s.store.CreateSilence(ctx, silence)
}

func (s *Service) DeleteSilence(ctx context.Context, tenantID, id int64) error {
	if s == nil || s.store == nil {
		return ErrStoreNotConfigured
	}
	if tenantID <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	return s.store.DeleteSilence(ctx, tenantID, id)
}

func (s *Service) ListSilences(ctx context.Context, in ListSilencesInput) ([]AlertSilence, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 {
		return nil, ErrInvalidInput
	}
	if err := normalizePage(&in.Limit, &in.Offset); err != nil {
		return nil, err
	}
	return s.store.ListSilences(ctx, in)
}

func (s *Service) ManualResolveEvent(ctx context.Context, tenantID, eventID int64) (AlertEvent, error) {
	if s == nil || s.store == nil {
		return AlertEvent{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || eventID <= 0 {
		return AlertEvent{}, ErrInvalidInput
	}
	return s.store.ManualResolveEvent(ctx, tenantID, eventID, s.now().UTC())
}

func (s *Service) EvaluateRules(ctx context.Context, tenantID int64, metricSnapshot map[string]float64) error {
	return s.evaluateRules(ctx, tenantID, func(AlertRule) (map[string]float64, error) {
		return metricSnapshot, nil
	})
}

type metricSnapshotResolver func(AlertRule) (map[string]float64, error)

func (s *Service) evaluateRules(ctx context.Context, tenantID int64, resolveSnapshot metricSnapshotResolver) error {
	if s == nil || s.store == nil {
		return ErrStoreNotConfigured
	}
	if tenantID <= 0 {
		return ErrInvalidInput
	}
	now := s.now().UTC()
	rules, err := s.store.ListEnabledRules(ctx, tenantID)
	if err != nil {
		return err
	}
	silences, err := s.store.ListActiveSilences(ctx, tenantID, now)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		metricSnapshot, err := resolveSnapshot(rule)
		if err != nil {
			return err
		}
		metricKey := metricKeyForRule(rule)
		observed, ok := metricSnapshot[metricKey]
		if !ok || math.IsNaN(observed) || math.IsInf(observed, 0) {
			continue
		}
		if breaches(rule.Comparator, observed, rule.Threshold) {
			if !s.sustainedReady(rule, now) {
				continue
			}
			if cooldownActive(rule, now) {
				continue
			}
			dimensions := normalizeStringMap(rule.Filters)
			event, created, err := s.store.UpsertFiringEvent(ctx, UpsertFiringEventInput{
				TenantID:       tenantID,
				RuleID:         rule.ID,
				ObservedValue:  observed,
				ThresholdValue: rule.Threshold,
				MetricValue:    observed,
				Dimensions:     dimensions,
				FiredAt:        now,
			})
			if err != nil {
				return err
			}
			if created {
				s.clearBreachStart(tenantID, rule.ID)
				if err := s.store.MarkRuleTriggered(ctx, tenantID, rule.ID, now); err != nil {
					return err
				}
				if !silenceMatches(rule.ID, dimensions, silences) {
					// 原有多渠道通知始终保留；NotifyEmail 只控制新增的管理员邮件链，
					// 因此默认 false 时不会缩减既有通知行为。
					delivered := s.deliverFiring(ctx, tenantID, rule, observed, event.FiredAt)
					emailDelivered := s.deliverFiringEmail(ctx, tenantID, rule, observed, event.FiredAt)
					if delivered || emailDelivered {
						if _, err := s.store.MarkEventEmailSent(ctx, tenantID, event.ID); err != nil {
							return err
						}
					}
				}
			}
			continue
		}
		s.clearBreachStart(tenantID, rule.ID)
		if _, _, err := s.store.ResolveFiringEvent(ctx, tenantID, rule.ID, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) deliverFiring(ctx context.Context, tenantID int64, rule AlertRule, observed float64, firedAt time.Time) bool {
	if s == nil || s.firingDeliverer == nil {
		return false
	}
	if s.deliveryLimiter != nil && !s.deliveryLimiter.allow(tenantID, rule.ID, firedAt.UTC()) {
		return false
	}
	notice := firingNotice(rule, observed, firedAt)
	if err := s.firingDeliverer.DeliverFiring(ctx, tenantID, notice); err != nil && s.firingDeliveryErrRecorder != nil {
		s.firingDeliveryErrRecorder(ctx, tenantID, notice, err)
		return false
	} else if err != nil {
		return false
	}
	return true
}

func (s *Service) deliverFiringEmail(ctx context.Context, tenantID int64, rule AlertRule, observed float64, firedAt time.Time) bool {
	if s == nil || !rule.NotifyEmail || s.firingEmailDeliverer == nil {
		return false
	}
	notice := firingNotice(rule, observed, firedAt)
	delivered, err := s.firingEmailDeliverer.DeliverFiringEmail(ctx, tenantID, notice)
	if err != nil && s.firingDeliveryErrRecorder != nil {
		s.firingDeliveryErrRecorder(ctx, tenantID, notice, err)
		return false
	}
	return err == nil && delivered
}

func firingNotice(rule AlertRule, observed float64, firedAt time.Time) FiringNotice {
	return FiringNotice{
		RuleID:        rule.ID,
		RuleName:      rule.Name,
		Metric:        metricKeyForRule(rule),
		MetricType:    rule.MetricType,
		Comparator:    rule.Comparator,
		Threshold:     rule.Threshold,
		Severity:      rule.Severity,
		ObservedValue: observed,
		Dimensions:    normalizeStringMap(rule.Filters),
		FiredAt:       firedAt.UTC(),
	}
}

func validateRule(rule AlertRule) error {
	if rule.TenantID <= 0 || strings.TrimSpace(rule.Name) == "" || metricKeyForRule(rule) == "" {
		return ErrInvalidInput
	}
	switch rule.Comparator {
	case ComparatorGT, ComparatorGTE, ComparatorLT, ComparatorLTE:
	default:
		return ErrInvalidInput
	}
	if math.IsNaN(rule.Threshold) || math.IsInf(rule.Threshold, 0) {
		return ErrInvalidInput
	}
	switch rule.Severity {
	case SeverityInfo, SeverityWarning, SeverityCritical:
	default:
		return ErrInvalidInput
	}
	if rule.WindowSeconds <= 0 || time.Duration(rule.WindowSeconds)*time.Second > MaxRuleWindow {
		return ErrInvalidInput
	}
	if rule.SustainedSeconds < 0 || rule.CooldownSeconds < 0 {
		return ErrInvalidInput
	}
	return nil
}

func validateSilence(silence AlertSilence) error {
	if silence.TenantID <= 0 {
		return ErrInvalidInput
	}
	if silence.RuleID != nil && *silence.RuleID <= 0 {
		return ErrInvalidInput
	}
	if silence.StartsAt.IsZero() || silence.EndsAt.IsZero() || !silence.EndsAt.After(silence.StartsAt) {
		return ErrInvalidInput
	}
	return nil
}

func normalizePage(limit, offset *int) error {
	if *limit == 0 {
		*limit = defaultListLimit
	}
	if *limit < 0 || *limit > maxListLimit || *offset < 0 {
		return ErrInvalidInput
	}
	return nil
}

func breaches(cmp Comparator, observed, threshold float64) bool {
	switch cmp {
	case ComparatorGT:
		return observed > threshold
	case ComparatorGTE:
		return observed >= threshold
	case ComparatorLT:
		return observed < threshold
	case ComparatorLTE:
		return observed <= threshold
	default:
		return false
	}
}

func silenceMatches(ruleID int64, dimensions map[string]string, silences []AlertSilence) bool {
	for _, silence := range silences {
		if silence.RuleID != nil && *silence.RuleID != ruleID {
			continue
		}
		if silenceScopeMatches(silence, dimensions) {
			return true
		}
	}
	return false
}

func silenceScopeMatches(silence AlertSilence, dimensions map[string]string) bool {
	if silence.Platform == "" && silence.GroupID == "" && silence.Region == "" {
		return true
	}
	if silence.Platform != "" && dimensions["platform"] != silence.Platform {
		return false
	}
	if silence.GroupID != "" && dimensions["group_id"] != silence.GroupID {
		return false
	}
	if silence.Region != "" && dimensions["region"] != silence.Region {
		return false
	}
	return true
}

func metricKeyForRule(rule AlertRule) string {
	if metricType := strings.TrimSpace(string(rule.MetricType)); metricType != "" {
		return metricType
	}
	return strings.TrimSpace(rule.Metric)
}

func cooldownActive(rule AlertRule, now time.Time) bool {
	if rule.CooldownSeconds <= 0 || rule.LastTriggeredAt == nil {
		return false
	}
	return now.Sub(rule.LastTriggeredAt.UTC()) < time.Duration(rule.CooldownSeconds)*time.Second
}

func (s *Service) sustainedReady(rule AlertRule, now time.Time) bool {
	if rule.SustainedSeconds <= 0 {
		return true
	}
	key := ruleRuntimeKey{tenantID: rule.TenantID, ruleID: rule.ID}
	s.mu.Lock()
	defer s.mu.Unlock()
	start, ok := s.breachStarts[key]
	if !ok {
		s.breachStarts[key] = now.UTC()
		return false
	}
	return !now.UTC().Before(start.UTC().Add(time.Duration(rule.SustainedSeconds) * time.Second))
}

func (s *Service) clearBreachStart(tenantID, ruleID int64) {
	key := ruleRuntimeKey{tenantID: tenantID, ruleID: ruleID}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.breachStarts, key)
}

type ruleRuntimeKey struct {
	tenantID int64
	ruleID   int64
}

type firingDeliveryLimiter struct {
	mu     sync.Mutex
	window time.Duration
	last   map[string]time.Time
}

func newFiringDeliveryLimiter(window time.Duration) *firingDeliveryLimiter {
	return &firingDeliveryLimiter{window: window, last: make(map[string]time.Time)}
}

func (l *firingDeliveryLimiter) allow(tenantID, ruleID int64, now time.Time) bool {
	if l == nil || l.window <= 0 {
		return true
	}
	key := fmt.Sprintf("%d:%d", tenantID, ruleID)
	l.mu.Lock()
	defer l.mu.Unlock()
	if last, ok := l.last[key]; ok && now.Sub(last) < l.window {
		return false
	}
	l.last[key] = now
	return true
}

func normalizeStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	return out
}

func int64Ptr(in *int64) *int64 {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}
