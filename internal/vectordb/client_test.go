package vectordb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestValidateDimension(t *testing.T) {
	// Correct dim passes
	err := validateDimension(3, [][]float64{{1, 2, 3}, {4, 5, 6}})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Wrong dim fails
	err = validateDimension(3, [][]float64{{1, 2, 3}, {4, 5}})
	if err == nil {
		t.Fatal("expected error for wrong dimension, got nil")
	}
}

func TestCircuitBreakerOpensAfterFailures(t *testing.T) {
	cb := newCircuitBreaker(3, 30*time.Second)

	// First three failures
	for i := 0; i < 3; i++ {
		cb.recordFailure()
	}

	if cb.allow() {
		t.Fatal("expected circuit breaker to be open after 3 failures")
	}
}

func TestCircuitBreakerHalfOpen(t *testing.T) {
	cb := newCircuitBreaker(3, 50*time.Millisecond)

	// Trip the breaker
	cb.recordFailure()
	cb.recordFailure()
	cb.recordFailure()

	if cb.allow() {
		t.Fatal("expected circuit breaker to be open immediately after failures")
	}

	// Wait for the open timeout
	time.Sleep(60 * time.Millisecond)

	if !cb.allow() {
		t.Fatal("expected circuit breaker to be half-open after timeout")
	}
}

func TestCircuitBreakerRecovery(t *testing.T) {
	cb := newCircuitBreaker(3, 50*time.Millisecond)

	// Trip the breaker
	cb.recordFailure()
	cb.recordFailure()
	cb.recordFailure()

	// Wait for half-open
	time.Sleep(60 * time.Millisecond)

	if !cb.allow() {
		t.Fatal("expected circuit breaker to be half-open after timeout")
	}

	// Success in half-open -> closed
	cb.recordSuccess()

	if !cb.allow() {
		t.Fatal("expected circuit breaker to be closed after recovery")
	}
}

func TestClientHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Errorf("expected /, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"title":"qdrant","version":"1.12.0"}`)
	}))
	defer server.Close()

	client, err := NewClient(Config{
		Host:    "localhost",
		Port:    0, // will be overridden below
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Override baseURL to use the test server
	client.baseURL = server.URL
	client.client = server.Client()

	version, err := client.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.12.0" {
		t.Fatalf("expected version 1.12.0, got %s", version)
	}
}

func TestClientUpsert(t *testing.T) {
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/collections/test_coll/points" {
			t.Errorf("expected /collections/test_coll/points, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("wait") != "true" {
			t.Errorf("expected wait=true, got %s", r.URL.Query().Get("wait"))
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"result":{"operation_id":1,"status":"completed"}}`)
	}))
	defer server.Close()

	client, err := NewClient(Config{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	client.baseURL = server.URL
	client.client = server.Client()

	points := []VectorPoint{
		{ID: "1", Vector: []float64{0.1, 0.2, 0.3}, Payload: map[string]interface{}{"text": "hello"}},
	}
	err = client.Upsert(context.Background(), "test_coll", points)
	if err != nil {
		t.Fatal(err)
	}

	pts, ok := gotBody["points"].([]interface{})
	if !ok || len(pts) != 1 {
		t.Fatalf("expected 1 point, got %v", gotBody)
	}
}

func TestClientSearch(t *testing.T) {
	expectedResults := []SearchResult{
		{ID: "1", Score: 0.95, Payload: map[string]interface{}{"text": "result1"}},
		{ID: "2", Score: 0.85, Payload: map[string]interface{}{"text": "result2"}},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/collections/test_coll/points/search" {
			t.Errorf("expected /collections/test_coll/points/search, got %s", r.URL.Path)
		}

		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}

		if body["limit"] != float64(5) {
			t.Errorf("expected limit 5, got %v", body["limit"])
		}
		if body["score_threshold"] != 0.7 {
			t.Errorf("expected score_threshold 0.7, got %v", body["score_threshold"])
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{"result": expectedResults}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	client.baseURL = server.URL
	client.client = server.Client()

	results, err := client.Search(context.Background(), "test_coll", []float64{0.1, 0.2, 0.3}, SearchOptions{TopK: 5, Threshold: 0.7})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != "1" || results[0].Score != 0.95 {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
}

func TestClientEnsureCollectionExists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Collection already exists
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"result":{"status":"green"}}`)
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	client, err := NewClient(Config{Timeout: 5 * time.Second, ExpectedDim: 1536})
	if err != nil {
		t.Fatal(err)
	}
	client.baseURL = server.URL
	client.client = server.Client()

	err = client.EnsureCollection(context.Background(), "existing_coll")
	if err != nil {
		t.Fatal(err)
	}
}

func TestClientEnsureCollectionCreates(t *testing.T) {
	var createdBody map[string]interface{}
	checked := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Collection does not exist
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"status":{"error":"Not found"}}`)
			checked = true
			return
		}
		if r.Method == http.MethodPut {
			if err := json.NewDecoder(r.Body).Decode(&createdBody); err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"result":{"operation_id":1}}`)
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	client, err := NewClient(Config{Timeout: 5 * time.Second, ExpectedDim: 1536})
	if err != nil {
		t.Fatal(err)
	}
	client.baseURL = server.URL
	client.client = server.Client()

	err = client.EnsureCollection(context.Background(), "new_coll")
	if err != nil {
		t.Fatal(err)
	}

	if !checked {
		t.Fatal("expected GET check for collection existence")
	}

	vectors, ok := createdBody["vectors"].(map[string]interface{})
	if !ok {
		t.Fatal("expected vectors in create payload")
	}
	if vectors["size"] != float64(1536) {
		t.Fatalf("expected size 1536, got %v", vectors["size"])
	}
	if vectors["distance"] != "Cosine" {
		t.Fatalf("expected distance Cosine, got %v", vectors["distance"])
	}
}

func TestNewClientDefaults(t *testing.T) {
	client, err := NewClient(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if client.config.Host != "localhost" {
		t.Errorf("expected localhost, got %s", client.config.Host)
	}
	if client.config.Port != 6333 {
		t.Errorf("expected 6333, got %d", client.config.Port)
	}
	if client.config.Scheme != "http" {
		t.Errorf("expected http, got %s", client.config.Scheme)
	}
	if client.config.Timeout != 10*time.Second {
		t.Errorf("expected 10s timeout, got %v", client.config.Timeout)
	}
	if client.config.ExpectedDim != 1536 {
		t.Errorf("expected 1536 dim, got %d", client.config.ExpectedDim)
	}
	if client.config.MaxRetries != 1 {
		t.Errorf("expected 1 max retry, got %d", client.config.MaxRetries)
	}
	if client.config.Collections.TaskEmbeddings != "task_embeddings" {
		t.Errorf("expected task_embeddings, got %s", client.config.Collections.TaskEmbeddings)
	}
	if client.config.Collections.Summaries != "summaries" {
		t.Errorf("expected summaries, got %s", client.config.Collections.Summaries)
	}
	if client.config.Collections.DecompPatterns != "decomposition_patterns" {
		t.Errorf("expected decomposition_patterns, got %s", client.config.Collections.DecompPatterns)
	}
}
