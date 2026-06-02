package llm

import (
	"os"
	"testing"
)

func TestValidateModelProvider(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		wantErr  bool
	}{
		{"minimax+MiniMax-M2.7 valid", "minimax", "MiniMax-M2.7", false},
		{"openai+gpt-4o-mini valid", "openai", "gpt-4o-mini", false},
		{"minimax+gpt-4o-mini rejected", "minimax", "gpt-4o-mini", true},
		{"anthropic+claude-sonnet valid", "anthropic", "claude-sonnet-4-5-20250929", false},
		{"anthropic+gpt-4o-mini rejected", "anthropic", "gpt-4o-mini", true},
		{"openai+gpt-5.1 valid", "openai", "gpt-5.1", false},
		{"minimax+claude rejected", "minimax", "claude-sonnet-4-5-20250929", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateModelProvider(tt.provider, tt.model)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateModelProvider(%q,%q) err=%v wantErr=%v", tt.provider, tt.model, err, tt.wantErr)
			}
		})
	}
}

func TestResolveWithProfile(t *testing.T) {
	catalog, err := LoadCatalog("../../config/model_providers.yaml")
	if err != nil {
		t.Skipf("catalog not found: %v", err)
	}

	// minimax + MiniMax-M2.7: valid
	os.Setenv("LLM_API_KEY", "test-key-for-unit-test")
	profile := &LLMProfile{
		Provider:    "minimax",
		ChatModel:   "MiniMax-M2.7",
		APIKeyEnv:   "LLM_API_KEY",
		RequireReal: true,
	}
	r := (&ConfigResolver{}).WithProfile(profile).WithCatalog(catalog)
	ec := r.Resolve()
	if ec.ValidationError != "" {
		t.Errorf("valid profile should not error: %s", ec.ValidationError)
	}
	if ec.ChatModel != "MiniMax-M2.7" {
		t.Errorf("expected MiniMax-M2.7, got %s", ec.ChatModel)
	}

	// minimax + gpt-4o-mini: rejected
	badProfile := &LLMProfile{Provider: "minimax", ChatModel: "gpt-4o-mini", APIKeyEnv: "LLM_API_KEY"}
	r2 := (&ConfigResolver{}).WithProfile(badProfile).WithCatalog(catalog)
	ec2 := r2.Resolve()
	if ec2.ValidationError == "" {
		t.Error("minimax+gpt-4o-mini should be rejected")
	}

	// openai + gpt-4o-mini: valid (if in catalog)
	openaiProfile := &LLMProfile{Provider: "openai", ChatModel: "gpt-5-nano-2025-08-07", APIKeyEnv: "OPENAI_API_KEY"}
	r3 := (&ConfigResolver{}).WithProfile(openaiProfile).WithCatalog(catalog)
	ec3 := r3.Resolve()
	t.Logf("openai+gpt-5-nano: model=%s validation=%s", ec3.ChatModel, ec3.ValidationError)

	// Empty model → use default
	defaultProfile := &LLMProfile{Provider: "minimax", ChatModel: "", APIKeyEnv: "LLM_API_KEY"}
	r4 := (&ConfigResolver{}).WithProfile(defaultProfile).WithCatalog(catalog)
	ec4 := r4.Resolve()
	if ec4.ChatModel == "" {
		t.Error("empty model should use catalog default")
	}
	t.Logf("default model for minimax: %s", ec4.ChatModel)

	// require_real + no API key → error
	if os.Getenv("LLM_API_KEY") == "" {
		strictProfile := &LLMProfile{Provider: "minimax", ChatModel: "MiniMax-M2.7", APIKeyEnv: "NONEXISTENT_KEY", RequireReal: true}
		r5 := (&ConfigResolver{}).WithProfile(strictProfile).WithCatalog(catalog)
		ec5 := r5.Resolve()
		if ec5.ValidationError == "" {
			t.Error("require_real with missing key should error")
		}
	}
}

func TestDisplayName(t *testing.T) {
	if DisplayName("minimax") != "MiniMax" {
		t.Errorf("expected MiniMax, got %s", DisplayName("minimax"))
	}
	if DisplayName("openai") != "OpenAI" {
		t.Errorf("expected OpenAI, got %s", DisplayName("openai"))
	}
}
