package logcontract

import "regexp"

// 字段名是 slog、zap、持久化层和运营查询共享的机器合同。
const (
	FieldCategory          = "log_category"
	FieldEventType         = "event_type"
	FieldResult            = "result"
	FieldErrorClass        = "error_class"
	FieldErrorCode         = "error_code"
	FieldRetryable         = "retryable"
	FieldActorKind         = "actor_kind"
	FieldActorRef          = "actor_ref"
	FieldTenantID          = "tenant_id"
	FieldTargetType        = "target_type"
	FieldTargetRef         = "target_ref"
	FieldTraceID           = "trace_id"
	FieldUpstreamRequestID = "upstream_request_id"
	FieldIdempotencyKey    = "idempotency_key"
	FieldRecoveryState     = "recovery_state"
)

type Category string

const (
	CategoryOperation Category = "operation"
	CategoryFinancial Category = "financial"
	CategorySecurity  Category = "security"
	CategoryError     Category = "error"
	CategoryAccess    Category = "access"
	CategoryRecovery  Category = "recovery"
)

type Result string

const (
	ResultSuccess       Result = "success"
	ResultDenied        Result = "denied"
	ResultClientFailure Result = "client_failure"
	ResultServerFailure Result = "server_failure"
	ResultCanceled      Result = "canceled"
	ResultPartial       Result = "partial"
	ResultTimeout       Result = "timeout"
	ResultUnknown       Result = "unknown"
)

type ErrorClass string

const (
	ErrorNone                ErrorClass = "none"
	ErrorValidation          ErrorClass = "validation"
	ErrorAuthentication      ErrorClass = "authentication"
	ErrorAuthorization       ErrorClass = "authorization"
	ErrorConflict            ErrorClass = "conflict"
	ErrorInsufficientBalance ErrorClass = "insufficient_balance"
	ErrorRateLimit           ErrorClass = "rate_limit"
	ErrorTimeout             ErrorClass = "timeout"
	ErrorCanceled            ErrorClass = "canceled"
	ErrorDependency          ErrorClass = "dependency"
	ErrorDataIntegrity       ErrorClass = "data_integrity"
	ErrorManualRecovery      ErrorClass = "manual_recovery"
	ErrorUnknown             ErrorClass = "unknown"
)

type RecoveryState string

const (
	RecoveryNone             RecoveryState = "none"
	RecoveryPending          RecoveryState = "pending"
	RecoveryRetrying         RecoveryState = "retrying"
	RecoveryRecovered        RecoveryState = "recovered"
	RecoveryQuarantined      RecoveryState = "quarantined"
	RecoveryOperatorRequired RecoveryState = "operator_required"
	RecoveryFailed           RecoveryState = "failed"
)

type ActorKind string

const (
	ActorSystem        ActorKind = "system"
	ActorPlatformAdmin ActorKind = "platform_admin"
	ActorTenantAdmin   ActorKind = "tenant_admin"
	ActorUser          ActorKind = "user"
	ActorAPIKey        ActorKind = "api_key"
	ActorUnknown       ActorKind = "unknown"
)

var machineIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)

func ValidCategory(value string) bool {
	switch Category(value) {
	case CategoryOperation, CategoryFinancial, CategorySecurity, CategoryError, CategoryAccess, CategoryRecovery:
		return true
	default:
		return false
	}
}

func ValidResult(value string) bool {
	switch Result(value) {
	case ResultSuccess, ResultDenied, ResultClientFailure, ResultServerFailure,
		ResultCanceled, ResultPartial, ResultTimeout, ResultUnknown:
		return true
	default:
		return false
	}
}

func ValidErrorClass(value string) bool {
	switch ErrorClass(value) {
	case ErrorNone, ErrorValidation, ErrorAuthentication, ErrorAuthorization,
		ErrorConflict, ErrorInsufficientBalance, ErrorRateLimit, ErrorTimeout,
		ErrorCanceled, ErrorDependency, ErrorDataIntegrity, ErrorManualRecovery, ErrorUnknown:
		return true
	default:
		return false
	}
}

func ValidRecoveryState(value string) bool {
	switch RecoveryState(value) {
	case RecoveryNone, RecoveryPending, RecoveryRetrying, RecoveryRecovered,
		RecoveryQuarantined, RecoveryOperatorRequired, RecoveryFailed:
		return true
	default:
		return false
	}
}

func ValidActorKind(value string) bool {
	switch ActorKind(value) {
	case ActorSystem, ActorPlatformAdmin, ActorTenantAdmin, ActorUser, ActorAPIKey, ActorUnknown:
		return true
	default:
		return false
	}
}

func ValidMachineIdentifier(value string) bool {
	return machineIdentifier.MatchString(value)
}
