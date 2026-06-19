package mediatask

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

type Status string

const (
	StatusQueued     Status = "queued"
	StatusInProgress Status = "in_progress"
	StatusSucceeded  Status = "succeeded"
	StatusFailed     Status = "failed"
	StatusExpired    Status = "expired"
)

var (
	ErrDisabled              = errors.New("mediatask: disabled")
	ErrInvalidInput          = errors.New("mediatask: invalid input")
	ErrRequestIDConflict     = errors.New("mediatask: request id conflict")
	ErrNotFound              = errors.New("mediatask: not found")
	ErrNoActiveAPIKey        = errors.New("mediatask: no active api key for user")
	ErrProviderUnavailable   = errors.New("mediatask: provider unavailable")
	ErrNoRunnableTask        = errors.New("mediatask: no runnable task")
	ErrLeaseLost             = errors.New("mediatask: lease lost")
	ErrActualExceedsEstimate = errors.New("mediatask: actual cost exceeds estimate")
	ErrStoreNotConfigured    = errors.New("mediatask: store not configured")
)

type Config struct {
	Enabled               bool
	ProviderBaseURL       string
	PollInterval          time.Duration
	TaskTimeout           time.Duration
	DefaultEstimatedCents map[string]int64
	BillingPolicyVersion  string
	RequestClass          string
}

type ConfigSource interface {
	Load(context.Context) (Config, error)
}

type StaticConfigSource struct {
	Config Config
}

func (s StaticConfigSource) Load(context.Context) (Config, error) {
	return s.Config.withDefaults(), nil
}

type SubmitInput struct {
	RequestID   string          `json:"request_id"`
	TaskType    string          `json:"task_type"`
	Provider    string          `json:"provider"`
	InputParams json.RawMessage `json:"input_params"`
}

type CreateTaskInput struct {
	TenantID             int64
	UserID               int64
	RequestID            string
	TaskType             string
	Provider             string
	InputParams          json.RawMessage
	EstimatedCents       int64
	BillingPolicyVersion string
	RequestClass         string
}

type Task struct {
	ID             int64           `json:"id"`
	TenantID       int64           `json:"tenant_id"`
	UserID         int64           `json:"user_id"`
	TaskType       string          `json:"task_type"`
	Status         Status          `json:"status"`
	Provider       string          `json:"provider"`
	ProviderTaskID string          `json:"provider_task_id,omitempty"`
	RequestID      string          `json:"request_id"`
	InputParams    json.RawMessage `json:"input_params,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
	EstimatedCents int64           `json:"estimated_cents"`
	ActualCents    *int64          `json:"actual_cents,omitempty"`
	HoldRef        string          `json:"hold_ref,omitempty"`
	ErrorClass     string          `json:"error_class,omitempty"`
	Progress       int             `json:"progress"`
	LeaseOwner     string          `json:"-"`
	LeaseExpiresAt *time.Time      `json:"-"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
}

type SubmitReq struct {
	TaskID      int64           `json:"task_id"`
	RequestID   string          `json:"request_id"`
	TaskType    string          `json:"task_type"`
	InputParams json.RawMessage `json:"input_params"`
	// IdempotencyKey 是由任务身份(TaskID/RequestID)派生的稳定键,用于让上游侧把
	// 同一任务的重复提交去重到同一条上游记录。租约在 Submit 期间过期被第二个
	// worker 抢走时,两个 worker 会用同一个键再次提交,上游据此识别为同一任务,
	// 避免产生重复的、无人结算的孤儿上游成本。
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// DeriveIdempotencyKey 由任务身份派生稳定且确定的幂等键:同一任务无论被哪个
// worker 在何时提交都得到相同的键。优先用持久且不可变的数据库主键 TaskID;
// 仅当 TaskID 缺失(理论上不应发生)时回退到调用方提供的 RequestID。
func DeriveIdempotencyKey(taskID int64, requestID string) string {
	if taskID > 0 {
		return "mediatask-" + strconv.FormatInt(taskID, 10)
	}
	return strings.TrimSpace(requestID)
}

type PollResult struct {
	Status      Status          `json:"status"`
	Progress    int             `json:"progress"`
	Result      json.RawMessage `json:"result,omitempty"`
	ActualCents int64           `json:"actual_cents,omitempty"`
	ErrorClass  string          `json:"error_class,omitempty"`
}

type AsyncMediaProvider interface {
	Submit(context.Context, SubmitReq) (providerTaskID string, err error)
	Poll(context.Context, string) (PollResult, error)
}

type ProviderRegistry interface {
	Provider(context.Context, string) (AsyncMediaProvider, bool, error)
}

type StaticProviderRegistry map[string]AsyncMediaProvider

func (r StaticProviderRegistry) Provider(_ context.Context, name string) (AsyncMediaProvider, bool, error) {
	p, ok := r[name]
	return p, ok && p != nil, nil
}

func IsTerminal(status Status) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusExpired:
		return true
	default:
		return false
	}
}

func CanTransition(from, to Status) bool {
	if from == to {
		return true
	}
	switch from {
	case StatusQueued:
		return to == StatusInProgress || to == StatusFailed || to == StatusExpired
	case StatusInProgress:
		return to == StatusSucceeded || to == StatusFailed || to == StatusExpired
	default:
		return false
	}
}

func (c Config) withDefaults() Config {
	if c.PollInterval <= 0 {
		c.PollInterval = 5 * time.Second
	}
	if c.TaskTimeout <= 0 {
		c.TaskTimeout = 15 * time.Minute
	}
	if c.RequestClass == "" {
		c.RequestClass = "standard"
	}
	return c
}
