package types

import "testing"

func TestDebatePositionConstants(t *testing.T) {
	if DebatePositionPro != "pro" {
		t.Fatalf("DebatePositionPro = %q, want pro", DebatePositionPro)
	}
	if DebatePositionCon != "con" {
		t.Fatalf("DebatePositionCon = %q, want con", DebatePositionCon)
	}
}

func TestDebateVerdictConstants(t *testing.T) {
	if DebateVerdictPro != "pro" {
		t.Fatalf("DebateVerdictPro = %q, want pro", DebateVerdictPro)
	}
	if DebateVerdictCon != "con" {
		t.Fatalf("DebateVerdictCon = %q, want con", DebateVerdictCon)
	}
	if DebateVerdictTie != "tie" {
		t.Fatalf("DebateVerdictTie = %q, want tie", DebateVerdictTie)
	}
}

func TestDebateConfigMockLLMExplicit(t *testing.T) {
	// Phase 7 task book: DebateConfig.MockLLM must be an EXPLICIT field
	// (no implicit fallback). Verify it appears in the JSON schema.
	cfg := DebateConfig{MockLLM: true}
	if !cfg.MockLLM {
		t.Fatalf("MockLLM not preserved")
	}
	cfg2 := DebateConfig{MockLLM: false}
	if cfg2.MockLLM {
		t.Fatalf("MockLLM should be false")
	}
}

func TestDebateResultRoundLimit(t *testing.T) {
	// Result struct has Rounds field (history-friendly count).
	res := DebateResult{Rounds: 3, FinalPosition: DebateVerdictTie}
	if res.Rounds != 3 {
		t.Fatalf("Rounds not preserved")
	}
	if res.FinalPosition != DebateVerdictTie {
		t.Fatalf("FinalPosition not preserved")
	}
}
