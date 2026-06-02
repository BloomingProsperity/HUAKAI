package quota

import "errors"

var (
	// ErrDenied 表示配额强制模式下拒绝本次请求。
	ErrDenied = errors.New("quota: denied")
	// ErrRetryable 表示调用方可以重试同一 quota 操作。
	ErrRetryable = errors.New("quota: retryable")
	// ErrReconciliationNeeded 表示主路径已完成, 但 quota 补偿任务必须入队。
	ErrReconciliationNeeded = errors.New("quota: reconciliation needed")
	// ErrReservationReplayConflict 表示同一 claim 的重放身份不一致, 不得复用既有 reservation。
	ErrReservationReplayConflict = errors.New("quota: reservation replay conflict")
)

// DenyError 带上可审计的拒绝决策。
type DenyError struct {
	Decision Decision
	Cause    error
}

func (e *DenyError) Error() string {
	if e == nil {
		return ErrDenied.Error()
	}
	if e.Decision.Code != "" {
		return "quota: denied: " + e.Decision.Code
	}
	return ErrDenied.Error()
}

func (e *DenyError) Unwrap() error {
	if e == nil || e.Cause == nil {
		return ErrDenied
	}
	return e.Cause
}

func (e *DenyError) Is(target error) bool {
	return target == ErrDenied
}

// IsDenied 判断错误是否属于 quota deny 分类。
func IsDenied(err error) bool {
	return errors.Is(err, ErrDenied)
}

// RetryableError 包装可重试的存储/事务错误。
type RetryableError struct {
	Operation string
	Cause     error
}

func (e *RetryableError) Error() string {
	if e == nil || e.Operation == "" {
		return ErrRetryable.Error()
	}
	return "quota: retryable: " + e.Operation
}

func (e *RetryableError) Unwrap() error {
	if e == nil || e.Cause == nil {
		return ErrRetryable
	}
	return e.Cause
}

func (e *RetryableError) Is(target error) bool {
	return target == ErrRetryable
}

// IsRetryable 判断错误是否属于可重试分类。
func IsRetryable(err error) bool {
	return errors.Is(err, ErrRetryable)
}

// ReconciliationNeededError 记录需要后台补偿的 claim/reservation。
type ReconciliationNeededError struct {
	TenantID      int64
	ClaimID       int64
	ReservationID int64
	Kind          string
	Cause         error
}

func (e *ReconciliationNeededError) Error() string {
	if e == nil || e.Kind == "" {
		return ErrReconciliationNeeded.Error()
	}
	return "quota: reconciliation needed: " + e.Kind
}

func (e *ReconciliationNeededError) Unwrap() error {
	if e == nil || e.Cause == nil {
		return ErrReconciliationNeeded
	}
	return e.Cause
}

func (e *ReconciliationNeededError) Is(target error) bool {
	return target == ErrReconciliationNeeded
}

// IsReconciliationNeeded 判断错误是否需要 quota reconciliation job。
func IsReconciliationNeeded(err error) bool {
	return errors.Is(err, ErrReconciliationNeeded)
}
