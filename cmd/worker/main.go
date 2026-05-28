package main

import (
	"log"

	"cribug/internal/activities"
	"cribug/internal/config"
	"cribug/internal/db"
	"cribug/internal/hooks"
	redisclient "cribug/internal/redis"
	"cribug/internal/workflows"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
	"go.temporal.io/sdk/worker"
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

	log.Printf("worker started, task_queue=%s", cfg.TemporalTaskQueue)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("worker error: %v", err)
	}
}