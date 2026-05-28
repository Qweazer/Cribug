package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── Test Helpers ────────────────────────────────────────────────────

type mockRepo struct {
	hooks []HookRegistration
	logs  []HookAuditLog
}

func (m *mockRepo) ListEnabledHooksByPoint(ctx context.Context, hookPoint HookPoint, tenantID string) ([]HookRegistration, error) {
	var result []HookRegistration
	for _, h := range m.hooks {
		if h.HookPoint == hookPoint && h.Enabled && h.TenantID == tenantID {
			result = append(result, h)
		}
	}
	return result, nil
}

func (m *mockRepo) InsertHookAuditLog(ctx context.Context, log HookAuditLog) error {
	m.logs = append(m.logs, log)
	return nil
}

func defaultRuntimeConfig() RuntimeConfig {
	return RuntimeConfig{
		HooksEnabled:          true,
		BlockingEnabled:       true,
		BlockingFailClosed:    false,
		HandlerTimeoutSec:     5,
		HandlerMaxResultBytes: 65536,
		AllowedInternalHn:     []string{"audit_logger", "log_only", "permission_check", "deny_tool_for_test", "fail_for_test", "timeout_for_test"},
		AllowedHTTPHosts:      []string{"localhost", "127.0.0.1"},
	}
}

func makeEvent(hookPoint HookPoint, sourceComponent string, payload map[string]interface{}) HookEvent {
	return HookEvent{
		EventID:         "evt-test-001",
		HookPoint:       hookPoint,
		AgentID:         "agent-1",
		WorkflowID:      "wf-1",
		TenantID:        "00000000-0000-0000-0000-000000000000",
		Timestamp:       time.Now().UTC(),
		SourceComponent: sourceComponent,
		Payload:         payload,
	}
}

func makeRegistration(name string, hookPoint HookPoint, handlerURL string, blocking bool, filter map[string]interface{}) HookRegistration {
	return HookRegistration{
		ID:         fmt.Sprintf("reg-%s", name),
		TenantID:   "00000000-0000-0000-0000-000000000000",
		Name:       name,
		HookPoint:  hookPoint,
		HandlerURL: handlerURL,
		Blocking:   blocking,
		Filter:     filter,
		Enabled:    true,
	}
}

// ── HookPoint Validation Tests ──────────────────────────────────────

func TestHookPointIsValid(t *testing.T) {
	if !HookPointBeforeToolCall.IsValid() {
		t.Error("before_tool_call should be valid")
	}
	if !HookPointOnError.IsValid() {
		t.Error("on_error should be valid")
	}
	if HookPoint("invalid_point").IsValid() {
		t.Error("invalid_point should not be valid")
	}
}

func TestHookPointAllowsBlocking(t *testing.T) {
	tests := []struct {
		point   HookPoint
		allowed bool
	}{
		{HookPointBeforeToolCall, true},
		{HookPointAfterToolCall, false},
		{HookPointBeforeLLMCall, true},
		{HookPointAfterLLMCall, false},
		{HookPointOnAgentStep, false},
		{HookPointOnWorkspaceAppend, false},
		{HookPointOnHandoff, false},
		{HookPointOnError, true},
	}
	for _, tt := range tests {
		if tt.point.AllowsBlocking() != tt.allowed {
			t.Errorf("%s: expected blocking allowed=%v, got %v", tt.point, tt.allowed, tt.point.AllowsBlocking())
		}
	}
}

// ── HookRegistration Validation Tests ───────────────────────────────

func TestValidateHookRegistration_Valid(t *testing.T) {
	reg := makeRegistration("test-hook", HookPointAfterToolCall, "internal:audit_logger", false, nil)
	err := ValidateHookRegistration(reg, []string{"audit_logger"}, []string{"localhost"})
	if err != nil {
		t.Errorf("expected valid registration, got error: %v", err)
	}
}

func TestValidateHookRegistration_BlockingOnNonBlockingPoint(t *testing.T) {
	reg := makeRegistration("test-hook", HookPointAfterToolCall, "internal:audit_logger", true, nil)
	err := ValidateHookRegistration(reg, []string{"audit_logger"}, []string{"localhost"})
	if err == nil {
		t.Error("expected error for blocking hook on non-blocking point")
	}
	if !strings.Contains(err.Error(), "blocking") {
		t.Errorf("error should mention blocking: %v", err)
	}
}

