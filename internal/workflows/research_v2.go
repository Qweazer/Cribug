package workflows

import (
	"fmt"
	"time"

	"cribug/internal/activities"
	"cribug/internal/types"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const ResearchSynthesisV2WorkflowName = "ResearchSynthesisV2Workflow"

// ResearchSynthesisV2Workflow implements Phase 7F Slice 28 multi-source
// research-synthesis with credibility scoring, contradiction detection,
// citation chains, optional reflection / debate, and approval gate.
//
// Constraints (per Phase 7 task book §7.4):
//   - Built on Phase 6F Research-Synthesis v1 (DecomposeQuery /
//     RetrieveEvidence / SynthesizeResult) plus v2 enhancements
//     (multi-source, credibility, contradictions, citation chain).
//   - Workflow is deterministic: no time.Now / rand / goroutines /
//     direct IO. All LLM / DB / VectorDB calls go through Activities.
//   - Long content (sources / evidence / synthesis / final report)
//     always via WorkspaceRef; Workflow history stores only refs +
//     short metadata + counts.
//   - Result must satisfy RoutedExecutionResult via dispatchResearchV2.
func ResearchSynthesisV2Workflow(ctx workflow.Context, input types.ResearchV2WorkflowInput) (*types.ResearchV2WorkflowResult, error) {
	workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
	logger := workflow.GetLogger(ctx)

	cfg := input.Config
	if cfg.MaxSources <= 0 {
		cfg.MaxSources = 5
	}
	if cfg.MaxEvidenceItems <= 0 {
		cfg.MaxEvidenceItems = 20
	}
	if cfg.MaxSubqueries <= 0 {
		cfg.MaxSubqueries = 4
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 2
	}
	if cfg.TokenBudget <= 0 {
		cfg.TokenBudget = 6000
	}
	if cfg.CredibilityThreshold <= 0 {
		cfg.CredibilityThreshold = 0.4
	}
	if len(cfg.SourceTypes) == 0 {
		cfg.SourceTypes = []string{types.SourceTypeLocalRAG}
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    2,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Resolve effective LLM config (mock vs real follows the project profile).
	var llmCfg activities.LLMConfigSnapshot
	err := workflow.ExecuteActivity(ctx, "ResolveEffectiveLLMConfigActivity",
		activities.ResolveEffectiveLLMConfigInput{},
	).Get(ctx, &llmCfg)
	if err != nil || llmCfg.ChatModel == "" {
		logger.Warn("ResolveEffectiveLLMConfig failed, using safe default", "error", err)
		llmCfg = activities.LLMConfigSnapshot{
			Provider: "openai_compatible", ChatModel: "gpt-4o-mini",
			BaseURL: "http://127.0.0.1:8000",
		}
	}
	effectiveModel := llmCfg.ChatModel

	usedMode := "unknown"
	if cfg.MockLLM {
		usedMode = "mock"
	}
	totalTokens := 0
	llmCalls := 0
	sourceCount := 0
	contradictionCount := 0
	workspaceTopic := fmt.Sprintf("research:%s:transcript", workflowID[:min8(len(workflowID))])
	if len(workflowID) < 8 {
		workspaceTopic = fmt.Sprintf("research:%s:transcript", workflowID)
	}

	// If the profile requires real LLM, force MockLLM=false here so
	// the activities actually call the LLM.
	mockLLM := cfg.MockLLM
	if llmCfg.RequireReal {
		mockLLM = false
	}

	// Step 1: Plan research subqueries.
	var planResult activities.PlanResearchResult
	err = workflow.ExecuteActivity(ctx, "PlanResearchActivity",
		activities.PlanResearchInput{
			Query:         input.Query,
			MaxSubqueries: cfg.MaxSubqueries,
			Model:         effectiveModel,
			MockLLM:       mockLLM,
			TaskID:        input.TaskID,
			WorkflowID:    input.WorkflowID,
			RunID:         input.RunID,
		},
	).Get(ctx, &planResult)
	if err != nil {
		return &types.ResearchV2WorkflowResult{
			WorkspaceTopic: workspaceTopic,
			Mode:           usedMode,
			Provider:       llmCfg.Provider,
			ModelUsed:      effectiveModel,
		}, fmt.Errorf("plan research failed: %w", err)
	}
	totalTokens += planResult.TokensUsed
	if planResult.Mode != "" {
		usedMode = planResult.Mode
	}
	llmCalls++

	// Step 2: Multi-source retrieval per subquery.
	allEvidence := make([]types.Evidence, 0)
	sourceRefs := make([]string, 0)
	for _, sq := range planResult.Subqueries {
		if totalTokens >= cfg.TokenBudget {
			logger.Info("Token budget exhausted; stopping retrieval", "tokens", totalTokens)
			break
		}
		var retrResult activities.RetrieveMultiSourceEvidenceResult
		err := workflow.ExecuteActivity(ctx, "RetrieveMultiSourceEvidenceActivity",
			activities.RetrieveMultiSourceEvidenceInput{
				SubqueryID:   sq.ID,
				SubqueryText: sq.Text,
				SourceTypes:  cfg.SourceTypes,
				MaxPerSource: cfg.MaxSources,
				Collection:   "task_embeddings",
				WorkflowID:   input.WorkflowID,
			},
		).Get(ctx, &retrResult)
		if err != nil {
			logger.Warn("retrieve multi-source failed", "subquery", sq.ID, "error", err)
			continue
		}
		for _, e := range retrResult.Evidence {
			allEvidence = append(allEvidence, e)
			sourceRefs = append(sourceRefs, e.ContentRef)
			if len(allEvidence) >= cfg.MaxEvidenceItems {
				break
			}
		}
		if len(allEvidence) >= cfg.MaxEvidenceItems {
			break
		}
	}
	sourceCount = uniqueIDs(sourceRefs)

	// Step 3: Score each source's credibility (per-evidence).
	scoredEvidence := make([]types.Evidence, 0, len(allEvidence))
	for _, e := range allEvidence {
		var scoreResult activities.ScoreSourceCredibilityResult
		err := workflow.ExecuteActivity(ctx, "ScoreSourceCredibilityActivity",
			activities.ScoreSourceCredibilityInput{
				SourceURL:  e.SourceID,
				SourceType: e.SourceType,
			},
		).Get(ctx, &scoreResult)
		if err == nil {
			e.SourceCredibility = types.SourceCredibility{
				SourceType:        e.SourceType,
				Domain:            scoreResult.Domain,
				CredibilityScore:  scoreResult.CredibilityScore,
				QualityScore:      scoreResult.QualityScore,
			}
			e.CredibilityScore = scoreResult.CredibilityScore
		}
		scoredEvidence = append(scoredEvidence, e)
	}

	// Step 4: Filter by credibility threshold.
	var filterResult activities.FilterByCredibilityResult
	err = workflow.ExecuteActivity(ctx, "FilterByCredibilityActivity",
		activities.FilterByCredibilityInput{
			Evidence:  scoredEvidence,
			Threshold: cfg.CredibilityThreshold,
		},
	).Get(ctx, &filterResult)
	if err != nil {
		filterResult.Filtered = scoredEvidence
	}
	filtered := filterResult.Filtered

	// Step 5: Contradiction detection (if enabled).
	var contradictions []types.Contradiction
	if cfg.EnableContradictionDetection {
		var detectResult activities.DetectContradictionsResult
		err := workflow.ExecuteActivity(ctx, "DetectContradictionsActivity",
			activities.DetectContradictionsInput{
				Evidence:  filtered,
				Threshold: cfg.CredibilityThreshold,
			},
		).Get(ctx, &detectResult)
		if err == nil {
			contradictions = detectResult.Contradictions
		}
	}
	contradictionCount = len(contradictions)

	// Mark evidence with IsContradiction flag.
	ctSet := map[string]bool{}
	for _, c := range contradictions {
		for _, id := range c.EvidenceIDs {
			ctSet[id] = true
		}
	}
	for i := range filtered {
		filtered[i].IsContradiction = ctSet[filtered[i].ID]
	}

	// Step 6: Build citation chains.
	var chainResult activities.BuildCitationChainResult
	err = workflow.ExecuteActivity(ctx, "BuildCitationChainActivity",
		activities.BuildCitationChainInput{Evidence: filtered},
	).Get(ctx, &chainResult)
	if err == nil {
		for i, chain := range chainResult.CitationChains {
			if i < len(filtered) {
				filtered[i].CitationChain = chain
			}
		}
	}

	// Step 7: Optional pre-synthesis reflection.
	if cfg.EnableReflection {
		var reflResult activities.ReflectionBeforeSynthesisResult
		err := workflow.ExecuteActivity(ctx, "ReflectionBeforeSynthesisActivity",
			activities.ReflectionBeforeSynthesisInput{
				Query:      input.Query,
				Evidence:   filtered,
				MockLLM:    mockLLM,
				TaskID:     input.TaskID,
				WorkflowID: input.WorkflowID,
				RunID:      input.RunID,
				Model:      effectiveModel,
			},
		).Get(ctx, &reflResult)
		if err == nil {
			filtered = reflResult.ImprovedEvidence
			totalTokens += reflResult.TokensUsed
			if reflResult.Mode != "" {
				usedMode = reflResult.Mode
			}
			llmCalls++
		}
	}

	// Step 8: Optional pre-synthesis debate checkpoint.
	if cfg.EnableDebate {
		var debateResult activities.DebateBeforeSynthesisResult
		err := workflow.ExecuteActivity(ctx, "DebateBeforeSynthesisActivity",
			activities.DebateBeforeSynthesisInput{
				Query:      input.Query,
				Evidence:   filtered,
				Config:     types.DebateConfig{MaxRounds: 1, ModeratorEnabled: true, MockLLM: mockLLM},
				Model:      effectiveModel,
				MockLLM:    mockLLM,
				TaskID:     input.TaskID,
				WorkflowID: input.WorkflowID,
				RunID:      input.RunID,
			},
		).Get(ctx, &debateResult)
		if err == nil {
			filtered = debateResult.ResolvedEvidence
		}
	}

	// Step 9: Citation stats.
	citationStats := types.CitationStats{
		UniqueSources:      uniqueIDsFromEvidence(filtered),
		UniqueDomains:     uniqueDomains(filtered),
		AverageCredibility: averageCredibility(filtered),
	}

	// Step 10: Generate report.
	var reportResult activities.GenerateReportV2Result
	err = workflow.ExecuteActivity(ctx, "GenerateReportV2Activity",
		activities.GenerateReportV2Input{
			QueryID:        planResult.QueryID,
			Query:          input.Query,
			Evidence:       filtered,
			Contradictions: contradictions,
			CitationStats:  citationStats,
			Title:          "",
			Config:         cfg,
			Model:          effectiveModel,
			MockLLM:        mockLLM,
			TaskID:         input.TaskID,
			WorkflowID:     input.WorkflowID,
			RunID:          input.RunID,
		},
	).Get(ctx, &reportResult)
	if err != nil {
		return &types.ResearchV2WorkflowResult{
			WorkspaceTopic: workspaceTopic,
			Mode:           usedMode,
			Provider:       llmCfg.Provider,
			ModelUsed:      effectiveModel,
		}, fmt.Errorf("generate report v2 failed: %w", err)
	}
	totalTokens += reportResult.TokensUsed
	if reportResult.Mode != "" {
		usedMode = reportResult.Mode
	}
	llmCalls++

	// Step 11: Audit (non-fatal).
	_ = workflow.ExecuteActivity(ctx, "AuditResearchV2Activity",
		activities.AuditResearchV2Input{
			WorkflowID:        input.WorkflowID,
			Query:             input.Query,
			TotalRounds:       1,
			TotalTokens:       totalTokens,
			ReportRef:         reportResult.ReportRef,
			TranscriptRef:     workspaceTopic,
			SourceCount:       sourceCount,
			EvidenceCount:     len(filtered),
			ContradictionCount: contradictionCount,
		},
	).Get(ctx, nil)

	if usedMode == "" || usedMode == "unknown" {
		usedMode = "mock"
	}

	finalText := truncateTo(reportResult.Report.ExecutiveSummary, 1500)

	return &types.ResearchV2WorkflowResult{
		Report:            reportResult.Report,
		ReportRef:         reportResult.ReportRef,
		SourceRefs:        sourceRefs,
		EvidenceRef:       fmt.Sprintf("research:%s:evidence_table", input.WorkflowID),
		SynthesisRef:      reportResult.ExecutiveRef,
		FinalAnswerRef:    reportResult.ReportRef,
		FinalAnswerText:   finalText,
		ContradictionRef:  reportResult.ContradictionRef,
		WorkspaceTopic:    workspaceTopic,
		TotalTokens:       totalTokens,
		Iterations:        1,
		Provider:          llmCfg.Provider,
		ModelUsed:         effectiveModel,
		Mode:              usedMode,
		Mock:              usedMode == "mock",
		FallbackUsed:      false,
		LLMCalls:          llmCalls,
		SourceCount:       sourceCount,
		EvidenceCount:     len(filtered),
		ContradictionCount: contradictionCount,
	}, nil
}

// ─── helpers ───────────────────────────────────────────────────────────

func uniqueIDs(refs []string) int {
	set := map[string]struct{}{}
	for _, r := range refs {
		set[r] = struct{}{}
	}
	return len(set)
}

func uniqueIDsFromEvidence(ev []types.Evidence) int {
	set := map[string]struct{}{}
	for _, e := range ev {
		set[e.SourceID] = struct{}{}
	}
	return len(set)
}

func uniqueDomains(ev []types.Evidence) int {
	set := map[string]struct{}{}
	for _, e := range ev {
		if e.SourceCredibility.Domain != "" {
			set[e.SourceCredibility.Domain] = struct{}{}
		}
	}
	return len(set)
}

func averageCredibility(ev []types.Evidence) float64 {
	if len(ev) == 0 {
		return 0
	}
	sum := 0.0
	for _, e := range ev {
		sum += e.CredibilityScore
	}
	return sum / float64(len(ev))
}
