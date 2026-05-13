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

	taskActivities := activities.NewTaskActivities(dbClient.Stdlib(), redisClient)
	execActivities := activities.NewExecutionActivities(dbClient.Stdlib())
	emitEventActivity := activities.NewEmitEventActivity(redisClient)

	w := worker.New(temporalClient, cfg.TemporalTaskQueue, worker.Options{})

	sw := workflows.NewSimpleWorkflow()
	w.RegisterWorkflowWithOptions(sw.Execute, workflow.RegisterOptions{Name: workflows.WorkflowName})

	w.RegisterActivityWithOptions(emitEventActivity.Execute, activity.RegisterOptions{Name: "EmitEventActivity"})
	w.RegisterActivityWithOptions(taskActivities.SaveResult, activity.RegisterOptions{Name: "SaveResultActivity"})
	w.RegisterActivityWithOptions(taskActivities.SaveFailure, activity.RegisterOptions{Name: "SaveFailureActivity"})
	w.RegisterActivityWithOptions(execActivities.RecordCompleted, activity.RegisterOptions{Name: "RecordExecutionCompletedActivity"})
	w.RegisterActivityWithOptions(execActivities.RecordFailed, activity.RegisterOptions{Name: "RecordExecutionFailedActivity"})

	log.Printf("worker started, task_queue=%s", cfg.TemporalTaskQueue)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("worker error: %v", err)
	}
}