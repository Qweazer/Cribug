package main

import (
	"log"
	"time"

	"cribug/internal/activities"
	"cribug/internal/config"
	"cribug/internal/db"
	"cribug/internal/embeddings"
	"cribug/internal/hooks"
	redisclient "cribug/internal/redis"
	"cribug/internal/vectordb"
	"cribug/internal/workflows"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func main() {
	cfg := config.Load()

	dbClient, err := db.New(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to init db: %v", err)
	}
	defer dbClient.Close()

	redisClient, err := redisclient.New(cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB)
	if err != nil {
		log.Fatalf("failed to init redis: %v", err)
	}
	defer redisClient.Close()

	temporalClient, err := client.Dial(client.Options{
		HostPort: cfg.TemporalAddress,
	})
	if err != nil {
		log.Fatalf("failed to connect to temporal: %v", err)
	}
	defer temporalClient.Close()

	// Initialize all activities
	taskActivities := activities.NewTaskActivities(dbClient.Stdlib(), redisClient)
	execActivities := activities.NewExecutionActivities(dbClient.Stdlib())
	emitEventActivity := activities.NewEmitEventActivity(redisClient)
	agentActivities := activities.NewAgentActivities(cfg.LLMServiceURL)
	sessionActivities := activities.NewSessionActivities(redisClient)
	budgetActivities := activities.NewBudgetActivities(cfg.LLMServiceURL, redisClient)
	usageActivities := activities.NewUsageActivities(dbClient.Stdlib())
	dagActivities := activities.NewDAGActivities(dbClient.Stdlib(), cfg.LLMServiceURL, cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB, cfg.DAGTTLSeconds)
	multiAgentActivities := activities.NewMultiAgentActivities(cfg.LLMServiceURL)
	reactActivities := activities.NewReActActivities(cfg.LLMServiceURL, cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB)

	// Phase 6D: Hooks Event System
	hookRuntimeConfig := hooks.RuntimeConfig{
		HooksEnabled:          cfg.EnableHooks,
		BlockingEnabled:       cfg.HooksBlockingEnabled,
		BlockingFailClosed:    cfg.HookBlockingFailClosed,
		HandlerTimeoutSec:     cfg.HookHandlerTimeoutSec,
		HandlerMaxResultBytes: cfg.HookHandlerMaxResultBytes,
		AllowedInternalHn:     cfg.HookAllowedInternalHandlers,
		AllowedHTTPHosts:      cfg.HookAllowedHTTPHosts,
	}
	hookRuntime := hooks.NewHookRuntime(dbClient, hookRuntimeConfig)
	hookActivities := hooks.NewHookActivities(hookRuntime)

	// Inject hook runtime into LLM-calling activities for inline hooks
	agentActivities.SetHookRuntime(hookRuntime)

	w := worker.New(temporalClient, cfg.TemporalTaskQueue, worker.Options{})

	sw := workflows.NewSimpleWorkflow()
	w.RegisterWorkflowWithOptions(sw.Execute, workflow.RegisterOptions{Name: workflows.WorkflowName})

	dw := workflows.NewDAGWorkflow()
	w.RegisterWorkflowWithOptions(dw.Execute, workflow.RegisterOptions{Name: workflows.DAGWorkflowName})

	mw := workflows.NewMultiAgentWorkflow()
	w.RegisterWorkflowWithOptions(mw.Execute, workflow.RegisterOptions{Name: workflows.MultiAgentWorkflowName})

	swarmWf := workflows.NewSwarmWorkflow()
	w.RegisterWorkflowWithOptions(swarmWf.Execute, workflow.RegisterOptions{Name: workflows.SwarmWorkflowName})

	// Register all activities
	w.RegisterActivityWithOptions(emitEventActivity.Execute, activity.RegisterOptions{Name: "EmitEventActivity"})
	w.RegisterActivityWithOptions(taskActivities.SaveResult, activity.RegisterOptions{Name: "SaveResultActivity"})
	w.RegisterActivityWithOptions(taskActivities.SaveFailure, activity.RegisterOptions{Name: "SaveFailureActivity"})
	w.RegisterActivityWithOptions(execActivities.RecordCompleted, activity.RegisterOptions{Name: "RecordExecutionCompletedActivity"})
	w.RegisterActivityWithOptions(execActivities.RecordFailed, activity.RegisterOptions{Name: "RecordExecutionFailedActivity"})
	w.RegisterActivityWithOptions(agentActivities.CallLLM, activity.RegisterOptions{Name: "AgentActivity"})
	w.RegisterActivityWithOptions(sessionActivities.LoadSession, activity.RegisterOptions{Name: "LoadSessionActivity"})
	w.RegisterActivityWithOptions(sessionActivities.SaveSession, activity.RegisterOptions{Name: "SaveSessionActivity"})
	w.RegisterActivityWithOptions(budgetActivities.EstimatePromptTokens, activity.RegisterOptions{Name: "EstimatePromptTokensActivity"})
	w.RegisterActivityWithOptions(budgetActivities.CheckBudget, activity.RegisterOptions{Name: "CheckBudgetActivity"})
	w.RegisterActivityWithOptions(usageActivities.RecordUsage, activity.RegisterOptions{Name: "RecordUsageActivity"})
	w.RegisterActivityWithOptions(dagActivities.ClassifyTask, activity.RegisterOptions{Name: "ClassifyTaskActivity"})
	w.RegisterActivityWithOptions(dagActivities.PlanDAG, activity.RegisterOptions{Name: "PlanDAGActivity"})
	w.RegisterActivityWithOptions(dagActivities.ExecuteDAGNode, activity.RegisterOptions{Name: "ExecuteDAGNodeActivity"})
	w.RegisterActivityWithOptions(dagActivities.RecordDAGNodeUsage, activity.RegisterOptions{Name: "RecordDAGNodeUsageActivity"})
	w.RegisterActivityWithOptions(dagActivities.Synthesis, activity.RegisterOptions{Name: "SynthesisActivity"})
	w.RegisterActivityWithOptions(multiAgentActivities.RunAgent, activity.RegisterOptions{Name: "RunAgentActivity"})
	w.RegisterActivityWithOptions(multiAgentActivities.RunResearcherAgent, activity.RegisterOptions{Name: "RunResearcherAgentActivity"})
	w.RegisterActivityWithOptions(multiAgentActivities.RunCriticAgent, activity.RegisterOptions{Name: "RunCriticAgentActivity"})
	w.RegisterActivityWithOptions(multiAgentActivities.RunSynthesizerAgent, activity.RegisterOptions{Name: "RunSynthesizerAgentActivity"})

	// Phase 3D: ReAct Activity (deprecated - replaced by Workflow-level ReactLoop in Phase 4B)
	// Kept for backward compatibility with existing tasks
	w.RegisterActivityWithOptions(reactActivities.ExecuteReActNode, activity.RegisterOptions{Name: "ExecuteReActNodeActivity"})

	// Phase 4B: ReAct Audit Activity (Workflow-level ReAct step audit to Postgres)
	reactAuditActivities := activities.NewReActAuditActivities(dbClient.Stdlib())
	w.RegisterActivityWithOptions(reactAuditActivities.SaveReActStepAudit, activity.RegisterOptions{Name: "SaveReActStepAuditActivity"})

	// Phase 6D: Hooks Event System Activity
	w.RegisterActivityWithOptions(hookActivities.EmitHookEvent, activity.RegisterOptions{Name: "EmitHookEventActivity"})

	// Phase 6C: Skills System Activities & Workflow
	skillActivities := activities.NewSkillActivities(nil, dbClient, redisClient)
	skillActivities.SetHookRuntime(hookRuntime)
	w.RegisterActivityWithOptions(skillActivities.ListSkillsActivity, activity.RegisterOptions{Name: "ListSkillsActivity"})
	w.RegisterActivityWithOptions(skillActivities.GetSkillActivity, activity.RegisterOptions{Name: "GetSkillActivity"})
	w.RegisterActivityWithOptions(skillActivities.ExecuteSkillActivity, activity.RegisterOptions{Name: "ExecuteSkillActivity"})
	w.RegisterActivityWithOptions(skillActivities.AuditSkillExecutionActivity, activity.RegisterOptions{Name: "AuditSkillExecutionActivity"})
	w.RegisterActivityWithOptions(skillActivities.WorkspaceAppendSkillResultActivity, activity.RegisterOptions{Name: "WorkspaceAppendSkillResultActivity"})
	w.RegisterWorkflowWithOptions(workflows.SkillExecutionWorkflow, workflow.RegisterOptions{Name: "SkillExecutionWorkflow"})

	// Ensure react_steps table exists
	if err := activities.EnsureReActStepTable(dbClient.Stdlib()); err != nil {
		log.Printf("[WARN] Failed to ensure react_steps table: %v", err)
	}

	// Phase 4A: DAG Visualization Activities
	dagVisualActivities := activities.NewDAGVisualActivities(cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB, cfg.DAGTTLSeconds)
	w.RegisterActivityWithOptions(dagVisualActivities.RecordDAGNodeStatus, activity.RegisterOptions{Name: "RecordDAGNodeStatus"})
	w.RegisterActivityWithOptions(dagVisualActivities.BuildDAGVisualSnapshot, activity.RegisterOptions{Name: "BuildDAGVisualSnapshotActivity"})

	// Phase 4A: ReAct Observability Activities
	reactObsActivities := activities.NewReActObservabilityActivities(cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB, cfg.DAGTTLSeconds)
	w.RegisterActivityWithOptions(reactObsActivities.RecordAgentMetrics, activity.RegisterOptions{Name: "RecordAgentMetricsActivity"})
	w.RegisterActivityWithOptions(reactObsActivities.AggregateAgentMetrics, activity.RegisterOptions{Name: "AggregateAgentMetricsActivity"})
	w.RegisterActivityWithOptions(reactObsActivities.EmitMetricsSummary, activity.RegisterOptions{Name: "EmitMetricsSummaryActivity"})

	// Phase 5A Slice 10: Swarm Workflow
	swarmActivities := activities.NewSwarmActivities(cfg.LLMServiceURL)
	w.RegisterActivityWithOptions(swarmActivities.AuthorizeTeamAction, activity.RegisterOptions{Name: "AuthorizeTeamActionActivity"})
	w.RegisterActivityWithOptions(swarmActivities.WorkerAgent, activity.RegisterOptions{Name: "WorkerAgentActivity"})

	// Phase 4D Slice 13: DAG Dynamic Replanning
	dagFallbackActivities := activities.NewDAGFallbackActivities(cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB, cfg.DAGTTLSeconds)
	w.RegisterActivityWithOptions(dagFallbackActivities.HandleDAGNodeFailure, activity.RegisterOptions{Name: "HandleDAGNodeFailureActivity"})

	// Phase 3C: Tool Activities
	toolActivities := activities.NewToolActivities()
	w.RegisterActivityWithOptions(toolActivities.ExecuteTool, activity.RegisterOptions{Name: "ExecuteToolActivity"})

	// Phase 6A Slice 17: MCP Tool Runtime
	mcpActivities := activities.NewMCPActivities(dbClient, redisClient, nil)
	w.RegisterActivityWithOptions(mcpActivities.RegisterMCPServer, activity.RegisterOptions{Name: "RegisterMCPServerActivity"})
	w.RegisterActivityWithOptions(mcpActivities.DiscoverMCPTools, activity.RegisterOptions{Name: "DiscoverMCPToolsActivity"})
	w.RegisterActivityWithOptions(mcpActivities.CallMCPTool, activity.RegisterOptions{Name: "CallMCPToolActivity"})
	w.RegisterActivityWithOptions(mcpActivities.AuditMCPToolCall, activity.RegisterOptions{Name: "AuditMCPToolCallActivity"})
	w.RegisterActivityWithOptions(mcpActivities.WorkspaceAppend, activity.RegisterOptions{Name: "WorkspaceAppendActivity"})

	mcpWorkflow := workflows.NewMCPToolCallWorkflow()
	w.RegisterWorkflowWithOptions(mcpWorkflow.Execute, workflow.RegisterOptions{Name: workflows.MCPToolCallWorkflowName})

	// Ensure MCP tables exist
	if err := activities.EnsureMCPTables(dbClient.Stdlib()); err != nil {
		log.Printf("[WARN] Failed to ensure MCP tables: %v", err)
	}

	// Phase 6B Slice 18: Sandbox Runtime
	sandboxActivities := activities.NewSandboxActivities(dbClient, redisClient, nil)
	w.RegisterActivityWithOptions(sandboxActivities.ExecuteSandbox, activity.RegisterOptions{Name: "ExecuteSandboxActivity"})
	w.RegisterActivityWithOptions(sandboxActivities.AuditSandbox, activity.RegisterOptions{Name: "AuditSandboxActivity"})
	w.RegisterActivityWithOptions(sandboxActivities.WorkspaceAppendForSandbox, activity.RegisterOptions{Name: "SandboxWorkspaceAppendActivity"})

	sandboxWf := workflows.NewSandboxWorkflow()
	w.RegisterWorkflowWithOptions(sandboxWf.Execute, workflow.RegisterOptions{Name: workflows.SandboxWorkflowName})

	if err := activities.EnsureSandboxTables(dbClient.Stdlib()); err != nil {
		log.Printf("[WARN] Failed to ensure sandbox tables: %v", err)
	}

	// Phase 7A: Advanced Strategy Router
	routerActivities := activities.NewRouterActivities(dbClient.Stdlib())
	w.RegisterActivityWithOptions(routerActivities.ClassifyTaskComplexity, activity.RegisterOptions{Name: "ClassifyTaskComplexityActivity"})
	w.RegisterActivityWithOptions(routerActivities.DetectTaskCapabilities, activity.RegisterOptions{Name: "DetectTaskCapabilitiesActivity"})
	w.RegisterActivityWithOptions(routerActivities.EvaluateRoutingPolicy, activity.RegisterOptions{Name: "EvaluateRoutingPolicyActivity"})
	w.RegisterActivityWithOptions(routerActivities.EstimateRouteCost, activity.RegisterOptions{Name: "EstimateRouteCostActivity"})
	w.RegisterActivityWithOptions(routerActivities.AuditRoutingDecision, activity.RegisterOptions{Name: "AuditRoutingDecisionActivity"})
	w.RegisterActivityWithOptions(routerActivities.EmitRoutingEvent, activity.RegisterOptions{Name: "EmitRoutingEventActivity"})
	w.RegisterActivityWithOptions(routerActivities.WriteRoutingPolicyTrace, activity.RegisterOptions{Name: "WriteRoutingPolicyTraceActivity"})
	w.RegisterActivityWithOptions(routerActivities.EvaluateApprovalPolicy, activity.RegisterOptions{Name: "EvaluateApprovalPolicyActivity"})
	w.RegisterActivityWithOptions(routerActivities.PersistRoutedExecutionResult, activity.RegisterOptions{Name: "PersistRoutedExecutionResultActivity"})
	w.RegisterActivityWithOptions(routerActivities.UpdateTaskApprovalStatus, activity.RegisterOptions{Name: "UpdateTaskApprovalStatusActivity"})
	w.RegisterWorkflowWithOptions(workflows.AdvancedRoutingWorkflow, workflow.RegisterOptions{Name: workflows.AdvancedRoutingWorkflowName})
	if err := activities.EnsureRouterTable(dbClient.Stdlib()); err != nil {
		log.Printf("[WARN] Failed to ensure routing_audit_logs table: %v", err)
	}

	// Phase 7B: HITL / Approval
	approvalActivities := activities.NewApprovalActivities(dbClient.Stdlib())
	w.RegisterActivityWithOptions(approvalActivities.RequestApproval, activity.RegisterOptions{Name: "RequestApprovalActivity"})
	w.RegisterActivityWithOptions(approvalActivities.RecordApprovalResponse, activity.RegisterOptions{Name: "RecordApprovalResponseActivity"})
	w.RegisterActivityWithOptions(approvalActivities.MarkApprovalTimeout, activity.RegisterOptions{Name: "MarkApprovalTimeoutActivity"})
	w.RegisterActivityWithOptions(approvalActivities.EmitApprovalEvent, activity.RegisterOptions{Name: "EmitApprovalEventActivity"})
	if err := activities.EnsureApprovalTables(dbClient.Stdlib()); err != nil {
		log.Printf("[WARN] Failed to ensure approval tables: %v", err)
	}

	// Phase 7C: Reflection Mode
	reflectionActivities := activities.NewReflectionActivities(cfg.LLMServiceURL)
	w.RegisterActivityWithOptions(reflectionActivities.GenerateInitialDraft, activity.RegisterOptions{Name: "GenerateInitialDraftActivity"})
	w.RegisterActivityWithOptions(reflectionActivities.EvaluateDraft, activity.RegisterOptions{Name: "EvaluateDraftActivity"})
	w.RegisterActivityWithOptions(reflectionActivities.ReviseDraft, activity.RegisterOptions{Name: "ReviseDraftActivity"})
	w.RegisterActivityWithOptions(reflectionActivities.AuditReflection, activity.RegisterOptions{Name: "AuditReflectionActivity"})
	w.RegisterActivityWithOptions(reflectionActivities.EmitReflectionEvent, activity.RegisterOptions{Name: "EmitReflectionEventActivity"})
	w.RegisterActivityWithOptions(reflectionActivities.ResolveEffectiveLLMConfig, activity.RegisterOptions{Name: "ResolveEffectiveLLMConfigActivity"})
	w.RegisterWorkflowWithOptions(workflows.ReflectionWorkflow, workflow.RegisterOptions{Name: workflows.ReflectionWorkflowName})

	// Phase 7D: Tree-of-Thoughts
	totActivities := activities.NewToTActivities(cfg.LLMServiceURL)
	w.RegisterActivityWithOptions(totActivities.GenerateThoughts, activity.RegisterOptions{Name: "GenerateThoughtsActivity"})
	w.RegisterActivityWithOptions(totActivities.ScoreThought, activity.RegisterOptions{Name: "ScoreThoughtActivity"})
	w.RegisterActivityWithOptions(totActivities.FindBestPath, activity.RegisterOptions{Name: "FindBestPathActivity"})
	w.RegisterActivityWithOptions(totActivities.SynthesizeToTResult, activity.RegisterOptions{Name: "SynthesizeToTResultActivity"})
	w.RegisterWorkflowWithOptions(workflows.TreeOfThoughtsWorkflow, workflow.RegisterOptions{Name: workflows.TreeOfThoughtsWorkflowName})

	// Phase 7E: Debate Mode (Slice 27)
	debateActivities := activities.NewDebateActivities(cfg.LLMServiceURL)
	w.RegisterActivityWithOptions(debateActivities.GenerateArguments, activity.RegisterOptions{Name: "GenerateArgumentsActivity"})
	w.RegisterActivityWithOptions(debateActivities.JudgeDebate, activity.RegisterOptions{Name: "JudgeDebateActivity"})
	w.RegisterActivityWithOptions(debateActivities.CheckConsensus, activity.RegisterOptions{Name: "CheckConsensusActivity"})
	w.RegisterActivityWithOptions(debateActivities.AuditDebate, activity.RegisterOptions{Name: "AuditDebateActivity"})
	w.RegisterWorkflowWithOptions(workflows.DebateWorkflow, workflow.RegisterOptions{Name: workflows.DebateWorkflowName})

	// Phase 7F: Research-Synthesis v2 (Slice 28)
	researchV2Activities := activities.NewResearchV2Activities(cfg.LLMServiceURL)
	w.RegisterActivityWithOptions(researchV2Activities.PlanResearch, activity.RegisterOptions{Name: "PlanResearchActivity"})
	w.RegisterActivityWithOptions(researchV2Activities.RetrieveMultiSourceEvidence, activity.RegisterOptions{Name: "RetrieveMultiSourceEvidenceActivity"})
	w.RegisterActivityWithOptions(researchV2Activities.ScoreSourceCredibility, activity.RegisterOptions{Name: "ScoreSourceCredibilityActivity"})
	w.RegisterActivityWithOptions(researchV2Activities.DetectContradictions, activity.RegisterOptions{Name: "DetectContradictionsActivity"})
	w.RegisterActivityWithOptions(researchV2Activities.BuildCitationChain, activity.RegisterOptions{Name: "BuildCitationChainActivity"})
	w.RegisterActivityWithOptions(researchV2Activities.FilterByCredibility, activity.RegisterOptions{Name: "FilterByCredibilityActivity"})
	w.RegisterActivityWithOptions(researchV2Activities.GenerateReportV2, activity.RegisterOptions{Name: "GenerateReportV2Activity"})
	w.RegisterActivityWithOptions(researchV2Activities.ReflectionBeforeSynthesis, activity.RegisterOptions{Name: "ReflectionBeforeSynthesisActivity"})
	w.RegisterActivityWithOptions(researchV2Activities.DebateBeforeSynthesis, activity.RegisterOptions{Name: "DebateBeforeSynthesisActivity"})
	w.RegisterActivityWithOptions(researchV2Activities.AuditResearchV2, activity.RegisterOptions{Name: "AuditResearchV2Activity"})
	w.RegisterWorkflowWithOptions(workflows.ResearchSynthesisV2Workflow, workflow.RegisterOptions{Name: workflows.ResearchSynthesisV2WorkflowName})

	// Phase 7G: Workspace Store Persistence
	workspaceActivities := activities.NewWorkspaceActivities(dbClient.Stdlib())
	w.RegisterActivityWithOptions(workspaceActivities.WorkspacePut, activity.RegisterOptions{Name: "WorkspacePutActivity"})

	// Phase 6E: Embeddings + Qdrant + RAG
	embedCfg := embeddings.Config{
		BaseURL:      cfg.LLMServiceURL,
		DefaultModel: cfg.EmbeddingModel,
		ExpectedDim:  cfg.EmbeddingDim,
		Timeout:      time.Duration(cfg.EmbeddingTimeoutSec) * time.Second,
		CacheEnabled: cfg.EmbeddingCacheEnabled,
		CacheMaxSize: cfg.EmbeddingCacheMaxSize,
		MaxRetries:   2,
	}
	embedSvc := embeddings.NewService(embedCfg)

	vdbCfg := vectordb.Config{
		Host:        cfg.QdrantHost,
		Port:        cfg.QdrantPort,
		Scheme:      cfg.QdrantScheme,
		Timeout:     time.Duration(cfg.QdrantTimeoutSec) * time.Second,
		ExpectedDim: cfg.EmbeddingDim,
	}
	vdbClient, err := vectordb.NewClient(vdbCfg)
	if err != nil {
		log.Printf("[WARN] Failed to create Qdrant client: %v", err)
	}

	docRepo := db.NewDocumentRepository(dbClient.Stdlib())

	// RAG retrieval activities
	ragRetrievalActivities := activities.NewRAGRetrievalActivities(embedSvc, vdbClient, docRepo)
	w.RegisterActivityWithOptions(ragRetrievalActivities.EmbedAndSearchChunksActivity, activity.RegisterOptions{Name: "EmbedAndSearchChunksActivity"})
	w.RegisterActivityWithOptions(ragRetrievalActivities.FetchChunkContentActivity, activity.RegisterOptions{Name: "FetchChunkContentActivity"})
	w.RegisterActivityWithOptions(ragRetrievalActivities.PackContextActivity, activity.RegisterOptions{Name: "PackContextActivity"})

	// Ingestion activities
	ingestionActivities := activities.NewIngestionActivities(docRepo, embedSvc, vdbClient)
	w.RegisterActivityWithOptions(ingestionActivities.SaveDocumentMetadataActivity, activity.RegisterOptions{Name: "SaveDocumentMetadataActivity"})
	w.RegisterActivityWithOptions(ingestionActivities.ChunkDocumentActivity, activity.RegisterOptions{Name: "ChunkDocumentActivity"})
	w.RegisterActivityWithOptions(ingestionActivities.EmbedAndUpsertChunksActivity, activity.RegisterOptions{Name: "EmbedAndUpsertChunksActivity"})
	w.RegisterActivityWithOptions(ingestionActivities.UpdateDocumentIndexStatusActivity, activity.RegisterOptions{Name: "UpdateDocumentIndexStatusActivity"})

	// RAG workflows
	w.RegisterWorkflowWithOptions(workflows.RAGQueryWorkflow, workflow.RegisterOptions{Name: workflows.RAGQueryWorkflowName})
	w.RegisterWorkflowWithOptions(workflows.DocumentIngestionWorkflow, workflow.RegisterOptions{Name: workflows.DocumentIngestionWorkflowName})
	w.RegisterWorkflowWithOptions(workflows.ResearchSynthesisWorkflow, workflow.RegisterOptions{Name: workflows.ResearchSynthesisWorkflowName})

	log.Printf("worker started, task_queue=%s", cfg.TemporalTaskQueue)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("worker error: %v", err)
	}
}
