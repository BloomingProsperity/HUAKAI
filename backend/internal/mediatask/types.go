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
	ErrInvalidOrphanStatus   = errors.New("mediatask: invalid orphan reconcile status")
)

type Config struct {
	Enabled         bool
	ProviderBaseURL string
	PollInterval    time.Duration
	TaskTimeout     time.Duration
	// ProviderCallTimeout 是单次上游 Submit/Poll HTTP 调用的硬上限。媒体 provider 是真实外部服务,
	// Submit/Poll 本应秒级返回(异步任务在 provider 侧跑,这里只提交/查状态);若不设上限,慢上游/半开连接
	// 会让单 goroutine 串行 worker 永久挂起、整个媒体子系统停摆、预扣资金久冻(<=0 时 withDefaults 兜默认)。
	ProviderCallTimeout   time.Duration
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
	// ClaimLeaseWindow 是 billing claim 的孤儿回收租约窗口,必须 > 媒体任务最大
	// 生命周期(TaskTimeout)。否则跑得久的合法任务的 claim 会被 billing LeaseSweeper
	// 提前误 abort、预扣费释放,任务完成时无法 commit 计费致亏钱。<=0 时 store 回退到
	// defaultMediaClaimLeaseWindow。见 resolveClaimLeaseWindow。
	ClaimLeaseWindow time.Duration
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
		c.TaskTimeout = defaultTaskTimeout
	}
	if c.ProviderCallTimeout <= 0 {
		c.ProviderCallTimeout = defaultProviderCallTimeout
	}
	if c.RequestClass == "" {
		c.RequestClass = "standard"
	}
	return c
}

// providerCallTimeout 返回单次上游调用超时,即使 cfg 未经 withDefaults 也兜底(防御性,绝不返回 <=0)。
func (c Config) providerCallTimeout() time.Duration {
	if c.ProviderCallTimeout > 0 {
		return c.ProviderCallTimeout
	}
	return defaultProviderCallTimeout
}

const (
	// defaultTaskTimeout 是 TaskTimeout 缺省值的单一真相源,withDefaults 与
	// defaultMediaClaimLeaseWindow 共用,避免两处字面量失同步(改一处即可)。
	defaultTaskTimeout = 15 * time.Minute
	// defaultProviderCallTimeout 是单次上游 Submit/Poll HTTP 调用的默认硬上限。取 20s:Submit/Poll 应秒级
	// 返回,留足慢网络余量;且 < worker LeaseTTL(默认 30s),确保单次调用绝不会跨过租约导致重复提交/孤儿。
	defaultProviderCallTimeout = 20 * time.Second
	// claimLeaseGrace 是 billing claim 孤儿回收租约在媒体任务超时之外额外预留的余量,
	// 确保 mediatask 自身的 TaskTimeout 超时处理(worker ExpireTask)总是先于 billing
	// LeaseSweeper 动作,避免合法长任务的 claim 被 sweeper 提前 abort(亏钱)。
	claimLeaseGrace = 5 * time.Minute
	// defaultMediaClaimLeaseWindow 是 ClaimLeaseWindow 缺省(<=0)时的回退值,取默认
	// TaskTimeout + grace。必须 > 默认 TaskTimeout,杜绝回退到过短窗口。
	defaultMediaClaimLeaseWindow = defaultTaskTimeout + claimLeaseGrace
)

// resolveClaimLeaseWindow 返回 billing claim 的孤儿回收租约窗口:调用方传入的窗口
// (通常 = TaskTimeout + grace);<=0 时回退到 defaultMediaClaimLeaseWindow。永不返回
// 过短窗口,杜绝 90s 那种 < TaskTimeout 导致合法长任务 claim 被 LeaseSweeper 提前
// abort 的 money 缺陷。
func resolveClaimLeaseWindow(requested time.Duration) time.Duration {
	if requested <= 0 {
		return defaultMediaClaimLeaseWindow
	}
	return requested
}