func TestValidateHookRegistration_UnknownInternalHandler(t *testing.T) {
	reg := makeRegistration("test-hook", HookPointAfterToolCall, "internal:unknown", false, nil)
	err := ValidateHookRegistration(reg, []string{"audit_logger"}, []string{"localhost"})
	if err == nil {
		t.Error("expected error for unknown internal handler")
	}
}

func TestValidateHookRegistration_HTTPHostNotAllowed(t *testing.T) {
	reg := makeRegistration("test-hook", HookPointAfterToolCall, "http://evil.com/hook", false, nil)
	err := ValidateHookRegistration(reg, []string{"audit_logger"}, []string{"localhost"})
	if err == nil {
		t.Error("expected error for disallowed HTTP host")
	}
}

func TestValidateHookRegistration_HTTPHostAllowed(t *testing.T) {
	reg := makeRegistration("test-hook", HookPointAfterToolCall, "http://localhost:9999/hook", false, nil)
	err := ValidateHookRegistration(reg, []string{"audit_logger"}, []string{"localhost"})
	if err != nil {
		t.Errorf("expected valid HTTP handler, got error: %v", err)
	}
}

func TestValidateHookRegistration_InvalidScheme(t *testing.T) {
	reg := makeRegistration("test-hook", HookPointAfterToolCall, "ftp://localhost/hook", false, nil)
	err := ValidateHookRegistration(reg, []string{"audit_logger"}, []string{"localhost"})
	if err == nil {
		t.Error("expected error for unsupported URL scheme")
	}
}

// ── Filter Tests ────────────────────────────────────────────────────

func TestFilter_EmptyFilterMatchesAll(t *testing.T) {
	f := NewHookFilter()
	event := makeEvent(HookPointBeforeToolCall, "mcp", map[string]interface{}{"tool_name": "test"})
	reg := makeRegistration("h", HookPointBeforeToolCall, "internal:audit_logger", false, nil)
	if !f.Match(event, reg) {
		t.Error("empty filter should match all events")
	}
}

func TestFilter_ToolNameMatch(t *testing.T) {
	f := NewHookFilter()
	event := makeEvent(HookPointBeforeToolCall, "mcp", map[string]interface{}{"tool_name": "echo"})
	reg := makeRegistration("h", HookPointBeforeToolCall, "internal:audit_logger", false, map[string]interface{}{"tool_name": "echo"})
	if !f.Match(event, reg) {
		t.Error("filter should match on tool_name")
	}
}

func TestFilter_ToolNameNoMatch(t *testing.T) {
	f := NewHookFilter()
	event := makeEvent(HookPointBeforeToolCall, "mcp", map[string]interface{}{"tool_name": "calculator"})
	reg := makeRegistration("h", HookPointBeforeToolCall, "internal:audit_logger", false, map[string]interface{}{"tool_name": "echo"})
	if f.Match(event, reg) {
		t.Error("filter should not match on different tool_name")
	}
}

func TestFilter_ToolTypesMatch(t *testing.T) {
	f := NewHookFilter()
	event := makeEvent(HookPointBeforeToolCall, "mcp", map[string]interface{}{"tool_type": "mcp"})
	reg := makeRegistration("h", HookPointBeforeToolCall, "internal:audit_logger", false, map[string]interface{}{
		"tool_type": "mcp",
	})
	if !f.Match(event, reg) {
		t.Error("filter should match tool_type string")
	}
}

func TestFilter_ToolTypesNoMatch(t *testing.T) {
	f := NewHookFilter()
	event := makeEvent(HookPointBeforeToolCall, "skill", map[string]interface{}{"tool_type": "skill"})
	reg := makeRegistration("h", HookPointBeforeToolCall, "internal:audit_logger", false, map[string]interface{}{
		"tool_type": "mcp",
	})
	if f.Match(event, reg) {
		t.Error("filter should not match different tool_type string")
	}
}

