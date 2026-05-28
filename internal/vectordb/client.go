package vectordb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	config  Config
	baseURL string
	client  *http.Client
	cb      *circuitBreaker
}

func NewClient(config Config) (*Client, error) {
	if config.Host == "" {
		config.Host = "localhost"
	}
	if config.Port == 0 {
		config.Port = 6333
	}
	if config.Scheme == "" {
		config.Scheme = "http"
	}
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}
	if config.ExpectedDim == 0 {
		config.ExpectedDim = 1536
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 1
	}
	if config.Collections.TaskEmbeddings == "" {
		config.Collections.TaskEmbeddings = "task_embeddings"
	}
	if config.Collections.Summaries == "" {
		config.Collections.Summaries = "summaries"
	}
	if config.Collections.DecompPatterns == "" {
		config.Collections.DecompPatterns = "decomposition_patterns"
	}

	cbMaxFailures := config.CBMaxFailures
	if cbMaxFailures == 0 {
		cbMaxFailures = 3
	}
	cbTimeout := config.CBTimeout
	if cbTimeout == 0 {
		cbTimeout = 30 * time.Second
	}

	baseURL := fmt.Sprintf("%s://%s:%d", config.Scheme, config.Host, config.Port)

	return &Client{
		config:  config,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: config.Timeout,
		},
		cb: newCircuitBreaker(cbMaxFailures, cbTimeout),
	}, nil
}

func (c *Client) Health(ctx context.Context) (string, error) {
	var version string
	err := c.cb.do(ctx, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/", nil)
		if err != nil {
			return fmt.Errorf("health request: %w", err)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			return fmt.Errorf("health request failed: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("health read body: %w", err)
		}

		var result struct {
			Title   string `json:"title"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return fmt.Errorf("health decode: %w", err)
		}
		version = result.Version
		return nil
	})
	if err != nil {
		return "", err
	}
	return version, nil
}

func (c *Client) Upsert(ctx context.Context, collection string, points []VectorPoint) error {
	return c.cb.do(ctx, func() error {
		bodyPayload := map[string]interface{}{
			"points": points,
		}
		bodyBytes, err := json.Marshal(bodyPayload)
		if err != nil {
			return fmt.Errorf("upsert marshal: %w", err)
		}

		u, _ := url.Parse(c.baseURL + "/collections/" + url.PathEscape(collection) + "/points")
		q := u.Query()
		q.Set("wait", "true")
		u.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("upsert request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.client.Do(req)
		if err != nil {
			return fmt.Errorf("upsert request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("upsert failed with status %d: %s", resp.StatusCode, string(body))
		}

		return nil
	})
}

func (c *Client) Search(ctx context.Context, collection string, vector []float64, opts SearchOptions) ([]SearchResult, error) {
	if opts.TopK <= 0 {
		opts.TopK = 5
	}
	if opts.Threshold <= 0 {
		opts.Threshold = 0.7
	}

	var results []SearchResult
	err := c.cb.do(ctx, func() error {
		bodyPayload := map[string]interface{}{
			"vector":          vector,
			"limit":           opts.TopK,
			"score_threshold": opts.Threshold,
		}
		bodyBytes, err := json.Marshal(bodyPayload)
		if err != nil {
			return fmt.Errorf("search marshal: %w", err)
		}

		u := c.baseURL + "/collections/" + url.PathEscape(collection) + "/points/search"

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("search request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.client.Do(req)
		if err != nil {
			return fmt.Errorf("search request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("search failed with status %d: %s", resp.StatusCode, string(body))
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("search read body: %w", err)
		}

		var searchResp struct {
			Result []SearchResult `json:"result"`
		}
		if err := json.Unmarshal(body, &searchResp); err != nil {
			return fmt.Errorf("search decode: %w", err)
		}
		results = searchResp.Result
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (c *Client) EnsureCollection(ctx context.Context, collection string) error {
	return c.cb.do(ctx, func() error {
		// Check if collection exists
		checkURL := c.baseURL + "/collections/" + url.PathEscape(collection)
		checkReq, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
		if err != nil {
			return fmt.Errorf("ensure collection check request: %w", err)
		}

		checkResp, err := c.client.Do(checkReq)
		if err != nil {
			return fmt.Errorf("ensure collection check failed: %w", err)
		}
		checkResp.Body.Close()

		if checkResp.StatusCode == http.StatusOK {
			// Collection already exists
			return nil
		}

		// Create collection
		createPayload := map[string]interface{}{
			"vectors": map[string]interface{}{
				"size":     c.config.ExpectedDim,
				"distance": "Cosine",
			},
		}
		bodyBytes, err := json.Marshal(createPayload)
		if err != nil {
			return fmt.Errorf("ensure collection marshal: %w", err)
		}

		createReq, err := http.NewRequestWithContext(ctx, http.MethodPut, checkURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("ensure collection create request: %w", err)
		}
		createReq.Header.Set("Content-Type", "application/json")

		createResp, err := c.client.Do(createReq)
		if err != nil {
			return fmt.Errorf("ensure collection create failed: %w", err)
		}
		defer createResp.Body.Close()

		if createResp.StatusCode >= 400 {
			body, _ := io.ReadAll(createResp.Body)
			return fmt.Errorf("ensure collection create failed with status %d: %s", createResp.StatusCode, string(body))
		}

		return nil
	})
}
