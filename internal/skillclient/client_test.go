package skillclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListSkills(t *testing.T) {
	expected := []string{"tool1", "tool2", "tool3"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/tools" {
			t.Errorf("expected /tools, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(expected); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	skills, err := client.ListSkills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != len(expected) {
		t.Fatalf("got %d skills, want %d", len(skills), len(expected))
	}
	for i, s := range skills {
		if s != expected[i] {
			t.Errorf("skill %d: got %q, want %q", i, s, expected[i])
		}
	}
}

func TestGetSkillMetadata(t *testing.T) {
	expected := SkillMetadata{
		Name:            "test_skill",
		Version:         "1.0.0",
		Description:     "A test skill",
		Category:        "utility",
		ExecutionMode:   "sync",
		RequiresSandbox: false,
		RequiresLLM:     true,
		RiskLevel:       "low",
		SideEffects:     false,
		Parameters: map[string]interface{}{
			"prompt": map[string]interface{}{
				"type":        "string",
				"description": "Input prompt",
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/tools/test_skill" {
			t.Errorf("expected /tools/test_skill, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(expected); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	meta, err := client.GetSkillMetadata(context.Background(), "test_skill")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != expected.Name {
		t.Errorf("Name: got %q, want %q", meta.Name, expected.Name)
	}
	if meta.Version != expected.Version {
		t.Errorf("Version: got %q, want %q", meta.Version, expected.Version)
	}
	if meta.Description != expected.Description {
		t.Errorf("Description: got %q, want %q", meta.Description, expected.Description)
	}
	if meta.RequiresLLM != expected.RequiresLLM {
		t.Errorf("RequiresLLM: got %v, want %v", meta.RequiresLLM, expected.RequiresLLM)
	}
}

func TestGetSkillMetadataNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.GetSkillMetadata(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "skill not found: nonexistent" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestExecuteSkill(t *testing.T) {
	expectedResp := ExecuteResponse{
		ToolName:        "test_skill",
		RequestID:       "req-123",
		Success:         true,
		Output:          map[string]interface{}{"result": "done"},
		ExecutionTimeMs: 150,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/tools/test_skill/execute" {
			t.Errorf("expected /tools/test_skill/execute, got %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", ct)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(expectedResp); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	params := map[string]interface{}{"prompt": "hello"}
	result, err := client.ExecuteSkill(context.Background(), "test_skill", params)
	if err != nil {
		t.Fatal(err)
	}
	if result.ToolName != expectedResp.ToolName {
		t.Errorf("ToolName: got %q, want %q", result.ToolName, expectedResp.ToolName)
	}
	if result.Success != expectedResp.Success {
		t.Errorf("Success: got %v, want %v", result.Success, expectedResp.Success)
	}
	if result.ExecutionTimeMs != expectedResp.ExecutionTimeMs {
		t.Errorf("ExecutionTimeMs: got %d, want %d", result.ExecutionTimeMs, expectedResp.ExecutionTimeMs)
	}
}

func TestExecuteSkillNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	result, err := client.ExecuteSkill(context.Background(), "nonexistent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Error("expected Success=false")
	}
	if result.ErrorType != "tool_not_found" {
		t.Errorf("expected ErrorType=tool_not_found, got %q", result.ErrorType)
	}
	if result.Error != "skill not found" {
		t.Errorf("expected Error='skill not found', got %q", result.Error)
	}
}

func TestExecuteSkillHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	result, err := client.ExecuteSkill(context.Background(), "failing", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Error("expected Success=false")
	}
	if result.ErrorType != "http_error" {
		t.Errorf("expected ErrorType=http_error, got %q", result.ErrorType)
	}
	if result.Error != "HTTP error: 500" {
		t.Errorf("expected Error='HTTP error: 500', got %q", result.Error)
	}
}

func TestExecuteSkillMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("this is not valid json"))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.ExecuteSkill(context.Background(), "bad", nil)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestWithTimeout(t *testing.T) {
	client := NewClient("http://example.com", WithTimeout(5*time.Second))
	if client.timeout != 5*time.Second {
		t.Errorf("expected timeout 5s, got %v", client.timeout)
	}
}
