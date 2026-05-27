// 从 docs/openapi/openapi.yaml 推导的 TypeScript 类型
// 所有字段名保持 snake_case（与后端 JSON 一致）

// ---- 分页 ----

export interface PageMeta {
  cursor: string | null;
  next_cursor: string | null;
  has_more: boolean;
}

// ---- Provider Accounts ----

export type AccountType = 'oauth' | 'api_key' | 'service_account' | 'upstream_static';

export type HealthState = 'operational' | 'degraded' | 'failed' | 'cooling_down' | 'error';

export type CredentialState =
  | 'valid'
  | 'refreshing'
  | 'refreshing_with_grace'
  | 'refresh_failed'
  | 'revoked';

export type OAuthEndpointHealth = 'operational' | 'degraded' | 'circuit_open';

export interface ProviderAccount {
  id: number;
  tenant_id: number;
  provider_id: number;
  channel_id: number;
  name: string;
  account_type: AccountType;
  enabled: boolean;
  expires_at: string | null;
  health_state: HealthState;
  credential_state: CredentialState;
  cap_concurrency: number;
  in_flight_count: number;
  priority: number;
  last_dispatch_at: string | null;
  model_allow_list: string[];
  capability_flags: string[];
  rate_limited_at: string | null;
  rate_limit_reset_at: string | null;
  rate_limit_reason: string | null;
  overload_until: string | null;
  temp_unschedulable_until: string | null;
  token_version: number;
  last_refresh_at: string | null;
  last_refresh_outcome: string | null;
  oauth_endpoint_health?: OAuthEndpointHealth;
  custom_error_codes_enabled: boolean;
  custom_error_codes: number[];
  pool_mode: boolean;
  temp_unschedulable_enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface ProviderAccountCreate {
  provider_id: number;
  channel_id: number;
  name: string;
  account_type: AccountType;
  // WRITE-ONLY：GET 端点不返回此字段
  credentials: Record<string, unknown>;
  cap_concurrency?: number;
  priority?: number;
  model_allow_list?: string[];
  capability_flags?: string[];
}

export interface TempUnschedulableRule {
  error_code: number;
  keywords: string[];
  duration_minutes: number;
  description?: string;
}

export interface ProviderAccountUpdate {
  enabled?: boolean;
  priority?: number;
  cap_concurrency?: number;
  model_allow_list?: string[];
  capability_flags?: string[];
  custom_error_codes_enabled?: boolean;
  custom_error_codes?: number[];
  pool_mode?: boolean;
  temp_unschedulable_enabled?: boolean;
  temp_unschedulable_rules?: TempUnschedulableRule[];
}

export interface ProviderAccountList {
  items: ProviderAccount[];
  page: PageMeta;
}

// ---- Pool Groups ----

export type CapabilityDefault = 'exact_capability_only' | 'safe_equivalent_allowed';

export interface PoolGroup {
  id: number;
  tenant_id: number;
  name: string;
  routing_policy_version: string;
  top_k_default: number;
  capability_default: CapabilityDefault;
  allow_tenant_operator_force: boolean;
  allow_last_resort: boolean;
  allow_mid_stream_failover: boolean;
  sticky_wait_max_waiting: number;
  fallback_wait_max_waiting: number;
  sticky_wait_timeout_ms: number;
  fallback_wait_timeout_ms: number;
  forced_route_rate_limit_per_hour: number;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface PoolGroupCreate {
  name: string;
  routing_policy_version?: string;
  top_k_default?: number;
  capability_default?: CapabilityDefault;
  allow_tenant_operator_force?: boolean;
  allow_last_resort?: boolean;
  allow_mid_stream_failover?: boolean;
}

export interface PoolGroupUpdate {
  routing_policy_version?: string;
  top_k_default?: number;
  capability_default?: CapabilityDefault;
  allow_tenant_operator_force?: boolean;
  allow_last_resort?: boolean;
  allow_mid_stream_failover?: boolean;
  sticky_wait_max_waiting?: number;
  fallback_wait_max_waiting?: number;
  sticky_wait_timeout_ms?: number;
  fallback_wait_timeout_ms?: number;
  forced_route_rate_limit_per_hour?: number;
  enabled?: boolean;
}

export interface PoolGroupList {
  items: PoolGroup[];
  page: PageMeta;
}

// ---- Usage Records ----

export type EndClass =
  | 'stream_end_graceful'
  | 'stream_end_no_terminal_marker'
  | 'upstream_error_4xx'
  | 'upstream_error_5xx'
  | 'upstream_rate_limit'
  | 'upstream_auth_failure'
  | 'first_token_timeout'
  | 'inter_event_timeout'
  | 'total_stream_timeout'
  | 'client_disconnect'
  | 'event_size_exceeded'
  | 'orchestrator_cancelled'
  | 'usage_ambiguous'
  | 'unknown_termination'
  | 'non_streaming';

export type UsageSource = 'reported' | 'normalized' | 'inferred' | 'partial' | 'ambiguous';
export type TrustStatus = 'verified' | 'signed-only' | 'unverified' | 'missing' | 'mismatch';

export interface ProtocolLossEntry {
  feature: string;
  direction: 'client_to_canonical' | 'canonical_to_upstream' | 'upstream_to_canonical' | 'canonical_to_client';
  verdict: 'LOSSY' | 'UNSUPPORTED';
  note?: string;
}

export interface UsageRecord {
  id: number;
  tenant_id: number;
  claim_id: number;
  api_key_id: number;
  provider_account_id: number;
  provider?: string | null;
  attempt_seq?: number;
  tokens_input?: number;
  tokens_output?: number;
  cache_creation_tokens?: number;
  cache_read_tokens?: number;
  // numeric(20,8) 以 string 形式返回
  actual_cost: string;
  end_class: EndClass;
  usage_source: UsageSource;
  confidence_score?: number | null;
  pending_reconciliation: boolean;
  drain_outcome?: 'max_seconds' | 'max_bytes' | 'max_estimated_cost' | null;
  routing_reason?: Record<string, unknown>;
  protocol_loss?: ProtocolLossEntry[];
  requested_at?: string;
  settled_at: string;
  requested_model?: string;
  upstream_model?: string | null;
  request_id?: string;
  trust_status?: TrustStatus | null;
  stream: boolean;
}

export interface UsageRecordList {
  items: UsageRecord[];
  page: PageMeta;
}

// ---- Billing Claims ----

export type ClaimStatus = 'reserving' | 'committed' | 'aborted';

export interface BillingLedgerClaim {
  id: number;
  tenant_id: number;
  idempotency_key: string;
  api_key_id?: number;
  user_id?: number;
  endpoint_family?: string;
  requested_model?: string;
  provider_account_id?: number | null;
  attempt_seq?: number;
  predicted_cost?: string;
  actual_cost?: string | null;
  currency_code?: string;
  status: ClaimStatus;
  aborted_reason?: string | null;
  reserved_at: string;
  settled_at?: string | null;
}

export interface BillingLedgerClaimList {
  items: BillingLedgerClaim[];
  page: PageMeta;
}

// ---- Usage block (gateway 响应) ----

export interface UsageBlock {
  input_tokens?: number;
  output_tokens?: number;
  cache_creation_input_tokens?: number;
  cache_read_input_tokens?: number;
}

// ---- Chat (OpenAI) ----

export interface ChatMessage {
  role: 'system' | 'user' | 'assistant' | 'tool' | 'function';
  content: string | unknown[];
  tool_calls?: unknown[];
  tool_call_id?: string;
}

export interface ChatCompletionsRequest {
  model: string;
  messages: ChatMessage[];
  stream?: boolean;
  max_tokens?: number;
  temperature?: number;
  stream_options?: { include_usage?: boolean };
}

export interface ChatCompletionsResponse {
  id: string;
  object: 'chat.completion';
  model: string;
  choices: unknown[];
  usage?: UsageBlock;
}

// ---- Anthropic Messages ----

export interface AnthropicMessagesRequest {
  model: string;
  messages: unknown[];
  system?: string | unknown[];
  max_tokens: number;
  stream?: boolean;
}

export interface AnthropicMessagesResponse {
  id: string;
  type: string;
  model: string;
  content: unknown[];
  stop_reason?: string | null;
  usage?: UsageBlock;
}

// ---- Error response ----

export interface APIError {
  error: {
    code: string;
    message: string;
    request_id?: string;
    retry_after_seconds?: number;
    details?: Record<string, unknown>;
  };
}

// ---- Auth Credential Renew status ----

export type AuthCredentialRenewState =
  | 'active'
  | 'refreshing'
  | 'refreshing_with_grace'
  | 'expired'
  | 'temp_unschedulable'
  | 'needs_rotation'
  | 'revoked'
  | 'operator_attention'
  // OpenAPI declares this field as string, so the UI must tolerate future backend states.
  | (string & {});

export interface AuthCredentialRenewStatus {
  id: number;
  tenant_id: number;
  tenant_name: string;
  account_id: number;
  account_name: string;
  vendor: string;
  auth_mode: string;
  state: AuthCredentialRenewState;
  credential_version: number;
  access_expires_at?: string | null;
  refresh_before_at?: string | null;
  last_refresh_at?: string | null;
  last_refresh_outcome?: string | null;
  failure_class?: string | null;
  failure_count: number;
}

export interface AuthCredentialRenewStatusList {
  items: AuthCredentialRenewStatus[];
  next_cursor: string | null;
}

// ---- Mock: Mimicry profile（后端尚无此端点，全 mock） ----

export interface MimicryProfile {
  id: string;
  name: string;
  // 对应 backend MimicryPlan 字段
  enabled: boolean;
  system_rewrite: boolean;
  strip_system_cache_control: boolean;
  cache_breakpoints: boolean;
  use_ttl_ordering_for_step3: boolean;
  tool_names: boolean;
  metadata_user_id: string;
  apply_tools_tail_cache_bp: boolean;
  tools_tail_ttl: string;
}
