// build: 2026-04-01 14:01:43 UTC
package main

import (
	"log"
	"net/http"
	"strings"
	"os"

	"github.com/kapoost/humanmcp-go/internal/auth"
	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
	"github.com/kapoost/humanmcp-go/internal/mcp"
	mcpv2 "github.com/kapoost/humanmcp-go/internal/mcp/v2"
	"github.com/kapoost/humanmcp-go/internal/mysloodsiewnia"
	"github.com/kapoost/humanmcp-go/internal/rituals"
	"github.com/kapoost/humanmcp-go/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if cfg.EditToken == "" {
		log.Println("WARNING: EDIT_TOKEN is not set — owner API is disabled")
	}

	// Ensure content directory exists
	if err := os.MkdirAll(cfg.ContentDir, 0755); err != nil {
		log.Fatalf("content dir: %v", err)
	}

	store := content.NewStore(cfg.ContentDir)
	if err := store.Load(); err != nil {
		log.Printf("initial content load: %v", err)
	}

	a := auth.New(cfg.EditToken)
	liveness := mysloodsiewnia.New()
	bridgeQueue := mysloodsiewnia.NewQueue()
	bridge := mysloodsiewnia.NewBridge(liveness, bridgeQueue, cfg.VaultBridgeToken)
	ritualWorker := rituals.New(cfg)
	ritualWorker.Start()
	mcpHandler := mcp.NewHandler(cfg, store, a, ritualWorker)
	mcpHandler.SetBridge(liveness, bridgeQueue)
	webHandler := web.NewHandler(cfg, store, a)
	webHandler.SetMCPToolCount(len(mcpHandler.ToolNames()))
	webHandler.SetLiveness(liveness)

	mux := http.NewServeMux()

	// MCP endpoint (legacy, protocolVersion 2024-11-05)
	mux.Handle("/mcp", corsMiddleware(mcpHandler))

	// MCP v2 (protocolVersion 2026-07-28, stateless Streamable HTTP via go-sdk).
	// Mounted before the /mcp/ catch-all so the v2 path wins.
	v2Handler := mcpv2.New(cfg, mcpHandler)
	mux.Handle("/mcp/v2", corsMiddleware(v2Handler))
	mux.Handle("/mcp/v2/", corsMiddleware(v2Handler))
	mux.Handle("/mcp/", corsMiddleware(mcpHandler))

	// Web UI + REST API
	webHandler.RegisterRoutes(mux)

	// Bridge endpoints for the mysłoodsiewnia vault pull worker.
	// Auth: Bearer VAULT_BRIDGE_TOKEN. Empty token ⇒ every endpoint 503.
	bridge.Register(mux)

	addr := cfg.Host + ":" + cfg.Port
	log.Printf("humanMCP starting on %s", addr)
	log.Printf("  author:  %s", cfg.AuthorName)
	log.Printf("  domain:  %s", cfg.Domain)
	log.Printf("  content: %s", cfg.ContentDir)
	log.Printf("  mcp:     http://%s/mcp", addr)
	log.Printf("  mcp v2:  http://%s/mcp/v2 (protocol 2026-07-28, stateless)", addr)

	if err := http.ListenAndServe(addr, secureMiddleware(mux)); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Edit-Token")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// secureMiddleware adds security + cache headers to all responses
func secureMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Security headers
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Cache: MCP endpoints never cache, HTML pages short TTL
		if r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/mcp/") || strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