func TestFilter_MultipleKeysAND(t *testing.T) {
	f := NewHookFilter()
	event := makeEvent(HookPointBeforeToolCall, "mcp", map[string]interface{}{
		"tool_type": "mcp",
		"tool_name": "echo",
	})
	reg := makeRegistration("h", HookPointBeforeToolCall, "internal:audit_logger", false, map[string]interface{}{
		"tool_type": "mcp",
		"tool_name": "echo",
	})
	if !f.Match(event, reg) {
		t.Error("filter should match when all keys match")
	}
}

func TestFilter_MultipleKeysAND_Partial(t *testing.T) {
	f := NewHookFilter()
	event := makeEvent(HookPointBeforeToolCall, "mcp", map[string]interface{}{
		"tool_type": "mcp",
		"tool_name": "calculator",
	})
	reg := makeRegistration("h", HookPointBeforeToolCall, "internal:audit_logger", false, map[string]interface{}{
		"tool_type": "mcp",
		"tool_name": "echo",
	})
	if f.Match(event, reg) {
		t.Error("filter should NOT match when tool_name differs")
	}
}

// ── Recursion Guard Tests ───────────────────────────────────────────

func TestRecursionGuard_DepthExceeded(t *testing.T) {
	repo := &mockRepo{}
	cfg := defaultRuntimeConfig()
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointOnWorkspaceAppend, "workspace", nil)
	event.RecursionDepth = 2

	decision := runtime.EmitAndExecute(context.Background(), event)
	if !decision.CanContinue {
		t.Error("recursion depth exceeded should not block, just skip")
	}
	if len(decision.Results) != 0 {
		t.Error("recursion depth exceeded should return no results")
	}
}

func TestRecursionGuard_HookOriginOnWorkspaceAppend(t *testing.T) {
	repo := &mockRepo{}
	cfg := defaultRuntimeConfig()
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointOnWorkspaceAppend, "hook", nil)
	event.HookOrigin = true

	decision := runtime.EmitAndExecute(context.Background(), event)
	if !decision.CanContinue {
		t.Error("hook_origin should not block")
	}
	if len(decision.Results) != 0 {
		t.Error("hook_origin on workspace_append should return no results")
	}
}

// ── Non-Blocking Handler Tests ──────────────────────────────────────

func TestNonBlockingHandler_Success(t *testing.T) {
	repo := &mockRepo{}
	repo.hooks = []HookRegistration{
		makeRegistration("audit", HookPointAfterToolCall, "internal:audit_logger", false, nil),
	}
	cfg := defaultRuntimeConfig()
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointAfterToolCall, "mcp", map[string]interface{}{"tool_name": "echo"})
	decision := runtime.EmitAndExecute(context.Background(), event)

	if !decision.CanContinue {
		t.Error("non-blocking handler should not block")
	}
	if decision.Denied {
		t.Error("non-blocking handler should not deny")
	}
	if len(decision.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(decision.Results))
	}
	if !decision.Results[0].Success {
		t.Error("audit_logger should succeed")
	}
	if len(repo.logs) != 1 {
		t.Errorf("expected 1 audit log, got %d", len(repo.logs))
	}
}

func TestNonBlockingHandler_FailureDoesNotBlock(t *testing.T) {
	repo := &mockRepo{}
	repo.hooks = []HookRegistration{
		makeRegistration("failer", HookPointAfterToolCall, "internal:fail_for_test", false, nil),
	}
	cfg := defaultRuntimeConfig()
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointAfterToolCall, "mcp", nil)
	decision := runtime.EmitAndExecute(context.Background(), event)

	if !decision.CanContinue {
		t.Error("non-blocking handler failure should NOT block main flow")
	}
	if decision.Denied {
		t.Error("non-blocking failure should not result in denied")
	}
	if len(decision.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(decision.Results))
	}
	if decision.Results[0].Success {
		t.Error("fail_for_test should report failure")
	}
	if decision.Results[0].Warning == "" {
		t.Error("non-blocking failure should have warning")
	}
}

// ── Blocking Handler Tests ──────────────────────────────────────────

