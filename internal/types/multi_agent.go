package types

// AgentRole represents the role of an agent in multi-agent workflow
type AgentRole string

const (
	AgentRolePlanner     AgentRole = "planner"
	AgentRoleResearcher  AgentRole = "researcher"
	AgentRoleCritic      AgentRole = "critic"
	AgentRoleSynthesizer AgentRole = "synthesizer"
)

// AgentStep represents a single step in multi-agent execution
type AgentStep struct {
	TaskID          string    `json:"task_id"`
	Role            AgentRole `json:"role"`
	Input           string    `json:"input"`
	Output          string    `json:"output,omitempty"`
	Status          string    `json:"status"` // "started" | "completed" | "failed"
	Error           string    `json:"error,omitempty"`
	PromptTokens    int       `json:"prompt_tokens,omitempty"`
	CompletionTokens int       `json:"completion_tokens,omitempty"`
	TotalTokens     int       `json:"total_tokens,omitempty"`
}

// MultiAgentResult represents the final result of multi-agent execution
type MultiAgentResult struct {
	TaskID          string       `json:"task_id"`
	Steps           []AgentStep `json:"steps"`
	FinalAnswer     string       `json:"final_answer"`
	AgentCount      int          `json:"agent_count"`
	CompletedAgents int          `json:"completed_agents"`
	TotalTokens     int          `json:"total_tokens"`
}

// RunAgentActivityInput is the input for RunAgentActivity
type RunAgentActivityInput struct {
	TaskID        string    `json:"task_id"`
	Role          AgentRole `json:"role"`
	Query         string    `json:"query"`
	PreviousSteps []AgentStep `json:"previous_steps,omitempty"`
}

// RunAgentActivityOutput is the output from RunAgentActivity
type RunAgentActivityOutput struct {
	Step AgentStep `json:"step"`
}

// RunSynthesizerAgentInput is the input for synthesizer agent (LLM-backed)
type RunSynthesizerAgentInput struct {
	TaskID               string
	WorkflowID           string
	RunID                string
	Query                string
	Model                string
	Temperature          float64
	MaxCompletionTokens  int
	PlannerOutput        string
	ResearcherOutput     string
	CriticOutput         string
}

// RunSynthesizerAgentOutput is the output from synthesizer agent
type RunSynthesizerAgentOutput struct {
	Step       AgentStep
	LLMOutput  string
	PromptTokens    int
	CompletionTokens int
	TotalTokens     int
	LatencyMS       int64
}

// RunCriticAgentInput is the input for critic agent (LLM-backed)
type RunCriticAgentInput struct {
	TaskID               string
	WorkflowID           string
	RunID                string
	Query                string
	Model                string
	Temperature          float64
	MaxCompletionTokens  int
	PlannerOutput        string
	ResearcherOutput     string
	CurrentAnswer        string
}

// RunCriticAgentOutput is the output from critic agent
type RunCriticAgentOutput struct {
	Step       AgentStep
	LLMOutput  string
	PromptTokens    int
	CompletionTokens int
	TotalTokens     int
	LatencyMS       int64
}