package activities

import (
	"context"
	"testing"
	"time"

	"cribug/internal/types"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello"},
		{"abc", 3, "abc"},
		{"abc", 4, "abc"},
		{"", 10, ""},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

func TestRequestApprovalNoDB(t *testing.T) {
	aa := NewApprovalActivities(nil) // nil DB
	result, err := aa.RequestApproval(context.Background(), RequestApprovalInput{
		SessionID:   "sess-1",
		WorkflowID:  "wf-1",
		RunID:       "run-1",
		Query:       "test query",
		Reason:      "test reason",
		RiskLevel:   "high",
		Mode:        string(types.RouteSandboxExecution),
		RequestedAt: time.Now(),
		ExpiresAt:   time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("RequestApproval failed: %v", err)
	}
	if result.ApprovalID == "" {
		t.Error("ApprovalID should not be empty")
	}
	if len(result.ApprovalID) < len("approval-") {
		t.Errorf("ApprovalID too short: %s", result.ApprovalID)
	}
}

func TestEvaluateApprovalPolicyCritical(t *testing.T) {
	aa := NewRouterActivities(nil)
	result, err := aa.EvaluateApprovalPolicy(context.Background(), EvaluateApprovalPolicyInput{
		ComplexityScore: 0.9,
		RiskLevel:       "critical",
		Mode:            string(types.RouteSandboxExecution),
		RequiresSandbox: true,
		RequireApproval: true,
	})
	if err != nil {
		t.Fatalf("EvaluateApprovalPolicy failed: %v", err)
	}
	if !result.Required {
		t.Error("critical risk should require approval")
	}
}

func TestEvaluateApprovalPolicyLowRisk(t *testing.T) {
	aa := NewRouterActivities(nil)
	result, err := aa.EvaluateApprovalPolicy(context.Background(), EvaluateApprovalPolicyInput{
		ComplexityScore: 0.1,
		RiskLevel:       "low",
		Mode:            string(types.RouteDirectAnswer),
		RequiresSandbox: false,
		RequireApproval: true,
	})
	if err != nil {
		t.Fatalf("EvaluateApprovalPolicy failed: %v", err)
	}
	if result.Required {
		t.Error("low risk direct answer should NOT require approval")
	}
}

func TestEvaluateApprovalPolicySandbox(t *testing.T) {
	aa := NewRouterActivities(nil)
	result, err := aa.EvaluateApprovalPolicy(context.Background(), EvaluateApprovalPolicyInput{
		ComplexityScore: 0.5,
		RiskLevel:       "medium",
		Mode:            string(types.RouteSandboxExecution),
		RequiresSandbox: true,
		RequireApproval: true,
	})
	if err != nil {
		t.Fatalf("EvaluateApprovalPolicy failed: %v", err)
	}
	if !result.Required {
		t.Error("sandbox execution should require approval")
	}
}

// TestEvaluateApprovalPolicyOverride verifies the test-only
// ROUTER_REQUIRE_APPROVAL=false kill switch. Even with critical
// risk, the gate must be disabled.
func TestEvaluateApprovalPolicyOverride(t *testing.T) {
	aa := NewRouterActivities(nil)
	result, err := aa.EvaluateApprovalPolicy(context.Background(), EvaluateApprovalPolicyInput{
		ComplexityScore: 0.95,
		RiskLevel:       "critical",
		Mode:            string(types.RouteSandboxExecution),
		RequiresSandbox: true,
		RequireApproval: false, // test/smoke override
	})
	if err != nil {
		t.Fatalf("EvaluateApprovalPolicy failed: %v", err)
	}
	if result.Required {
		t.Error("RequireApproval=false must override critical risk")
	}
}

func TestMarkApprovalTimeoutNoDB(t *testing.T) {
	aa := NewApprovalActivities(nil)
	result, err := aa.MarkApprovalTimeout(context.Background(), MarkApprovalTimeoutInput{
		ApprovalID:  "approval-test-123",
		WorkflowID:  "wf-test",
		RequestedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("MarkApprovalTimeout failed: %v", err)
	}
	if result.AuditID == "" {
		t.Error("AuditID should not be empty")
	}
}

func TestRecordApprovalResponseNoDB(t *testing.T) {
	aa := NewApprovalActivities(nil)
	result, err := aa.RecordApprovalResponse(context.Background(), RecordApprovalResponseInput{
		ApprovalID:  "approval-test-456",
		WorkflowID:  "wf-test",
		Approved:    true,
		Feedback:    "approved by test",
		ApprovedBy:  "test-user",
		RespondedAt: time.Now(),
		RequestedAt: time.Now().Add(-5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("RecordApprovalResponse failed: %v", err)
	}
	if result.AuditID == "" {
		t.Error("AuditID should not be empty")
	}
}

func TestEmitApprovalEventNoop(t *testing.T) {
	aa := NewApprovalActivities(nil)
	err := aa.EmitApprovalEvent(context.Background(), EmitApprovalEventInput{
		WorkflowID: "wf-test",
		ApprovalID: "approval-test",
		EventType:  "APPROVAL_REQUESTED",
	})
	if err != nil {
		t.Fatalf("EmitApprovalEvent should succeed: %v", err)
	}
}
