// Package llm wraps the Anthropic Messages API for the humanMCP narada
// worker. Intentionally minimal — one Complete call, one retry on transient
// errors, one timeout. If we grow more callers we split it, but for now a
// personal server calling one API doesn't need an abstraction layer.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// ModelSonnet46 — main persona voice generation.
	ModelSonnet46 = "claude-sonnet-4-6"
	// ModelHaiku45 — cheap summariser for persona journals.
	ModelHaiku45 = "claude-haiku-4-5-20251001"

	apiEndpoint      = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
	defaultTimeout   = 60 * time.Second
)

// Client holds the API key and an HTTP client. Zero-value Client is unusable;
// use New to construct.
type Client struct {
	APIKey     string
	Endpoint   string // override for tests; defaults to apiEndpoint
	HTTPClient *http.Client
}

// New returns a Client with a sensible default timeout. Pass an empty key to
// signal "no LLM available" — callers should fall back to stub behaviour
// rather than fail when the key is missing.
func New(apiKey string) *Client {
	return &Client{
		APIKey:     apiKey,
		Endpoint:   apiEndpoint,
		HTTPClient: &http.Client{Timeout: defaultTimeout},
	}
}

// Available reports whether the client is configured to make real API calls.
func (c *Client) Available() bool {
	return c != nil && strings.TrimSpace(c.APIKey) != ""
}

// CompleteRequest describes one Messages API call.
type CompleteRequest struct {
	Model     string
	System    string
	User      string
	MaxTokens int
}

// CompleteResult carries the response text plus token accounting the caller
// might want to log for cost tracking.
type CompleteResult struct {
	Text         string
	InputTokens  int
	OutputTokens int
}

type apiRequest struct {
	Model     string       `json:"model"`
	System    string       `json:"system,omitempty"`
	Messages  []apiMessage `json:"messages"`
	MaxTokens int          `json:"max_tokens"`
}

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Complete issues one Messages call. On transient failure (429, 500-599, or
// network) it retries once after a short backoff. Anything else surfaces as
// an error immediately so the caller can decide.
func (c *Client) Complete(ctx context.Context, req CompleteRequest) (CompleteResult, error) {
	if !c.Available() {
		return CompleteResult{}, fmt.Errorf("no API key configured")
	}
	if req.Model == "" {
		return CompleteResult{}, fmt.Errorf("model is required")
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 1024
	}

	body, err := json.Marshal(apiRequest{
		Model:     req.Model,
		System:    req.System,
		Messages:  []apiMessage{{Role: "user", Content: req.User}},
		MaxTokens: req.MaxTokens,
	})
	if err != nil {
		return CompleteResult{}, err
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return CompleteResult{}, ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
		endpoint := c.Endpoint
		if endpoint == "" {
			endpoint = apiEndpoint
		}
		result, retryable, err := c.doOnce(ctx, endpoint, body)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !retryable {
			return CompleteResult{}, err
		}
	}
	return CompleteResult{}, lastErr
}

func (c *Client) doOnce(ctx context.Context, endpoint string, body []byte) (CompleteResult, bool, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return CompleteResult{}, false, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.APIKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return CompleteResult{}, true, err // network hiccup — retry once
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return CompleteResult{}, true, err
	}
	if resp.StatusCode == 429 || (resp.StatusCode >= 500 && resp.StatusCode <= 599) {
		return CompleteResult{}, true, fmt.Errorf("anthropic %d: %s", resp.StatusCode, snippet(raw))
	}
	if resp.StatusCode >= 400 {
		return CompleteResult{}, false, fmt.Errorf("anthropic %d: %s", resp.StatusCode, snippet(raw))
	}

	var out apiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return CompleteResult{}, false, err
	}
	if out.Error != nil {
		return CompleteResult{}, false, fmt.Errorf("anthropic %s: %s", out.Error.Type, out.Error.Message)
	}
	var text strings.Builder
	for _, block := range out.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	return CompleteResult{
		Text:         strings.TrimSpace(text.String()),
		InputTokens:  out.Usage.InputTokens,
		OutputTokens: out.Usage.OutputTokens,
	}, false, nil
}

func snippet(raw []byte) string {
	s := string(raw)
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
