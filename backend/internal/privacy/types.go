package privacy

// SensitivityLabel 在字段抵达持久化落地点之前对其分类。
type SensitivityLabel string

const (
	NEVER_PERSIST   SensitivityLabel = "NEVER_PERSIST"
	SECRET_MATERIAL SensitivityLabel = "SECRET_MATERIAL"
	SENSITIVE_PII   SensitivityLabel = "SENSITIVE_PII"
	SAFE_METADATA   SensitivityLabel = "SAFE_METADATA"
	OPT_IN_PROOF    SensitivityLabel = "OPT_IN_PROOF"
)

const (
	SchemaVersion = "privacy.log.v1"

	RedactionResultClean      = "clean"
	RedactionResultRedacted   = "redacted"
	RedactionResultBlocked    = "blocked"
	ErrorClassPrivacyGuardHit = "privacy_guard_hit"
)
