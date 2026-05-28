package embeddings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mockEmbedServer creates a test HTTP server that responds to /embed requests.
// It returns vectors of the given dimension for each text in the request.
func mockEmbedServer(t *testing.T, dim int) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/embed", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var req embedRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)

		vectors := make([][]float64, len(req.Texts))
		for i := range req.Texts {
			vec := make([]float64, dim)
			for j := range vec {
				vec[j] = float64(i*1000 + j)
			}
			vectors[i] = vec
		}

		resp := embedResponse{
			Vectors:  vectors,
			Model:    req.Model,
			Provider: "test-provider",
			Usage: &EmbeddingUsage{
				PromptTokens: len(req.Texts) * 10,
				TotalTokens:  len(req.Texts) * 10,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(resp)
		require.NoError(t, err)
	}))
}

// mockErrorServer returns the given status code for every request.
func mockErrorServer(t *testing.T, statusCode int) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
	}))
}

func TestEmbedText(t *testing.T) {
	ts := mockEmbedServer(t, 1536)
	defer ts.Close()

	svc := NewService(Config{
		BaseURL:     ts.URL,
		ExpectedDim: 1536,
	})

	result, err := svc.EmbedText(context.Background(), "hello world", "test-model")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Vectors, 1)
	require.Len(t, result.Vectors[0], 1536)
	require.Equal(t, "test-model", result.Model)
	require.Equal(t, "test-provider", result.Provider)
	require.NotNil(t, result.Usage)
	require.Greater(t, result.DurationMs, int64(0))
	require.False(t, result.Cached)
}

func TestEmbedBatch(t *testing.T) {
	ts := mockEmbedServer(t, 1536)
	defer ts.Close()

	svc := NewService(Config{
		BaseURL:     ts.URL,
		ExpectedDim: 1536,
	})

	texts := []string{"first text", "second text", "third text"}
	result, err := svc.EmbedBatch(context.Background(), texts, "test-model")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Vectors, 3)
	for i, vec := range result.Vectors {
		require.Len(t, vec, 1536, "vector %d should have dimension 1536", i)
	}
	require.Equal(t, "test-model", result.Model)
	require.NotNil(t, result.Usage)
	require.Greater(t, result.DurationMs, int64(0))
	require.False(t, result.Cached)
}

func TestEmbedDimensionMismatch(t *testing.T) {
	ts := mockEmbedServer(t, 512)
	defer ts.Close()

	svc := NewService(Config{
		BaseURL:     ts.URL,
		ExpectedDim: 1536,
	})

	_, err := svc.EmbedText(context.Background(), "hello world", "test-model")
	require.Error(t, err)
	require.Contains(t, err.Error(), "dimension 512, expected 1536")
}

func TestEmbedCacheEnabled(t *testing.T) {
	requestCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		var req embedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		vectors := make([][]float64, len(req.Texts))
		for i := range req.Texts {
			vec := make([]float64, 1536)
			for j := range vec {
				vec[j] = float64(i*1000 + j)
			}
			vectors[i] = vec
		}

		resp := embedResponse{
			Vectors:  vectors,
			Model:    req.Model,
			Provider: "test-provider",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	svc := NewService(Config{
		BaseURL:      ts.URL,
		ExpectedDim:  1536,
		CacheEnabled: true,
		CacheMaxSize: 100,
	})

	// First call - should hit the server
	result1, err := svc.EmbedText(context.Background(), "cache me", "test-model")
	require.NoError(t, err)
	require.NotNil(t, result1)
	require.Len(t, result1.Vectors, 1)
	require.False(t, result1.Cached)
	require.Equal(t, 1, requestCount)

	// Second call with same text - should come from cache
	result2, err := svc.EmbedText(context.Background(), "cache me", "test-model")
	require.NoError(t, err)
	require.NotNil(t, result2)
	require.Len(t, result2.Vectors, 1)
	require.True(t, result2.Cached)
	// Same vectors
	for i := range result2.Vectors[0] {
		require.Equal(t, result1.Vectors[0][i], result2.Vectors[0][i])
	}
	// Should not have incremented request count
	require.Equal(t, 1, requestCount)
}

func TestEmbedEmptyInput(t *testing.T) {
	ts := mockEmbedServer(t, 1536)
	defer ts.Close()

	svc := NewService(Config{
		BaseURL:     ts.URL,
		ExpectedDim: 1536,
	})

	t.Run("empty string", func(t *testing.T) {
		_, err := svc.EmbedText(context.Background(), "", "test-model")
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty text")
	})

	t.Run("whitespace only", func(t *testing.T) {
		_, err := svc.EmbedText(context.Background(), "   ", "test-model")
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty text")
	})

	t.Run("empty batch", func(t *testing.T) {
		_, err := svc.EmbedBatch(context.Background(), []string{}, "test-model")
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty texts")
	})
}

func TestEmbedServerError(t *testing.T) {
	ts := mockErrorServer(t, 500)
	defer ts.Close()

	svc := NewService(Config{
		BaseURL:     ts.URL,
		ExpectedDim: 1536,
		MaxRetries:  0, // no retries
	})

	_, err := svc.EmbedText(context.Background(), "hello", "test-model")
	require.Error(t, err)
	require.Contains(t, err.Error(), "status 500")
}

func TestEmbedCacheNotEnabled(t *testing.T) {
	ts := mockEmbedServer(t, 1536)
	defer ts.Close()

	svc := NewService(Config{
		BaseURL:      ts.URL,
		ExpectedDim:  1536,
		CacheEnabled: false, // explicitly disabled
	})

	result, err := svc.EmbedText(context.Background(), "hello", "test-model")
	require.NoError(t, err)
	require.False(t, result.Cached)
}

func TestEmbedWithDefaultModel(t *testing.T) {
	ts := mockEmbedServer(t, 1536)
	defer ts.Close()

	svc := NewService(Config{
		BaseURL:       ts.URL,
		DefaultModel:  "default-test-model",
		ExpectedDim:   1536,
	})

	result, err := svc.EmbedText(context.Background(), "hello", "")
	require.NoError(t, err)
	require.Equal(t, "default-test-model", result.Model)
}

func TestEmbedContextTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		var req embedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		vectors := make([][]float64, 0)
		resp := embedResponse{Vectors: vectors, Model: req.Model, Provider: "test"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	svc := NewService(Config{
		BaseURL:     ts.URL,
		ExpectedDim: 1536,
		MaxRetries:  0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := svc.EmbedText(ctx, "hello", "test-model")
	require.Error(t, err)
}
