package main

import (
	"log"

	"cribug/internal/activities"
	"cribug/internal/config"
	"cribug/internal/db"
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
	budgetActivities := activities.NewBudgetActivities(cfg.LLMServiceURL)
	usageActivities := activities.NewUsageActivities(dbClient.Stdlib())
	dagActivities := activities.NewDAGActivities(dbClient.Stdlib(), cfg.LLMServiceURL, cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB, cfg.DAGTTLSeconds)
	multiAgentActivities := activities.NewMultiAgentActivities(cfg.LLMServiceURL)
	reactActivities := activities.NewReActActivities(cfg.LLMServiceURL, cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB)

	w := worker.New(temporalClient, cfg.TemporalTaskQueue, worker.Options{})

	sw := workflows.NewSimpleWorkflow()
	w.RegisterWorkflowWithOptions(sw.Execute, workflow.RegisterOptions{Name: workflows.WorkflowName})

	dw := workflows.NewDAGWorkflow()
	w.RegisterWorkflowWithOptions(dw.Execute, workflow.RegisterOptions{Name: workflows.DAGWorkflowName})

	mw := workflows.NewMultiAgentWorkflow()
	w.RegisterWorkflowWithOptions(mw.Execute, workflow.RegisterOptions{Name: workflows.MultiAgentWorkflowName})

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

	// Phase 3D: ReAct Activity
	w.RegisterActivityWithOptions(reactActivities.ExecuteReActNode, activity.RegisterOptions{Name: "ExecuteReActNodeActivity"})

	// Phase 3C: Tool Activities
	toolActivities := activities.NewToolActivities()
	w.RegisterActivityWithOptions(toolActivities.ExecuteTool, activity.RegisterOptions{Name: "ExecuteToolActivity"})

	log.Printf("worker started, task_queue=%s", cfg.TemporalTaskQueue)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("worker error: %v", err)
	}
}