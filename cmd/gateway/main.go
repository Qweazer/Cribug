package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cribug/internal/api"
	"cribug/internal/config"
	"cribug/internal/db"
	redisclient "cribug/internal/redis"

	"go.temporal.io/sdk/client"
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

	h := api.NewHandler(dbClient, redisClient, temporalClient)
	r := api.NewRouter(h)

	srv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("gateway listening on %s", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("server forced to shutdown: %v", err)
	}

	log.Println("server stopped")
}