package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"cribug/internal/types"
)

// ToTActivities holds dependencies for Tree-of-Thoughts Activities.
type ToTActivities struct {
	agentActs *AgentActivities
}

// NewToTActivities creates a new ToTActivities instance.
func NewToTActivities(llmServiceURL string) *ToTActivities {
	return &ToTActivities{agentActs: NewAgentActivities(llmServiceURL)}
}

// ─── GenerateThoughtsActivity ──────────────────────────────────────────

type GenerateThoughtsInput struct {
	ParentThoughtRef string `json:"parent_thought_ref"`
	ParentSummary    string `json:"parent_summary"`
	ParentID         string `json:"parent_id"`
	Query            string `json:"query"`
	Depth            int    `json:"depth"`
	Count            int    `json:"count"` // branching factor
	Model            string `json:"model"`
	MockLLM          bool   `json:"mock_llm"`
	TaskID           string `json:"task_id"`
	WorkflowID       string `json:"workflow_id"`
	RunID            string `json:"run_id"`
}

type GenerateThoughtsResult struct {
	Thoughts   []types.ThoughtNode `json:"thoughts"`
	TokensUsed int                 `json:"tokens_used"`
	Mode       string              `json:"mode,omitempty"`
}

func (ta *ToTActivities) GenerateThoughts(ctx context.Context, input GenerateThoughtsInput) (*GenerateThoughtsResult, error) {
	if input.MockLLM {
		thoughts := make([]types.ThoughtNode, input.Count)
		for i := 0; i < input.Count; i++ {
			summary := fmt.Sprintf("Mock thought branch %d at depth %d for: %s", i+1, input.Depth, truncate(input.Query, 60))
			thoughts[i] = types.ThoughtNode{
				ID:          fmt.Sprintf("n-%d-%d", input.Depth, i+1),
				ParentID:    input.ParentID,
				Depth:       input.Depth,
				Score:       0.0,
				Status:      types.ThoughtStatusActive,
				ThoughtRef:  fmt.Sprintf("tot:%s:thought:n-%d-%d", input.WorkflowID, input.Depth, i+1),
				Summary:     truncate(summary, 200),
				TokensUsed:  20,
				IsTerminal:  input.Depth >= 2, // mock terminal at depth 2
				Explanation: fmt.Sprintf("mock explanation for branch %d", i+1),
			}
		}
		return &GenerateThoughtsResult{Thoughts: thoughts, TokensUsed: input.Count * 20, Mode: "mock"}, nil
	}

	// Real LLM: reuse AgentActivity to generate branches
	prompt := fmt.Sprintf(
		`Given the query: "%s" and the parent reasoning: "%s", generate %d distinct reasoning paths (thought branches) at depth %d.
Each branch should explore a different angle or approach. Return as a numbered list with brief 1-2 sentence descriptions.`,
		input.Query, input.ParentSummary, input.Count, input.Depth,
	)

	result, err := ta.agentActs.CallLLM(ctx, AgentActivityInput{
		TaskID:                input.TaskID,
		WorkflowID:            input.WorkflowID,
		RunID:                 input.RunID,
		Query:                 prompt,
		Model:                 input.Model,
		Temperature:           0.8,
		MaxCompletionTokens:   512,
		AllowedCompletionTokens: 512,
	})
	if err != nil {
		return nil, fmt.Errorf("generate thoughts: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(result.Answer), "\n")
	thoughts := make([]types.ThoughtNode, 0, input.Count)
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || len(thoughts) >= input.Count {
			continue
		}
		// Strip leading numbers like "1. " or "1) "
		line = strings.TrimLeft(line, "0123456789.)- ")
		if len(line) < 5 {
			continue
		}
		thoughts = append(thoughts, types.ThoughtNode{
			ID:         fmt.Sprintf("n-%d-%d", input.Depth, i+1),
			ParentID:   input.ParentID,
			Depth:      input.Depth,
			Score:      0.0,
			Status:     types.ThoughtStatusActive,
			ThoughtRef: fmt.Sprintf("tot:%s:thought:n-%d-%d", input.WorkflowID, input.Depth, i+1),
			Summary:    truncate(line, 200),
			TokensUsed: result.Usage.TotalTokens / input.Count,
		})
	}
	if len(thoughts) == 0 {
		thoughts = append(thoughts, types.ThoughtNode{
			ID: fmt.Sprintf("n-%d-1", input.Depth), ParentID: input.ParentID, Depth: input.Depth,
			Status: types.ThoughtStatusActive, Summary: truncate(result.Answer, 200),
			ThoughtRef: fmt.Sprintf("tot:%s:thought:n-%d-1", input.WorkflowID, input.Depth),
			TokensUsed: result.Usage.TotalTokens,
		})
	}

	return &GenerateThoughtsResult{Thoughts: thoughts, TokensUsed: result.Usage.TotalTokens, Mode: "real"}, nil
}

// ─── ScoreThoughtActivity ──────────────────────────────────────────────

type ScoreThoughtInput struct {
	ThoughtRef  string  `json:"thought_ref"`
	ThoughtSummary string `json:"thought_summary"`
	Query       string  `json:"query"`
	ParentScore float64 `json:"parent_score"`
	Depth       int     `json:"depth"`
	Method      string  `json:"method"` // "scoring" | "voting" | "llm"
	MockLLM     bool    `json:"mock_llm"`
	Model       string  `json:"model"`
	TaskID      string  `json:"task_id"`
	WorkflowID  string  `json:"workflow_id"`
	RunID       string  `json:"run_id"`
}

type ScoreThoughtResult struct {
	Score       float64 `json:"score"`
	Explanation string  `json:"explanation"`
	TokensUsed  int     `json:"tokens_used"`
	Mode        string  `json:"mode,omitempty"`
}

func (ta *ToTActivities) ScoreThought(ctx context.Context, input ScoreThoughtInput) (*ScoreThoughtResult, error) {
	if input.MockLLM {
		score := 0.5 + 0.1*float64(input.Depth)
		if score > 1.0 {
			score = 1.0
		}
		return &ScoreThoughtResult{
			Score:       score,
			Explanation: fmt.Sprintf("mock heuristic score %.2f at depth %d", score, input.Depth),
			TokensUsed:  10,
			Mode:        "mock",
		}, nil
	}

	prompt := fmt.Sprintf(
		`Score this reasoning path for the query "%s" on a scale of 0.0-1.0:
Reasoning: %s
Consider relevance, logical soundness, and depth. Return a JSON with "score" (float) and "explanation" (short).`,
		input.Query, input.ThoughtSummary,
	)

	result, err := ta.agentActs.CallLLM(ctx, AgentActivityInput{
		TaskID:                input.TaskID,
		WorkflowID:            input.WorkflowID,
		RunID:                 input.RunID,
		Query:                 prompt,
		Model:                 input.Model,
		Temperature:           0.3,
		MaxCompletionTokens:   256,
		AllowedCompletionTokens: 256,
	})
	if err != nil {
		return nil, fmt.Errorf("score thought: %w", err)
	}

	// Parse JSON score from LLM response
	score, explanation := parseScoreJSON(result.Answer)
	return &ScoreThoughtResult{
		Score:       score,
		Explanation: truncate(explanation, 200),
		TokensUsed:  result.Usage.TotalTokens,
		Mode:        "real",
	}, nil
}

// ─── FindBestPathActivity ──────────────────────────────────────────────

type FindBestPathInput struct {
	TreeID string             `json:"tree_id"`
	Nodes  []types.ThoughtNode `json:"nodes"`
}

type FindBestPathResult struct {
	BestPath    []string `json:"best_path"`
	BestPathRef string   `json:"best_path_ref"`
	Confidence  float64  `json:"confidence"`
}

func (ta *ToTActivities) FindBestPath(ctx context.Context, input FindBestPathInput) (*FindBestPathResult, error) {
	if len(input.Nodes) == 0 {
		return &FindBestPathResult{}, nil
	}

	// Build parent->child map
	nodeMap := make(map[string]types.ThoughtNode, len(input.Nodes))
	for _, n := range input.Nodes {
		nodeMap[n.ID] = n
	}

	// Greedy: from each terminal node, trace back to root, pick highest cumulative score
	bestPath := []string{}
	bestScore := -1.0

	for _, n := range input.Nodes {
		if !n.IsTerminal && n.Depth > 0 {
			// also consider deepest non-terminal nodes
		}
		if n.IsTerminal || n.Depth == getMaxDepth(input.Nodes) {
			path := []string{n.ID}
			cumScore := n.Score
			current := n.ParentID
			for current != "" {
				if parent, ok := nodeMap[current]; ok {
					path = append([]string{current}, path...)
					cumScore += parent.Score
					current = parent.ParentID
				} else {
					break
				}
			}
			avgScore := cumScore / float64(len(path))
			if avgScore > bestScore {
				bestScore = avgScore
				bestPath = path
			}
		}
	}

	if len(bestPath) == 0 {
		bestPath = []string{"root"}
		bestScore = 1.0
	}

	return &FindBestPathResult{
		BestPath:    bestPath,
		BestPathRef: fmt.Sprintf("tot:%s:best_path", input.TreeID),
		Confidence:  math.Min(bestScore, 1.0),
	}, nil
}

func getMaxDepth(nodes []types.ThoughtNode) int {
	maxD := 0
	for _, n := range nodes {
		if n.Depth > maxD {
			maxD = n.Depth
		}
	}
	return maxD
}

// ─── SynthesizeToTResultActivity ──────────────────────────────────────

type SynthesizeToTResultInput struct {
	BestPathRef string             `json:"best_path_ref"`
	Nodes       []types.ThoughtNode `json:"nodes"`
	BestPath    []string            `json:"best_path"`
	Query       string              `json:"query"`
	Model       string              `json:"model"`
	MockLLM     bool                `json:"mock_llm"`
	TaskID      string              `json:"task_id"`
	WorkflowID  string              `json:"workflow_id"`
	RunID       string              `json:"run_id"`
}

type SynthesizeToTResultResult struct {
	SolutionRef     string `json:"solution_ref"`
	SolutionSummary string `json:"solution_summary"`
	TokensUsed      int    `json:"tokens_used"`
	Mode            string `json:"mode,omitempty"`
}

func (ta *ToTActivities) SynthesizeToTResult(ctx context.Context, input SynthesizeToTResultInput) (*SynthesizeToTResultResult, error) {
	if input.MockLLM {
		mockSolution := fmt.Sprintf("Mock synthesized solution from best path %v for query: %s", input.BestPath, truncate(input.Query, 60))
		return &SynthesizeToTResultResult{
			SolutionRef:     fmt.Sprintf("tot:%s:solution", input.WorkflowID),
			SolutionSummary: truncate(mockSolution, 500),
			TokensUsed:      40,
			Mode:            "mock",
		}, nil
	}

	// Collect summaries from best path nodes
	nodeMap := make(map[string]types.ThoughtNode, len(input.Nodes))
	for _, n := range input.Nodes {
		nodeMap[n.ID] = n
	}
	var summaries []string
	for _, id := range input.BestPath {
		if n, ok := nodeMap[id]; ok {
			summaries = append(summaries, n.Summary)
		}
	}

	prompt := fmt.Sprintf(
		`Synthesize a final answer for the query "%s" using these reasoning steps:
%s
Provide a concise, well-structured conclusion.`,
		input.Query, strings.Join(summaries, "\n"),
	)

	result, err := ta.agentActs.CallLLM(ctx, AgentActivityInput{
		TaskID:                input.TaskID,
		WorkflowID:            input.WorkflowID,
		RunID:                 input.RunID,
		Query:                 prompt,
		Model:                 input.Model,
		Temperature:           0.5,
		MaxCompletionTokens:   1024,
		AllowedCompletionTokens: 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("synthesize ToT result: %w", err)
	}

	return &SynthesizeToTResultResult{
		SolutionRef:     fmt.Sprintf("tot:%s:solution", input.WorkflowID),
		SolutionSummary: truncate(result.Answer, 500),
		TokensUsed:      result.Usage.TotalTokens,
		Mode:            "real",
	}, nil
}

// ─── Helpers ───────────────────────────────────────────────────────────

func sortNodesByScore(nodes []types.ThoughtNode) {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Score > nodes[j].Score
	})
}

