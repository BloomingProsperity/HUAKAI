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
	MarkSubmitting(context.Context, Task, string, time.Time) (Task, error)
	DeferSubmission(context.Context, Task, string, time.Time, time.Time) error
	MarkSubmissionUnknown(context.Context, Task, string, string, string, time.Time) (Task, error)
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
	if err := ValidateProviderTaskType(input.Provider, input.TaskType); err != nil {
		return Task{}, err
	}
	if isDurablyBoundVideoProvider(input.Provider) && !hasDurableSubmitBinding(input) {
		return Task{}, fmt.Errorf("%w: durable video provider requires exact key, pool, account, protocol, model and route binding", ErrInvalidInput)
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
		APIKeyID:     input.APIKeyID, ProviderAccountID: input.ProviderAccountID,
		PoolGroupID: input.PoolGroupID, ProtocolFamily: input.ProtocolFamily,
		RequestedModel: input.RequestedModel, ProviderModelID: input.ProviderModelID,
		RouteID: input.RouteID, BindingID: input.BindingID,
		BindingRPMLimit: input.BindingRPMLimit, BindingTPMLimit: input.BindingTPMLimit,
		BindingMaxParallelRequests: input.BindingMaxParallelRequests,
		// claim 孤儿回收租约必须覆盖任务整个生命周期(TaskTimeout)再加余量,随运维
		// 调整 TaskTimeout 自动跟随,避免 billing LeaseSweeper 在任务跑完前误 abort。
		ClaimLeaseWindow: cfg.TaskTimeout + claimLeaseGrace,
	})
	return task, err
}

// ValidateProviderTaskType 在创建任务和冻结余额前核对厂商支持的操作。
func ValidateProviderTaskType(providerName, taskType string) error {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	taskType = strings.TrimSpace(taskType)
	switch providerName {
	case grokVideoProviderName:
		switch taskType {
		case "video_generate", "video_edit", "video_extend":
			return nil
		}
	case geminiVideoProviderName:
		if taskType == "video_generate" {
			return nil
		}
	default:
		return nil
	}
	return fmt.Errorf("%w: provider %s does not support task type %s", ErrInvalidInput, providerName, taskType)
}

func hasDurableSubmitBinding(input SubmitInput) bool {
	return input.APIKeyID > 0 && input.ProviderAccountID > 0 && input.PoolGroupID > 0 &&
		strings.TrimSpace(input.ProtocolFamily) != "" && strings.TrimSpace(input.RequestedModel) != "" &&
		strings.TrimSpace(input.ProviderModelID) != "" && strings.TrimSpace(input.RouteID) != "" &&
		input.BindingID > 0 && input.BindingRPMLimit >= 0 && input.BindingTPMLimit >= 0 &&
		input.BindingMaxParallelRequests >= 0
}

func (s *Service) StatusForAPIKey(ctx context.Context, tenantID, userID, apiKeyID int64, requestID string) (Task, error) {
	if s == nil || s.store == nil {
		return Task{}, ErrStoreNotConfigured
	}
	store, ok := s.store.(interface {
		GetTaskForAPIKey(context.Context, int64, int64, int64, string) (Task, error)
	})
	if !ok {
		return Task{}, ErrStoreNotConfigured
	}
	task, err := store.GetTaskForAPIKey(ctx, tenantID, userID, apiKeyID, strings.TrimSpace(requestID))
	return taskForClient(task), err
}

func (s *Service) ContentForAPIKey(ctx context.Context, tenantID, userID, apiKeyID int64, requestID string) (ContentResult, error) {
	task, err := s.StatusForAPIKey(ctx, tenantID, userID, apiKeyID, requestID)
	if err != nil {
		return ContentResult{}, err
	}
	if task.Status != StatusSucceeded {
		return ContentResult{}, ErrContentUnavailable
	}
	mediaProvider, ok, err := s.lookupProvider(ctx, task.Provider)
	if err != nil {
		return ContentResult{}, err
	}
	downloader, supported := mediaProvider.(BoundMediaContentProvider)
	if !ok || !supported || downloader == nil {
		return ContentResult{}, ErrContentUnavailable
	}
	return downloader.DownloadBound(ctx, task)
}

func (s *Service) Status(ctx context.Context, tenantID, userID, id int64) (Task, error) {
	if s == nil || s.store == nil {
		return Task{}, ErrStoreNotConfigured
	}
	task, err := s.store.GetTask(ctx, tenantID, userID, id)
	return taskForClient(task), err
}

func (s *Service) List(ctx context.Context, tenantID, userID int64, limit int) ([]Task, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	tasks, err := s.store.ListTasks(ctx, tenantID, userID, limit)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		tasks[i] = taskForClient(tasks[i])
	}
	return tasks, nil
}

// taskForClient 只在任务和计费都已收敛后交付产物。内部 store 仍保存完整结果，
// worker 可据此恢复 settlement_pending；用户查询面不能绕过结算提前拿到产物。
func taskForClient(task Task) Task {
	if task.Status != StatusSucceeded {
		task.Result = nil
	}
	return task
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
	if input.APIKeyID < 0 {
		return SubmitInput{}, fmt.Errorf("%w: api_key_id", ErrInvalidInput)
	}
	if len(input.InputParams) == 0 {
		input.InputParams = json.RawMessage(`{}`)
	}
	if !json.Valid(input.InputParams) {
		return SubmitInput{}, fmt.Errorf("%w: input_params", ErrInvalidInput)
	}
	return input, nil
}

func MatchesSubmission(task Task, input SubmitInput) bool {
	return task.TaskType == strings.TrimSpace(input.TaskType) &&
		task.Provider == strings.TrimSpace(input.Provider) &&
		(input.RequestedModel == "" || task.RequestedModel == strings.TrimSpace(input.RequestedModel)) &&
		jsonCanonicalEqual(task.InputParams, input.InputParams)
}
