package hooks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ── Hook Runtime Config ────────────────────────────────────────────

type RuntimeConfig struct {
	HooksEnabled          bool
	BlockingEnabled       bool
	BlockingFailClosed    bool
	HandlerTimeoutSec     int
	HandlerMaxResultBytes int
	AllowedInternalHn     []string
	AllowedHTTPHosts      []string
}

// ── Hook Repository Interface ──────────────────────────────────────

// HookRepository abstracts DB operations for the hook runtime.
type HookRepository interface {
	ListEnabledHooksByPoint(ctx context.Context, hookPoint HookPoint, tenantID string) ([]HookRegistration, error)
	InsertHookAuditLog(ctx context.Context, log HookAuditLog) error
}

// ── Hook Runtime ───────────────────────────────────────────────────

type HookRuntime struct {
	repo   HookRepository
	config RuntimeConfig
	client *http.Client
}

func NewHookRuntime(repo HookRepository, config RuntimeConfig) *HookRuntime {
	timeout := config.HandlerTimeoutSec
	if timeout <= 0 {
		timeout = 5
	}
	return &HookRuntime{
		repo:   repo,
		config: config,
		client: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}
}

// EmitAndExecute is the main entry point. It finds matching hooks, executes them,
// writes audit logs, and returns a HookDecision for the caller.
func (r *HookRuntime) EmitAndExecute(ctx context.Context, event HookEvent) HookDecision {
	// Recursion guard
	if event.RecursionDepth >= 2 {
		return HookDecision{
			EventID:     event.EventID,
			CanContinue: true,
			Results:     []HookResult{},
		}
	}
	if event.HookOrigin && event.HookPoint == HookPointOnWorkspaceAppend {
		return HookDecision{
			EventID:     event.EventID,
			CanContinue: true,
			Results:     []HookResult{},
		}
	}

	if !r.config.HooksEnabled {
		return HookDecision{
			EventID:     event.EventID,
			CanContinue: true,
			Results:     []HookResult{},
		}
	}

	tenantID := event.TenantID
	if tenantID == "" {
		tenantID = "00000000-0000-0000-0000-000000000000"
	}

	registrations, err := r.repo.ListEnabledHooksByPoint(ctx, event.HookPoint, tenantID)
	if err != nil {
		return HookDecision{
			EventID:     event.EventID,
			CanContinue: true,
			Warnings:    []string{fmt.Sprintf("list hooks failed: %v", err)},
		}
	}

	if len(registrations) == 0 {
		return HookDecision{
			EventID:     event.EventID,
			CanContinue: true,
			Results:     []HookResult{},
		}
	}

	filter := NewHookFilter()
	var results []HookResult
	hasBlocking := false
	hasEffectiveBlocking := false
	var deniedBy *HookResult

	for _, reg := range registrations {
		if !filter.Match(event, reg) {
			continue
		}

		result := r.executeHandler(ctx, event, reg)
		r.writeAudit(ctx, event, reg, result)
		results = append(results, result)

		if result.BlockingConfigured {
			hasBlocking = true
		}
		if result.BlockingEffective {
			hasEffectiveBlocking = true
		}
		if result.DecisionEnforced && deniedBy == nil {
			deniedBy = &result
		}
	}

	decision := HookDecision{
		EventID:              event.EventID,
		Results:              results,
		HasBlockingHooks:     hasBlocking,
		HasEffectiveBlocking: hasEffectiveBlocking,
		CanContinue:          deniedBy == nil,
		Denied:               deniedBy != nil,
	}

	if deniedBy != nil {
		decision.RejectCode = deniedBy.RejectCode
		decision.RejectReason = deniedBy.RejectReason
		decision.DeniedByHandler = deniedBy.HandlerName
	}

	// Collect warnings from non-blocking failures
	for _, r := range results {
		if r.Warning != "" {
			decision.Warnings = append(decision.Warnings, r.Warning)
		}
	}

	if results == nil {
		decision.Results = []HookResult{}
	}

	return decision
}

