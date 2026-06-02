package types

import "testing"

func TestValidateModifiedAction(t *testing.T) {
	tests := []struct {
		name    string
		action  map[string]interface{}
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil action",
			action:  nil,
			wantErr: true,
			errMsg:  "at least one",
		},
		{
			name:    "empty action",
			action:  map[string]interface{}{},
			wantErr: true,
			errMsg:  "at least one",
		},
		{
			name:    "valid mode only",
			action:  map[string]interface{}{"mode": "direct_answer"},
			wantErr: false,
		},
		{
			name:    "valid addons only",
			action:  map[string]interface{}{"addons": []interface{}{"rag"}},
			wantErr: false,
		},
		{
			name:    "valid mode + addons",
			action:  map[string]interface{}{"mode": "direct_answer", "addons": []interface{}{"rag"}},
			wantErr: false,
		},
		{
			name:    "non-whitelisted field",
			action:  map[string]interface{}{"mode": "direct_answer", "risk_level": "low"},
			wantErr: true,
			errMsg:  "not in the modify whitelist",
		},
		{
			name:    "completely invalid field",
			action:  map[string]interface{}{"bypass_approval": true},
			wantErr: true,
			errMsg:  "not in the modify whitelist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateModifiedAction(tt.action)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errMsg)
				} else if tt.errMsg != "" {
					if !contains(err.Error(), tt.errMsg) {
						t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
					}
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestApprovalSignalName(t *testing.T) {
	name := ApprovalSignalName("approval-abc123")
	expected := "human-approval-approval-abc123"
	if name != expected {
		t.Errorf("expected %q, got %q", expected, name)
	}
}

func TestApprovalStatusConstants(t *testing.T) {
	// Verify all expected statuses are distinct
	statuses := map[string]bool{}
	for _, s := range []string{
		ApprovalStatusPending, ApprovalStatusApproved, ApprovalStatusRejected,
		ApprovalStatusModified, ApprovalStatusTimeout, ApprovalStatusCancelled,
	} {
		if statuses[s] {
			t.Errorf("duplicate status constant: %s", s)
		}
		statuses[s] = true
	}
}

func TestIsHighRiskMode(t *testing.T) {
	tests := []struct {
		mode     RoutingMode
		highRisk bool
	}{
		{RouteDirectAnswer, false},
		{RouteRAGAnswer, false},
		{RouteReActTool, false},
		{RouteSandboxExecution, true},
		{RouteDAGWorkflow, true},
		{RouteSwarmWorkflow, true},
		{RouteReflection, false},
		{RouteTreeOfThoughts, false},
		{RouteDebate, false},
		{RouteResearchV1, false},
		{RouteResearchV2, true},
		{RouteModeDisabled, false},
	}
	for _, tt := range tests {
		if got := IsHighRiskMode(tt.mode); got != tt.highRisk {
			t.Errorf("IsHighRiskMode(%s) = %v, want %v", tt.mode, got, tt.highRisk)
		}
	}
}

func TestRouterStatusIncludesApprovalTimeout(t *testing.T) {
	if RoutedStatusApprovalTimeout != "approval_timeout" {
		t.Errorf("RoutedStatusApprovalTimeout = %q, want %q", RoutedStatusApprovalTimeout, "approval_timeout")
	}
	// Verify it's distinct from generic timeout
	if RoutedStatusApprovalTimeout == RoutedStatusTimeout {
		t.Error("RoutedStatusApprovalTimeout must not equal RoutedStatusTimeout")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
