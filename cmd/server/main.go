package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BingYanchi/gitea-chatgpt-mcp/internal/config"
	"github.com/BingYanchi/gitea-chatgpt-mcp/internal/gitea"
	mcpserver "github.com/BingYanchi/gitea-chatgpt-mcp/internal/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	client, err := gitea.NewClient(cfg.GiteaBaseURL, cfg.GiteaToken, cfg.RequestTimeout)
	if err != nil {
		log.Fatal(err)
	}

	server := mcpserver.New(client)
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{
			JSONResponse: true,
			Stateless:    true,
		},
	)

	mux := http.NewServeMux()
	mux.Handle(cfg.MCPPath, handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte("{\"ok\":true}"))
	})

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("Gitea MCP listening on %s%s", cfg.ListenAddr, cfg.MCPPath)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server: %v", err)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