// executeHandler runs a single handler (internal or HTTP) and returns the HookResult.
func (r *HookRuntime) executeHandler(ctx context.Context, event HookEvent, reg HookRegistration) HookResult {
	start := time.Now()
	blockingEffective := reg.Blocking && r.config.BlockingEnabled
	handlerType := HandlerTypeInternal

	result := HookResult{
		EventID:            event.EventID,
		HookPoint:          event.HookPoint,
		HandlerName:        reg.Name,
		BlockingConfigured: reg.Blocking,
		BlockingEffective:  blockingEffective,
		ContinueDecision:   true,
	}

	if IsInternalHandler(reg.HandlerURL) {
		handlerType = HandlerTypeInternal
		r.executeInternal(ctx, event, reg, &result)
	} else if IsHTTPHandler(reg.HandlerURL) {
		handlerType = HandlerTypeHTTP
		r.executeHTTP(ctx, event, reg, &result)
	} else {
		result.Success = false
		result.Error = fmt.Sprintf("unsupported handler_url scheme: %s", reg.HandlerURL)
		result.Warning = result.Error
	}

	result.DurationMs = time.Since(start).Milliseconds()
	_ = handlerType // used in audit log
	return result
}

// executeInternal runs an internal handler.
func (r *HookRuntime) executeInternal(ctx context.Context, event HookEvent, reg HookRegistration, result *HookResult) {
	handlerName := InternalHandlerName(reg.HandlerURL)

	switch handlerName {
	case "audit_logger":
		// Always succeeds — detailed audit is in the audit log
		result.Success = true
		result.ContinueDecision = true

	case "log_only":
		result.Success = true
		result.ContinueDecision = true

	case "permission_check":
		r.runPermissionCheck(event, reg, result)

	case "deny_tool_for_test":
		result.Success = true
		result.ContinueDecision = false
		result.RejectCode = RejectCodePolicyDenied
		result.RejectReason = "test deny: tool blocked by deny_tool_for_test handler"
		r.applyBlockingDecision(result)

	case "fail_for_test":
		result.Success = false
		result.Error = "test failure: handler failed intentionally"
		result.Warning = result.Error

	case "timeout_for_test":
		time.Sleep(30 * time.Second)
		result.Success = false
		result.Error = "test timeout: handler timed out"
		result.Warning = result.Error
		r.applyBlockingFailure(result)

	default:
		result.Success = false
		result.Error = fmt.Sprintf("unknown internal handler: %s", handlerName)
		result.Warning = result.Error
	}
}

// ── Permission Check Handler ───────────────────────────────────────

func (r *HookRuntime) runPermissionCheck(event HookEvent, reg HookRegistration, result *HookResult) {
	// Get allowlist from filter configuration
	allowlist := extractStringSlice(reg.Filter, "allowed_tools")

	// Get tool_name from event payload
	toolName, _ := event.Payload["tool_name"].(string)

	if len(allowlist) > 0 {
		allowed := false
		for _, a := range allowlist {
			if a == toolName {
				allowed = true
				break
			}
		}
		if !allowed {
			result.Success = true
			result.ContinueDecision = false
			result.RejectCode = RejectCodePermissionDenied
			result.RejectReason = fmt.Sprintf("tool '%s' is not in the allowed list", toolName)
			r.applyBlockingDecision(result)
			return
		}
	}

	result.Success = true
	result.ContinueDecision = true
}

// ── HTTP Handler Execution ─────────────────────────────────────────

func (r *HookRuntime) executeHTTP(ctx context.Context, event HookEvent, reg HookRegistration, result *HookResult) {
	body, err := json.Marshal(event)
	if err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("marshal event: %v", err)
		result.Warning = result.Error
		r.applyBlockingFailure(result)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reg.HandlerURL, bytes.NewReader(body))
	if err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("create request: %v", err)
		result.Warning = result.Error
		r.applyBlockingFailure(result)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("HTTP call failed: %v", err)
		result.Warning = result.Error
		r.applyBlockingFailure(result)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Success = false
		result.Error = fmt.Sprintf("HTTP %d from handler", resp.StatusCode)
		result.Warning = result.Error
		r.applyBlockingFailure(result)
		return
	}

	// Limit response size
	limitedReader := io.LimitReader(resp.Body, int64(r.config.HandlerMaxResultBytes))
	var buf bytes.Buffer
	_, err = io.Copy(&buf, limitedReader)
	if err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("read response: %v", err)
		result.Warning = result.Error
		r.applyBlockingFailure(result)
		return
	}

	if buf.Len() >= r.config.HandlerMaxResultBytes {
		result.Success = false
		result.Error = "handler response exceeded max size"
		result.Warning = result.Error
		r.applyBlockingFailure(result)
		return
	}

	var handlerResp HandlerResponse
	if err := json.Unmarshal(buf.Bytes(), &handlerResp); err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("parse response JSON: %v", err)
		result.Warning = result.Error
		r.applyBlockingFailure(result)
		return
	}

	result.Success = true
	result.ContinueDecision = handlerResp.Continue

	if !handlerResp.Continue {
		result.RejectCode = handlerResp.RejectCode
		result.RejectReason = handlerResp.RejectReason
		if result.RejectCode == "" {
			result.RejectCode = RejectCodePolicyDenied
		}
		r.applyBlockingDecision(result)
	}
}

