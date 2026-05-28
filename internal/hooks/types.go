package hooks

import "time"

// ── HookPoint Enum ─────────────────────────────────────────────────

type HookPoint string

const (
	HookPointBeforeToolCall    HookPoint = "before_tool_call"
	HookPointAfterToolCall     HookPoint = "after_tool_call"
	HookPointBeforeLLMCall     HookPoint = "before_llm_call"
	HookPointAfterLLMCall      HookPoint = "after_llm_call"
	HookPointOnAgentStep       HookPoint = "on_agent_step"
	HookPointOnWorkspaceAppend HookPoint = "on_workspace_append"
	HookPointOnHandoff         HookPoint = "on_handoff"
	HookPointOnError           HookPoint = "on_error"
)

var validHookPoints = map[HookPoint]bool{
	HookPointBeforeToolCall:    true,
	HookPointAfterToolCall:     true,
	HookPointBeforeLLMCall:     true,
	HookPointAfterLLMCall:      true,
	HookPointOnAgentStep:       true,
	HookPointOnWorkspaceAppend: true,
	HookPointOnHandoff:         true,
	HookPointOnError:           true,
}

var blockingAllowed = map[HookPoint]bool{
	HookPointBeforeToolCall: true,
	HookPointBeforeLLMCall:  true,
	HookPointOnError:        true,
}

func (hp HookPoint) IsValid() bool {
	return validHookPoints[hp]
}

func (hp HookPoint) AllowsBlocking() bool {
	return blockingAllowed[hp]
}

func (hp HookPoint) String() string {
	return string(hp)
}

// AllHookPoints returns all valid hook points.
func AllHookPoints() []HookPoint {
	return []HookPoint{
		HookPointBeforeToolCall,
		HookPointAfterToolCall,
		HookPointBeforeLLMCall,
		HookPointAfterLLMCall,
		HookPointOnAgentStep,
		HookPointOnWorkspaceAppend,
		HookPointOnHandoff,
		HookPointOnError,
	}
}

// ── Reject Codes ───────────────────────────────────────────────────

const (
	RejectCodePermissionDenied = "permission_denied"
	RejectCodePolicyDenied     = "policy_denied"
	RejectCodeHandlerError     = "handler_error"
	RejectCodeTimeout          = "hook_timeout"
)

// ── Handler Types ──────────────────────────────────────────────────

const (
	HandlerTypeInternal = "internal"
	HandlerTypeHTTP     = "http"
)

// ── Suppressed Reasons ─────────────────────────────────────────────

const (
	SuppressedReasonBlockingDisabled = "blocking_disabled_by_config"
	SuppressedReasonHookOrigin       = "hook_origin_recursion_guard"
	SuppressedReasonRecursionDepth   = "recursion_depth_exceeded"
	SuppressedReasonFilterNoMatch    = "filter_no_match"
)

// ── HookEvent ──────────────────────────────────────────────────────

type HookEvent struct {
	EventID         string                 `json:"event_id"`
	HookPoint       HookPoint              `json:"hook_point"`
	AgentID         string                 `json:"agent_id"`
	WorkflowID      string                 `json:"workflow_id"`
	TenantID        string                 `json:"tenant_id,omitempty"`
	CorrelationID   string                 `json:"correlation_id,omitempty"`
	Timestamp       time.Time              `json:"timestamp"`
	SourceComponent string                 `json:"source_component"` // "mcp", "sandbox", "skill", "llm", "workspace", "handoff", "react"
	HookOrigin      bool                   `json:"hook_origin"`      // true if emitted by a hook handler itself
	RecursionDepth  int                    `json:"recursion_depth"`
	Payload         map[string]interface{} `json:"payload"`
}

// ── HookResult (per-handler, split-field semantics) ─────────────────