// parseScoreJSON extracts a score from an LLM response. Tries JSON first, then regex fallback.
func parseScoreJSON(raw string) (float64, string) {
	raw = strings.TrimSpace(raw)
	// Try to find a JSON block
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		block := raw[start : end+1]
		var parsed struct {
			Score       interface{} `json:"score"`
			Explanation string      `json:"explanation"`
		}
		if err := json.Unmarshal([]byte(block), &parsed); err == nil {
			return toFloat(parsed.Score), parsed.Explanation
		}
	}
	// Fallback: look for "score": 0.XX pattern
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "\"score\"") || strings.Contains(line, "score:") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				val := strings.TrimRight(strings.TrimSpace(parts[len(parts)-1]), ",} ")
				if s, err := strconv.ParseFloat(val, 64); err == nil && s >= 0 && s <= 1.0 {
					return s, raw
				}
			}
		}
	}
	// Heuristic fallback: count positive/negative keywords
	pos := countKeywords(raw, []string{"strong", "good", "relevant", "clear", "logical", "excellent", "sound"})
	neg := countKeywords(raw, []string{"weak", "poor", "irrelevant", "unclear", "flawed", "bad"})
	score := 0.5 + float64(pos)*0.1 - float64(neg)*0.1
	if score > 1.0 { score = 1.0 }
	if score < 0.0 { score = 0.0 }
	return score, raw
}

func countKeywords(text string, keywords []string) int {
	lower := strings.ToLower(text)
	count := 0
	for _, kw := range keywords {
		if strings.Contains(lower, kw) { count++ }
	}
	return count
}

func toFloat(v interface{}) float64 {
	switch val := v.(type) {
	case float64: return val
	case json.Number:
		f, _ := val.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	default:
		return 0.5
	}
}