func TestBlockingHandler_DenyWhenEnabled(t *testing.T) {
	repo := &mockRepo{}
	repo.hooks = []HookRegistration{
		makeRegistration("deny", HookPointBeforeToolCall, "internal:deny_tool_for_test", true, nil),
	}
	cfg := defaultRuntimeConfig() // BlockingEnabled=true
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointBeforeToolCall, "mcp", nil)
	decision := runtime.EmitAndExecute(context.Background(), event)

	if decision.CanContinue {
		t.Error("blocking handler should deny when BlockingEnabled=true")
	}
	if !decision.Denied {
		t.Error("denied should be true")
	}
	if len(decision.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(decision.Results))
	}
	if !decision.Results[0].BlockingConfigured {
		t.Error("blocking_configured should be true")
	}
	if !decision.Results[0].BlockingEffective {
		t.Error("blocking_effective should be true")
	}
	if !decision.Results[0].DecisionEnforced {
		t.Error("decision_enforced should be true when blocking is on and handler denies")
	}
}

func TestBlockingHandler_SuppressedWhenBlockingDisabled(t *testing.T) {
	repo := &mockRepo{}
	repo.hooks = []HookRegistration{
		makeRegistration("deny", HookPointBeforeToolCall, "internal:deny_tool_for_test", true, nil),
	}
	cfg := defaultRuntimeConfig()
	cfg.BlockingEnabled = false // globally disabled
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointBeforeToolCall, "mcp", nil)
	decision := runtime.EmitAndExecute(context.Background(), event)

	if !decision.CanContinue {
		t.Error("main flow should continue when blocking is globally disabled")
	}
	if decision.Denied {
		t.Error("denied should be false when blocking disabled")
	}
	r := decision.Results[0]
	if !r.BlockingConfigured {
		t.Error("blocking_configured should still be true (registration intent)")
	}
	if r.BlockingEffective {
		t.Error("blocking_effective should be false when blocking disabled")
	}
	if r.DecisionEnforced {
		t.Error("decision_enforced should be false")
	}
	if r.SuppressedReason != SuppressedReasonBlockingDisabled {
		t.Errorf("suppressed_reason should be %s, got %s", SuppressedReasonBlockingDisabled, r.SuppressedReason)
	}
}

// ── HTTP Handler Tests ──────────────────────────────────────────────

func TestHTTPHandler_Success(t *testing.T) {
	// Create a test HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HandlerResponse{Continue: true})
	}))
	defer server.Close()

	repo := &mockRepo{}
	repo.hooks = []HookRegistration{
		makeRegistration("http-ok", HookPointAfterToolCall, server.URL, false, nil),
	}
	cfg := defaultRuntimeConfig()
	// Allow the test server's host
	host := strings.TrimPrefix(server.URL, "http://")
	if idx := strings.Index(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	cfg.AllowedHTTPHosts = []string{host}
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointAfterToolCall, "mcp", nil)
	decision := runtime.EmitAndExecute(context.Background(), event)

	if !decision.CanContinue {
		t.Error("HTTP handler should return continue=true")
	}
	if len(decision.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(decision.Results))
	}
	if !decision.Results[0].Success {
		t.Error("HTTP handler should succeed")
	}
}

func TestHTTPHandler_500DoesNotBlockNonBlocking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	repo := &mockRepo{}
	host := strings.TrimPrefix(server.URL, "http://")
	if idx := strings.Index(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	repo.hooks = []HookRegistration{
		makeRegistration("http-500", HookPointAfterToolCall, server.URL, false, nil),
	}
	cfg := defaultRuntimeConfig()
	cfg.AllowedHTTPHosts = []string{host}
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointAfterToolCall, "mcp", nil)
	decision := runtime.EmitAndExecute(context.Background(), event)

	if !decision.CanContinue {
		t.Error("non-blocking HTTP 500 should NOT block main flow")
	}
	if len(repo.logs) != 1 {
		t.Errorf("expected 1 audit log, got %d", len(repo.logs))
	}
}