// ── Blocking Decision Helpers ──────────────────────────────────────

// applyBlockingDecision sets the decision_enforced field based on blocking semantics.
func (r *HookRuntime) applyBlockingDecision(result *HookResult) {
	if !result.BlockingConfigured {
		// Non-blocking handler returned continue=false — suppress it
		result.ContinueDecision = true
		result.SuppressedReason = SuppressedReasonBlockingDisabled
		return
	}

	if !r.config.BlockingEnabled {
		// Global blocking disabled — suppress the block
		result.BlockingEffective = false
		result.ContinueDecision = true
		result.SuppressedReason = SuppressedReasonBlockingDisabled
		return
	}

	// Blocking is effective and handler denied — enforce it
	result.BlockingEffective = true
	result.DecisionEnforced = true
}

// applyBlockingFailure handles a blocking handler that errored/timed out.
func (r *HookRuntime) applyBlockingFailure(result *HookResult) {
	if !result.BlockingConfigured {
		// Non-blocking failure: no impact on main flow
		return
	}

	if !r.config.BlockingEnabled {
		result.BlockingEffective = false
		result.SuppressedReason = SuppressedReasonBlockingDisabled
		return
	}

	result.BlockingEffective = true

	if r.config.BlockingFailClosed {
		// Fail-closed: error blocks the main flow
		result.DecisionEnforced = true
		result.ContinueDecision = false
		result.RejectCode = RejectCodeHandlerError
		if result.RejectReason == "" {
			result.RejectReason = result.Error
		}
	} else {
		// Fail-open: error is recorded but main flow continues
		result.ContinueDecision = true
	}
}

// ── Audit ──────────────────────────────────────────────────────────

func (r *HookRuntime) writeAudit(ctx context.Context, event HookEvent, reg HookRegistration, result HookResult) {
	handlerType := HandlerTypeInternal
	if IsHTTPHandler(reg.HandlerURL) {
		handlerType = HandlerTypeHTTP
	}

	// Compute payload hash
	payloadHash := ""
	if event.Payload != nil {
		b, _ := json.Marshal(event.Payload)
		h := sha256.Sum256(b)
		payloadHash = fmt.Sprintf("%x", h)
	}

	auditLog := HookAuditLog{
		ID:                 uuid.New().String(),
		EventID:            event.EventID,
		HookPoint:          event.HookPoint,
		HandlerName:        reg.Name,
		TenantID:           event.TenantID,
		WorkflowID:         event.WorkflowID,
		AgentID:            event.AgentID,
		CorrelationID:      event.CorrelationID,
		HandlerType:        handlerType,
		BlockingConfigured: result.BlockingConfigured,
		BlockingEffective:  result.BlockingEffective,
		DecisionEnforced:   result.DecisionEnforced,
		SuppressedReason:   result.SuppressedReason,
		ContinueDecision:   result.ContinueDecision,
		RejectCode:         result.RejectCode,
		RejectReason:       result.RejectReason,
		PayloadHash:        payloadHash,
		Success:            result.Success,
		DurationMs:         result.DurationMs,
		Error:              result.Error,
		Warning:            result.Warning,
		CreatedAt:          time.Now().UTC(),
	}

	if r.repo != nil {
		_ = r.repo.InsertHookAuditLog(ctx, auditLog) // best-effort, don't fail on audit
	}
}

// ── Helpers ────────────────────────────────────────────────────────

func extractStringSlice(filter map[string]interface{}, key string) []string {
	if filter == nil {
		return nil
	}
	raw, ok := filter[key]
	if !ok {
		return nil
	}
	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

