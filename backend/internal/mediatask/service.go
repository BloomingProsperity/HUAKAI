package mediatask

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Store interface {
	CreateTask(context.Context, CreateTaskInput) (Task, bool, error)
	GetTask(context.Context, int64, int64, int64) (Task, error)
	ListTasks(context.Context, int64, int64, int) ([]Task, error)
	AcquireLease(context.Context, string, time.Duration, time.Time) (Task, error)
	MarkProviderSubmitted(context.Context, Task, string, string, time.Time) (Task, error)
	UpdateProgress(context.Context, Task, string, int, time.Time) error
	CompleteSuccess(context.Context, Task, string, PollResult, time.Time) (bool, error)
	CompleteFailure(context.Context, Task, string, string, time.Time) (bool, error)
	ExpireTask(context.Context, Task, string, time.Time) (bool, error)
}

type Service struct {
	store    Store
	configs  ConfigSource
	registry ProviderRegistry
}

func NewService(store Store, configs ConfigSource, registry ProviderRegistry) *Service {
	return &Service{store: store, configs: configs, registry: registry}
}

func (s *Service) Submit(ctx context.Context, tenantID, userID int64, input SubmitInput) (Task, error) {
	cfg, err := s.enabledConfig(ctx)
	if err != nil {
		return Task{}, err
	}
	if s.store == nil {
		return Task{}, ErrStoreNotConfigured
	}
	input, err = normalizeSubmitInput(input)
	if err != nil {
		return Task{}, err
	}
	if _, ok, err := s.lookupProvider(ctx, input.Provider); err != nil || !ok {
		if err != nil {
			return Task{}, err
		}
		return Task{}, ErrProviderUnavailable
	}
	estimated, err := EstimateCents(ctx, cfg, input.TaskType)
	if err != nil {
		return Task{}, err
	}
	task, _, err := s.store.CreateTask(ctx, CreateTaskInput{
		TenantID: tenantID, UserID: userID, RequestID: input.RequestID,
		TaskType: input.TaskType, Provider: input.Provider, InputParams: input.InputParams,
		EstimatedCents: estimated, BillingPolicyVersion: cfg.BillingPolicyVersion,
		RequestClass: cfg.RequestClass,
		// claim 孤儿回收租约必须覆盖任务整个生命周期(TaskTimeout)再加余量,随运维
		// 调整 TaskTimeout 自动跟随,避免 billing LeaseSweeper 在任务跑完前误 abort。
		ClaimLeaseWindow: cfg.TaskTimeout + claimLeaseGrace,
	})
	return task, err
}

func (s *Service) Status(ctx context.Context, tenantID, userID, id int64) (Task, error) {
	if _, err := s.enabledConfig(ctx); err != nil {
		return Task{}, err
	}
	if s.store == nil {
		return Task{}, ErrStoreNotConfigured
	}
	return s.store.GetTask(ctx, tenantID, userID, id)
}

func (s *Service) List(ctx context.Context, tenantID, userID int64, limit int) ([]Task, error) {
	if _, err := s.enabledConfig(ctx); err != nil {
		return nil, err
	}
	if s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	return s.store.ListTasks(ctx, tenantID, userID, limit)
}

func (s *Service) enabledConfig(ctx context.Context) (Config, error) {
	if s == nil || s.configs == nil {
		return Config{}, ErrDisabled
	}
	cfg, err := s.configs.Load(ctx)
	if err != nil {
		return Config{}, err
	}
	cfg = cfg.withDefaults()
	if !cfg.Enabled {
		return Config{}, ErrDisabled
	}
	return cfg, nil
}

func (s *Service) lookupProvider(ctx context.Context, name string) (AsyncMediaProvider, bool, error) {
	if s.registry == nil {
		return nil, false, nil
	}
	return s.registry.Provider(ctx, name)
}

func normalizeSubmitInput(input SubmitInput) (SubmitInput, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.TaskType = strings.TrimSpace(input.TaskType)
	input.Provider = strings.TrimSpace(input.Provider)
	if input.RequestID == "" || input.TaskType == "" || input.Provider == "" {
		return SubmitInput{}, fmt.Errorf("%w: request_id/task_type/provider", ErrInvalidInput)
	}
	if len(input.InputParams) == 0 {
		input.InputParams = json.RawMessage(`{}`)
	}
	if !json.Valid(input.InputParams) {
		return SubmitInput{}, fmt.Errorf("%w: input_params", ErrInvalidInput)
	}
	return input, nil
}