func TestBlockingHTTPHandler_TimeoutFailOpen(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second) // longer than handler timeout
	}))
	defer server.Close()

	repo := &mockRepo{}
	host := strings.TrimPrefix(server.URL, "http://")
	if idx := strings.Index(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	repo.hooks = []HookRegistration{
		makeRegistration("http-timeout", HookPointBeforeToolCall, server.URL, true, nil),
	}
	cfg := defaultRuntimeConfig()
	cfg.AllowedHTTPHosts = []string{host}
	cfg.HandlerTimeoutSec = 1 // 1 second timeout
	cfg.BlockingFailClosed = false
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointBeforeToolCall, "mcp", nil)
	decision := runtime.EmitAndExecute(context.Background(), event)

	// Default: fail-open, blocking timeout should NOT block
	if !decision.CanContinue {
		t.Error("blocking HTTP timeout should fail-open by default")
	}
}

func TestBlockingHTTPHandler_TimeoutFailClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second)
	}))
	defer server.Close()

	repo := &mockRepo{}
	host := strings.TrimPrefix(server.URL, "http://")
	if idx := strings.Index(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	repo.hooks = []HookRegistration{
		makeRegistration("http-timeout-fc", HookPointBeforeToolCall, server.URL, true, nil),
	}
	cfg := defaultRuntimeConfig()
	cfg.AllowedHTTPHosts = []string{host}
	cfg.HandlerTimeoutSec = 1
	cfg.BlockingFailClosed = true
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointBeforeToolCall, "mcp", nil)
	decision := runtime.EmitAndExecute(context.Background(), event)

	if decision.CanContinue {
		t.Error("blocking HTTP timeout should fail-closed when BlockingFailClosed=true")
	}
}

// ── HooksDisabled Tests ─────────────────────────────────────────────

func TestHooksDisabled_FastReturn(t *testing.T) {
	repo := &mockRepo{}
	repo.hooks = []HookRegistration{
		makeRegistration("audit", HookPointAfterToolCall, "internal:audit_logger", false, nil),
	}
	cfg := defaultRuntimeConfig()
	cfg.HooksEnabled = false
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointAfterToolCall, "mcp", nil)
	decision := runtime.EmitAndExecute(context.Background(), event)

	if !decision.CanContinue {
		t.Error("should always continue when hooks disabled")
	}
	if len(decision.Results) != 0 {
		t.Error("should return empty results when hooks disabled")
	}
}

// ── Audit Log Tests ─────────────────────────────────────────────────

func TestAuditLog_ContainsBlockingFields(t *testing.T) {
	repo := &mockRepo{}
	repo.hooks = []HookRegistration{
		makeRegistration("deny", HookPointBeforeToolCall, "internal:deny_tool_for_test", true, nil),
	}
	cfg := defaultRuntimeConfig()
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointBeforeToolCall, "mcp", nil)
	runtime.EmitAndExecute(context.Background(), event)

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	log := repo.logs[0]
	if !log.BlockingConfigured {
		t.Error("audit log should have blocking_configured=true")
	}
	if !log.BlockingEffective {
		t.Error("audit log should have blocking_effective=true")
	}
	if !log.DecisionEnforced {
		t.Error("audit log should have decision_enforced=true")
	}
	if log.SuppressedReason != "" {
		t.Error("audit log should have empty suppressed_reason when not suppressed")
	}
}

func TestAuditLog_SuppressedReasonWhenDisabled(t *testing.T) {
	repo := &mockRepo{}
	repo.hooks = []HookRegistration{
		makeRegistration("deny", HookPointBeforeToolCall, "internal:deny_tool_for_test", true, nil),
	}
	cfg := defaultRuntimeConfig()
	cfg.BlockingEnabled = false
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointBeforeToolCall, "mcp", nil)
	runtime.EmitAndExecute(context.Background(), event)

	log := repo.logs[0]
	if log.SuppressedReason != SuppressedReasonBlockingDisabled {
		t.Errorf("suppressed_reason should be %s, got %s", SuppressedReasonBlockingDisabled, log.SuppressedReason)
	}
}

// ── Payload Hash Tests ──────────────────────────────────────────────

func TestAuditLog_HasPayloadHash(t *testing.T) {
	repo := &mockRepo{}
	repo.hooks = []HookRegistration{
		makeRegistration("audit", HookPointAfterToolCall, "internal:audit_logger", false, nil),
	}
	cfg := defaultRuntimeConfig()
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointAfterToolCall, "mcp", map[string]interface{}{"tool_name": "echo"})
	runtime.EmitAndExecute(context.Background(), event)

	log := repo.logs[0]
	if log.PayloadHash == "" {
		t.Error("audit log should have non-empty payload_hash")
	}
}

