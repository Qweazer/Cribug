package api

import (
	"testing"

	"cribug/internal/activities"
	"cribug/internal/skillclient"
)

// TestSkillsHandlerCompile verifies the SkillsHandler can be created
// and its methods are reachable (compile check).
func TestSkillsHandlerCompile(t *testing.T) {
	client := skillclient.NewClient("http://localhost:9999")
	acts := &activities.SkillActivities{}
	h := NewSkillsHandler(client, acts)
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
	if h.client == nil {
		t.Error("expected non-nil client")
	}
	if h.activities == nil {
		t.Error("expected non-nil activities")
	}
}
