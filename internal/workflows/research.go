package workflows

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const ResearchSynthesisWorkflowName = "ResearchSynthesisWorkflow"

// ── Workflow input/output types ──────────────────────────────────────────

type ResearchSynthesisWorkflowInput struct {
	Query      string
	AgentID    string
	WorkflowID string
	Collection string
	TopK       int
	Model      string
	MockLLM    bool
}

type ResearchSynthesisWorkflowOutput struct {
	QueryID         string
	Answer          string
	Evidence        []EvidenceReference
	SubqueryAnswers []SubqueryAnswer
	TokenCount      int
	Error           string
	Partial         bool
}

// ── Activity input/output types ──────────────────────────────────────────
// These match the types being created simultaneously in internal/activities/research.go.

type DecomposeQueryInput struct {
	Query      string
	AgentID    string
	WorkflowID string
}

type DecomposeQueryOutput struct {
	QueryID    string
	Subqueries []ResearchSubquery
}

type ResearchSubquery struct {
	ID   string
	Text string
}

type RetrieveEvidenceInput struct {
	Subquery   string
	Collection string
	TopK       int
}

type RetrieveEvidenceOutput struct {
	Summary  string
	ChunkIDs []string
	Score    float64
}

type SynthesizeResultInput struct {
	QueryID      string
	Query        string
	Subqueries   []ResearchSubquery
	EvidenceText []string
	Model        string
	MockLLM      bool
}

type SynthesizeResultOutput struct {
	Answer          string
	SubqueryAnswers []SubqueryAnswer
	TokenCount      int
}

type SubqueryAnswer struct {
	SubqueryID string
	Answer     string
	Evidence   string
}

type EvidenceReference struct {
	SubqueryID string
	ChunkCount int
	TopScore   float64
	Summary    string
}

// ResearchSynthesisWorkflow decomposes a research query into subqueries,
// retrieves evidence for each subquery, and synthesizes a final answer.
//
// Flow:
//  1. DecomposeQueryActivity  → subqueries
//  2. For each subquery (sequential):
//     RetrieveEvidenceActivity → evidence per subquery
//  3. SynthesizeResultActivity → final answer
//
// No direct HTTP, DB, Redis, LLM, goroutine, time.Now, rand, uuid.New in body.
func ResearchSynthesisWorkflow(ctx workflow.Context, input ResearchSynthesisWorkflowInput) (ResearchSynthesisWorkflowOutput, error) {
	ao := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	})

	// Step 1: Decompose query into subqueries
	var decompOutput DecomposeQueryOutput
	err := workflow.ExecuteActivity(ao, "DecomposeQueryActivity", DecomposeQueryInput{
		Query:      input.Query,
		AgentID:    input.AgentID,
		WorkflowID: input.WorkflowID,
	}).Get(ctx, &decompOutput)
	if err != nil {
		return ResearchSynthesisWorkflowOutput{Error: err.Error()}, err
	}

	// Early exit if no subqueries were produced
	if len(decompOutput.Subqueries) == 0 {
		return ResearchSynthesisWorkflowOutput{
			QueryID: decompOutput.QueryID,
			Answer:  "Query could not be decomposed into subqueries.",
		}, nil
	}

	// Step 2: Retrieve evidence for each subquery (sequential)
	evidenceTexts := make([]string, len(decompOutput.Subqueries))
	var evidenceRefs []EvidenceReference
	failedCount := 0

	for i, sq := range decompOutput.Subqueries {
		var evidenceOutput RetrieveEvidenceOutput
		err := workflow.ExecuteActivity(ao, "RetrieveEvidenceActivity", RetrieveEvidenceInput{
			Subquery:   sq.Text,
			Collection: input.Collection,
			TopK:       input.TopK,
		}).Get(ctx, &evidenceOutput)

		if err != nil {
			failedCount++
			evidenceTexts[i] = ""
			continue
		}
		evidenceTexts[i] = evidenceOutput.Summary
		evidenceRefs = append(evidenceRefs, EvidenceReference{
			SubqueryID: sq.ID,
			ChunkCount: len(evidenceOutput.ChunkIDs),
			TopScore:   evidenceOutput.Score,
			Summary:    truncate(evidenceOutput.Summary, 200),
		})
	}

	// Step 3: Synthesize final result from all evidence
	var synthOutput SynthesizeResultOutput
	err = workflow.ExecuteActivity(ao, "SynthesizeResultActivity", SynthesizeResultInput{
		QueryID:      decompOutput.QueryID,
		Query:        input.Query,
		Subqueries:   decompOutput.Subqueries,
		EvidenceText: evidenceTexts,
		Model:        input.Model,
		MockLLM:      input.MockLLM,
	}).Get(ctx, &synthOutput)
	if err != nil {
		return ResearchSynthesisWorkflowOutput{
			QueryID:  decompOutput.QueryID,
			Evidence: evidenceRefs,
			Error:    fmt.Sprintf("synthesis failed: %v", err),
			Partial:  true,
		}, nil
	}

	return ResearchSynthesisWorkflowOutput{
		QueryID:         decompOutput.QueryID,
		Answer:          synthOutput.Answer,
		Evidence:        evidenceRefs,
		SubqueryAnswers: synthOutput.SubqueryAnswers,
		TokenCount:      synthOutput.TokenCount,
		Partial:         failedCount > 0,
	}, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
