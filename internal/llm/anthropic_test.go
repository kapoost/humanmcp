package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewAvailableFalseWithoutKey(t *testing.T) {
	c := New("")
	if c.Available() {
		t.Error("empty key should be unavailable")
	}
	c2 := New("sk-real")
	if !c2.Available() {
		t.Error("real key should be available")
	}
}

func TestCompleteSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-test" {
			t.Errorf("bad api key header: %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Error("missing anthropic-version header")
		}
		var body apiRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model == "" || len(body.Messages) == 0 {
			t.Errorf("bad body: %+v", body)
		}
		_ = json.NewEncoder(w).Encode(apiResponse{
			Content: []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{{Type: "text", Text: "Persona odpowiada."}},
			Usage: struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			}{InputTokens: 100, OutputTokens: 20},
		})
	}))
	defer srv.Close()

	c := &Client{APIKey: "sk-test", HTTPClient: srv.Client()}
	c.HTTPClient.Timeout = 5 * time.Second
	// Point client at test server by swapping the endpoint via a wrapper.
	c.Endpoint = srv.URL
	res, err := c.Complete(context.Background(), CompleteRequest{Model: ModelSonnet46, User: "hi"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Text != "Persona odpowiada." {
		t.Errorf("text: %q", res.Text)
	}
	if res.InputTokens != 100 || res.OutputTokens != 20 {
		t.Errorf("tokens: %+v", res)
	}
}

func TestCompleteRetriesOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n := atomic.AddInt32(&calls, 1); n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"type":"rate_limit","message":"slow down"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(apiResponse{
			Content: []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{{Type: "text", Text: "ok"}},
		})
	}))
	defer srv.Close()

	c := &Client{APIKey: "sk-test", HTTPClient: srv.Client()}
	c.HTTPClient.Timeout = 5 * time.Second
	c.Endpoint = srv.URL
	res, err := c.Complete(context.Background(), CompleteRequest{Model: ModelHaiku45, User: "hi"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Text != "ok" {
		t.Errorf("expected retry success, got %q", res.Text)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

func TestCompleteFailsOn400WithoutRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"bad_request","message":"invalid model"}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "sk-test", HTTPClient: srv.Client(), Endpoint: srv.URL}
	_, err := c.Complete(context.Background(), CompleteRequest{Model: "bogus", User: "hi"})
	if err == nil {
		t.Fatal("expected 400 error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected 400 in error, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("expected 1 call (no retry on 400), got %d", calls)
	}
}

func TestCompleteRejectsMissingKey(t *testing.T) {
	c := New("")
	_, err := c.Complete(context.Background(), CompleteRequest{Model: ModelSonnet46, User: "hi"})
	if err == nil {
		t.Error("expected error when key is empty")
	}
}