// ── Filter + No Matching Hooks ─────────────────────────────────────

func TestNoMatchingHooks_CanContinue(t *testing.T) {
	repo := &mockRepo{}
	// No hooks registered
	cfg := defaultRuntimeConfig()
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointBeforeToolCall, "mcp", nil)
	decision := runtime.EmitAndExecute(context.Background(), event)

	if !decision.CanContinue {
		t.Error("should continue with no matching hooks")
	}
}

func TestFilterNoMatch_CanContinue(t *testing.T) {
	repo := &mockRepo{}
	repo.hooks = []HookRegistration{
		makeRegistration("mcp-only", HookPointBeforeToolCall, "internal:audit_logger", false, map[string]interface{}{
			"tool_types": []interface{}{"mcp"},
		}),
	}
	cfg := defaultRuntimeConfig()
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointBeforeToolCall, "sandbox", map[string]interface{}{"tool_type": "sandbox"})
	decision := runtime.EmitAndExecute(context.Background(), event)

	if !decision.CanContinue {
		t.Error("should continue when filter does not match")
	}
	if len(decision.Results) != 0 {
		t.Error("should have no results when no hooks match filter")
	}
}

// ── Permission Check Handler Tests ──────────────────────────────────

func TestPermissionCheck_Allowed(t *testing.T) {
	repo := &mockRepo{}
	repo.hooks = []HookRegistration{
		{
			ID: "reg-pc", TenantID: "00000000-0000-0000-0000-000000000000",
			Name: "perm-check", HookPoint: HookPointBeforeToolCall,
			HandlerURL: "internal:permission_check", Blocking: true, Enabled: true,
			Filter: map[string]interface{}{
				"allowed_tools": []interface{}{"echo", "get_time"},
			},
		},
	}
	cfg := defaultRuntimeConfig()
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointBeforeToolCall, "mcp", map[string]interface{}{
		"tool_name": "echo",
	})
	decision := runtime.EmitAndExecute(context.Background(), event)

	if !decision.CanContinue {
		t.Error("permission_check should allow echo")
	}
	if decision.Denied {
		t.Error("permission_check should not deny allowed tool")
	}
}

func TestPermissionCheck_Denied(t *testing.T) {
	repo := &mockRepo{}
	repo.hooks = []HookRegistration{
		{
			ID: "reg-pc", TenantID: "00000000-0000-0000-0000-000000000000",
			Name: "perm-check", HookPoint: HookPointBeforeToolCall,
			HandlerURL: "internal:permission_check", Blocking: true, Enabled: true,
			Filter: map[string]interface{}{
				"allowed_tools": []interface{}{"echo", "get_time"},
			},
		},
	}
	cfg := defaultRuntimeConfig()
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointBeforeToolCall, "mcp", map[string]interface{}{
		"tool_name": "dangerous_tool",
	})
	decision := runtime.EmitAndExecute(context.Background(), event)

	if decision.CanContinue {
		t.Error("permission_check should deny dangerous_tool")
	}
	if decision.RejectCode != RejectCodePermissionDenied {
		t.Errorf("reject_code should be %s, got %s", RejectCodePermissionDenied, decision.RejectCode)
	}
}

// ── HookDecision Construction Tests ─────────────────────────────────

func TestHookDecision_WarningsCollected(t *testing.T) {
	repo := &mockRepo{}
	repo.hooks = []HookRegistration{
		makeRegistration("fail1", HookPointAfterToolCall, "internal:fail_for_test", false, nil),
		makeRegistration("fail2", HookPointAfterToolCall, "internal:fail_for_test", false, nil),
	}
	cfg := defaultRuntimeConfig()
	runtime := NewHookRuntime(repo, cfg)

	event := makeEvent(HookPointAfterToolCall, "mcp", nil)
	decision := runtime.EmitAndExecute(context.Background(), event)

	if len(decision.Warnings) != 2 {
		t.Errorf("expected 2 warnings, got %d: %v", len(decision.Warnings), decision.Warnings)
	}
	if decision.Denied {
		t.Error("non-blocking failures should not deny")
	}
}