type HookResult struct {
	EventID            string   `json:"event_id"`
	HookPoint          HookPoint `json:"hook_point"`
	HandlerName        string `json:"handler_name"`
	Success            bool   `json:"success"`
	BlockingConfigured bool   `json:"blocking_configured"`
	BlockingEffective  bool   `json:"blocking_effective"`
	DecisionEnforced   bool   `json:"decision_enforced"`
	SuppressedReason   string `json:"suppressed_reason,omitempty"`
	ContinueDecision   bool   `json:"continue_decision"`
	RejectCode         string `json:"reject_code,omitempty"`
	RejectReason       string `json:"reject_reason,omitempty"`
	DurationMs         int64  `json:"duration_ms"`
	Error              string `json:"error,omitempty"`
	Warning            string `json:"warning,omitempty"`
}

// ── HookDecision (aggregated result returned to Workflow) ──────────

type HookDecision struct {
	EventID              string       `json:"event_id"`
	Results              []HookResult `json:"results"`
	HasBlockingHooks     bool         `json:"has_blocking_hooks"`
	HasEffectiveBlocking bool         `json:"has_effective_blocking"`
	Denied               bool         `json:"denied"`
	CanContinue          bool         `json:"can_continue"`
	RejectCode           string       `json:"reject_code,omitempty"`
	RejectReason         string       `json:"reject_reason,omitempty"`
	DeniedByHandler      string       `json:"denied_by_handler,omitempty"`
	Warnings             []string     `json:"warnings,omitempty"`
}

// ── HookRegistration ───────────────────────────────────────────────

type HookRegistration struct {
	ID         string                 `json:"id"`
	TenantID   string                 `json:"tenant_id"`
	Name       string                 `json:"name"`
	HookPoint  HookPoint              `json:"hook_point"`
	HandlerURL string                 `json:"handler_url"`
	Blocking   bool                   `json:"blocking"`
	Filter     map[string]interface{} `json:"filter,omitempty"`
	Enabled    bool                   `json:"enabled"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
}

// ── HookAuditLog ───────────────────────────────────────────────────

type HookAuditLog struct {
	ID                 string    `json:"id"`
	EventID            string    `json:"event_id"`
	HookPoint          HookPoint `json:"hook_point"`
	HandlerName        string    `json:"handler_name"`
	TenantID           string    `json:"tenant_id"`
	WorkflowID         string    `json:"workflow_id"`
	AgentID            string    `json:"agent_id"`
	CorrelationID      string    `json:"correlation_id"`
	HandlerType        string    `json:"handler_type"`
	BlockingConfigured bool      `json:"blocking_configured"`
	BlockingEffective  bool      `json:"blocking_effective"`
	DecisionEnforced   bool      `json:"decision_enforced"`
	SuppressedReason   string    `json:"suppressed_reason"`
	ContinueDecision   bool      `json:"continue_decision"`
	RejectCode         string    `json:"reject_code"`
	RejectReason       string    `json:"reject_reason"`
	PayloadHash        string    `json:"payload_hash"`
	Success            bool      `json:"success"`
	DurationMs         int64     `json:"duration_ms"`
	Error              string    `json:"error"`
	Warning            string    `json:"warning"`
	CreatedAt          time.Time `json:"created_at"`
}

// ── Handler Response (parsed from HTTP handler JSON response) ──────

type HandlerResponse struct {
	Continue     bool   `json:"continue"`
	RejectCode   string `json:"reject_code,omitempty"`
	RejectReason string `json:"reject_reason,omitempty"`
}

// ── EmitHookEvent Input/Output (Temporal Activity) ──────────────────

type EmitHookEventInput struct {
	HookPoint       HookPoint              `json:"hook_point"`
	AgentID         string                 `json:"agent_id"`
	WorkflowID      string                 `json:"workflow_id"`
	TenantID        string                 `json:"tenant_id"`
	CorrelationID   string                 `json:"correlation_id,omitempty"`
	SourceComponent string                 `json:"source_component"`
	HookOrigin      bool                   `json:"hook_origin"`
	RecursionDepth  int                    `json:"recursion_depth"`
	Payload         map[string]interface{} `json:"payload"`
}

type EmitHookEventResult struct {
	Decision HookDecision `json:"decision"`
}
