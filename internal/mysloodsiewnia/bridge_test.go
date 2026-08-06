package mysloodsiewnia

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestBridge(t *testing.T, token string) (*Bridge, *httptest.Server) {
	t.Helper()
	b := NewBridge(New(), NewQueue(), token)
	mux := http.NewServeMux()
	b.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return b, srv
}

func TestBridgeHeartbeatRequiresAuth(t *testing.T) {
	_, srv := newTestBridge(t, "sekret")
	body, _ := json.Marshal(heartbeatBody{CommitSHA: "abc", FTSHealthy: true})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mysloodsiewnia/heartbeat", bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no bearer ⇒ want 401, got %d", resp.StatusCode)
	}
}

func TestBridgeHeartbeatAcceptedUpdatesLiveness(t *testing.T) {
	b, srv := newTestBridge(t, "sekret")
	payload := heartbeatBody{
		CommitSHA:         "abc123",
		PersonasUpdatedAt: time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC),
		SkillsUpdatedAt:   time.Date(2026, 8, 5, 11, 0, 0, 0, time.UTC),
		FTSHealthy:        true,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mysloodsiewnia/heartbeat", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sekret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("heartbeat status = %d, want 200", resp.StatusCode)
	}
	snap := b.Liveness.Get()
	if snap.Status != StatusOnline {
		t.Fatalf("liveness status after heartbeat = %q, want %q", snap.Status, StatusOnline)
	}
	if snap.CommitSHA != "abc123" {
		t.Fatalf("commit sha not persisted: got %q", snap.CommitSHA)
	}
}

func TestBridgeDisabledWhenTokenEmpty(t *testing.T) {
	_, srv := newTestBridge(t, "")
	resp, err := http.Get(srv.URL + "/mysloodsiewnia/pending-ops")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("empty token ⇒ want 503, got %d", resp.StatusCode)
	}
}

func TestBridgeConstantTimeAuth(t *testing.T) {
	_, srv := newTestBridge(t, "sekret-długi")
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mysloodsiewnia/pending-ops", nil)
	req.Header.Set("Authorization", "Bearer wrong-token-different-length")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token ⇒ want 401, got %d", resp.StatusCode)
	}
}

func TestBridgePendingOpsAndComplete(t *testing.T) {
	b, srv := newTestBridge(t, "s")
	// Enqueue an op as if a tool did it.
	op := b.Queue.Enqueue(OpSearch, json.RawMessage(`{"query":"morze"}`))

	// Vault polls pending-ops.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mysloodsiewnia/pending-ops", nil)
	req.Header.Set("Authorization", "Bearer s")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pending-ops = %d", resp.StatusCode)
	}
	var pendResp struct {
		Ops []struct {
			ID   string `json:"op_id"`
			Kind string `json:"kind"`
		} `json:"ops"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pendResp); err != nil {
		t.Fatal(err)
	}
	if len(pendResp.Ops) != 1 || pendResp.Ops[0].ID != op.ID {
		t.Fatalf("pending-ops returned %+v, want the enqueued op %q", pendResp.Ops, op.ID)
	}

	// Vault completes the op.
	waitDone := make(chan struct{})
	go func() {
		completed, ok := b.Queue.WaitFor(op.ID, 1*time.Second)
		if !ok {
			t.Errorf("WaitFor timed out")
		}
		if completed.State != OpApplied {
			t.Errorf("state = %q, want applied", completed.State)
		}
		if !strings.Contains(string(completed.Result), "morze") {
			t.Errorf("result didn't round-trip: %s", completed.Result)
		}
		close(waitDone)
	}()

	completeBodyPayload := completeBody{
		OpID:   op.ID,
		Status: OpApplied,
		Result: json.RawMessage(`{"echo":"morze"}`),
	}
	body, _ := json.Marshal(completeBodyPayload)
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/mysloodsiewnia/complete", bytes.NewReader(body))
	req2.Header.Set("Authorization", "Bearer s")
	resp2, _ := http.DefaultClient.Do(req2)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("complete = %d", resp2.StatusCode)
	}
	<-waitDone
}

func TestBridgeCompleteDoubleReturnsConflict(t *testing.T) {
	b, srv := newTestBridge(t, "s")
	op := b.Queue.Enqueue(OpStatus, nil)

	post := func() int {
		body, _ := json.Marshal(completeBody{OpID: op.ID, Status: OpApplied})
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mysloodsiewnia/complete", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer s")
		resp, _ := http.DefaultClient.Do(req)
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := post(); got != http.StatusOK {
		t.Fatalf("first complete = %d", got)
	}
	if got := post(); got != http.StatusConflict {
		t.Fatalf("double complete = %d, want 409", got)
	}
}
