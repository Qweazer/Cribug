package activities

import (
	"context"
	"testing"
	"time"

	"cribug/internal/types"
)

func TestReActActivities_NewReActActivities(t *testing.T) {
	activities := NewReActActivities("http://localhost:8000", "localhost:6379", "", 0)
	if activities == nil {
		t.Fatal("NewReActActivities returned nil")
	}
	if activities.llmClient == nil {
		t.Fatal("llmClient is nil")
	}
	if activities.redisClient == nil {
		t.Fatal("redisClient is nil")
	}
}

func TestExecuteReActNodeInput_Structure(t *testing.T) {
	input := ExecuteReActNodeInput{
		TaskID:      "test-task-1",
		NodeID:      "n1",
		Prompt:      "Test prompt",
		Model:       "gpt-4o-mini",
		Temperature: 0.7,
		MaxTokens:   1024,
		ReactConfig: types.ReactLoopConfig{
			EnableReAct:       true,
			MaxIterations:    3,
			EarlyStopOnAnswer: true,
		},
		TTLSeconds: 86400,
	}

	if input.TaskID != "test-task-1" {
		t.Errorf("TaskID = %s, want test-task-1", input.TaskID)
	}
	if input.NodeID != "n1" {
		t.Errorf("NodeID = %s, want n1", input.NodeID)
	}
	if input.ReactConfig.EnableReAct != true {
		t.Errorf("EnableReAct = %v, want true", input.ReactConfig.EnableReAct)
	}
	if input.ReactConfig.MaxIterations != 3 {
		t.Errorf("MaxIterations = %d, want 3", input.ReactConfig.MaxIterations)
	}
	if input.ReactConfig.EarlyStopOnAnswer != true {
		t.Errorf("EarlyStopOnAnswer = %v, want true", input.ReactConfig.EarlyStopOnAnswer)
	}
}

func TestExecuteReActNodeOutput_Structure(t *testing.T) {
	output := ExecuteReActNodeOutput{
		Result: &types.ReactResult{
			Steps: []types.ReactStep{
				{
					Iteration:   1,
					Thought:     "Let me think...",
					Action:      "FINAL: answer",
					Observation: "Result",
					Timestamp:   time.Now().UTC().Format(time.RFC3339),
				},
			},
			FinalAnswer:    "answer",
			IterationsUsed: 1,
		},
		Usage: &types.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
	}

	if output.Result.IterationsUsed != 1 {
		t.Errorf("IterationsUsed = %d, want 1", output.Result.IterationsUsed)
	}
	if len(output.Result.Steps) != 1 {
		t.Errorf("Steps length = %d, want 1", len(output.Result.Steps))
	}
	if output.Usage.TotalTokens != 150 {
		t.Errorf("TotalTokens = %d, want 150", output.Usage.TotalTokens)
	}
}

func TestReactLoopConfig_Defaults(t *testing.T) {
	// Test that ReactLoopConfig has correct defaults per spec
	config := types.ReactLoopConfig{}

	// Default values should be false/0
	if config.EnableReAct != false {
		t.Errorf("EnableReAct default = %v, want false", config.EnableReAct)
	}
	if config.MaxIterations != 0 {
		t.Errorf("MaxIterations default = %d, want 0", config.MaxIterations)
	}
	if config.EarlyStopOnAnswer != false {
		t.Errorf("EarlyStopOnAnswer default = %v, want false", config.EarlyStopOnAnswer)
	}
}

func TestReactLoopConfig_MaxIterationsBound(t *testing.T) {
	// Test that max iterations is bounded at 10 (enforced at runtime in executeReActLoop)
	config := types.ReactLoopConfig{
		EnableReAct:       true,
		MaxIterations:    15, // Exceeds max
		EarlyStopOnAnswer: true,
	}

	// Config struct itself doesn't enforce bounds - runtime code caps at 10
	// This test documents the expected runtime behavior
	if config.MaxIterations <= 10 {
		t.Log("MaxIterations <= 10, will be used directly")
	} else {
		t.Log("MaxIterations > 10, should be capped to 10 at runtime")
	}
}

func TestReactStep_Structure(t *testing.T) {
	step := types.ReactStep{
		Iteration:   1,
		Thought:     "I need to calculate this",
		Action:      "TOOL:calculator:{\"expr\":\"2+2\"}",
		Observation: "4",
		Timestamp:   "2026-05-20T10:00:00Z",
	}

	if step.Iteration != 1 {
		t.Errorf("Iteration = %d, want 1", step.Iteration)
	}
	if step.Thought != "I need to calculate this" {
		t.Errorf("Thought = %s, want 'I need to calculate this'", step.Thought)
	}
	if step.Action != "TOOL:calculator:{\"expr\":\"2+2\"}" {
		t.Errorf("Action = %s, want tool call", step.Action)
	}
	if step.Observation != "4" {
		t.Errorf("Observation = %s, want '4'", step.Observation)
	}
}

func TestReactResult_Structure(t *testing.T) {
	result := types.ReactResult{
		Steps: []types.ReactStep{
			{Iteration: 1, Thought: "Step 1", Action: "ACT 1", Observation: "OBS 1"},
			{Iteration: 2, Thought: "Step 2", Action: "FINAL: answer", Observation: ""},
		},
		FinalAnswer:    "answer",
		IterationsUsed: 2,
	}

	if result.IterationsUsed != 2 {
		t.Errorf("IterationsUsed = %d, want 2", result.IterationsUsed)
	}
	if len(result.Steps) != 2 {
		t.Errorf("Steps length = %d, want 2", len(result.Steps))
	}
	if result.FinalAnswer != "answer" {
		t.Errorf("FinalAnswer = %s, want 'answer'", result.FinalAnswer)
	}
}

func TestDAGNode_ReactConfig(t *testing.T) {
	// Test that DAGNode supports ReactConfig
	node := types.DAGNode{
		ID:        "test-node",
		Type:      "llm",
		Name:      "Test Node",
		DependsOn: []string{},
		UseLLM:    true,
		ReactConfig: &types.ReactLoopConfig{
			EnableReAct:       true,
			MaxIterations:    3,
			EarlyStopOnAnswer: true,
		},
	}

	if node.ReactConfig == nil {
		t.Fatal("ReactConfig is nil")
	}
	if node.ReactConfig.EnableReAct != true {
		t.Errorf("EnableReAct = %v, want true", node.ReactConfig.EnableReAct)
	}
	if node.UseLLM != true {
		t.Errorf("UseLLM = %v, want true", node.UseLLM)
	}
}

func TestIsFinalAnswer(t *testing.T) {
	tests := []struct {
		action string
		want   bool
	}{
		{"FINAL: answer", true},
		{"final: answer", true},
		{"FINAL:42", true},
		{"Some action", false},
		{"", false},
		{"final", false},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			got := isFinalAnswer(tt.action)
			if got != tt.want {
				t.Errorf("isFinalAnswer(%q) = %v, want %v", tt.action, got, tt.want)
			}
		})
	}
}

// TestParseFinalAnswerFn is a compile-time check for runtime behavior
func TestParseFinalAnswerFn(t *testing.T) {
	steps := []types.ReactStep{
		{Iteration: 1, Action: "TOOL:calc", Observation: "4"},
		{Iteration: 2, Action: "FINAL: 42", Observation: ""},
	}
	got := parseFinalAnswer(steps)
	// Note: parseFinalAnswer trims the leading space from "FINAL: 42"
	if got != "42" {
		t.Errorf("parseFinalAnswer = %q, want %q", got, "42")
	}
}

func TestParseFinalAnswer(t *testing.T) {
	tests := []struct {
		name  string
		steps []types.ReactStep
		want  string
	}{
		{
			name: "with FINAL marker",
			steps: []types.ReactStep{
				{Iteration: 1, Action: "TOOL:calc", Observation: "4"},
				{Iteration: 2, Action: "FINAL: 42", Observation: ""},
			},
			want: "42", // trimmed
		},
		{
			name: "no FINAL marker - use last observation",
			steps: []types.ReactStep{
				{Iteration: 1, Action: "TOOL:calc", Observation: "4"},
			},
			want: "4",
		},
		{
			name: "empty steps",
			steps: []types.ReactStep{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFinalAnswer(tt.steps)
			if got != tt.want {
				t.Errorf("parseFinalAnswer() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseReActResponse(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantThought bool
		wantAction  string
	}{
		{
			name:        "with FINAL marker",
			content:     "Let me think about this.\nFINAL: the answer is 42",
			wantThought: true,
			wantAction:  "FINAL: the answer is 42",
		},
		{
			name:        "no FINAL marker - entire response is action",
			content:     "The answer is simply 42.",
			wantThought: false,
			wantAction:  "The answer is simply 42.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			thought, action, _ := parseReActResponse(tt.content)
			if tt.wantThought && thought == "" {
				t.Errorf("parseReActResponse() thought empty, want non-empty")
			}
			if action != tt.wantAction {
				t.Errorf("parseReActResponse() action = %q, want %q", action, tt.wantAction)
			}
		})
	}
}

// TestReActActivityCanBeCalled is a compile-time check that ExecuteReActNode has correct signature
func TestReActActivityCanBeCalled(t *testing.T) {
	activities := NewReActActivities("http://localhost:8000", "localhost:6379", "", 0)

	// This is a compile-time check - if the signature is wrong, this won't compile
	var _ func(context.Context, ExecuteReActNodeInput) (*ExecuteReActNodeOutput, error) = activities.ExecuteReActNode

	t.Log("ExecuteReActNode signature is valid")
}