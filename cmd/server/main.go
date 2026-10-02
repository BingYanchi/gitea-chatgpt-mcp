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

	"github.com/BingYanchi/gitea-chatgpt-mcp/internal/auth"
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

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte("{\"ok\":true}"))
	})

	switch cfg.AuthMode {
	case "oauth":
		bridge, err := auth.NewBridge(auth.BridgeConfig{
			PublicBaseURL:     cfg.PublicBaseURL,
			GiteaBaseURL:      cfg.GiteaBaseURL,
			MCPPath:           cfg.MCPPath,
			GiteaClientID:     cfg.GiteaOAuthClientID,
			GiteaClientSecret: cfg.GiteaOAuthClientSecret,
			GiteaScopes:       cfg.GiteaOAuthScopes,
			EncryptionKey:     cfg.OAuthEncryptionKey,
			HTTPClient:        &http.Client{Timeout: cfg.RequestTimeout},
		})
		if err != nil {
			log.Fatal(err)
		}
		bridge.RegisterRoutes(mux)
		resourceMetadataURL := cfg.PublicBaseURL + "/.well-known/oauth-protected-resource" + cfg.MCPPath

		fallbackClient, err := gitea.NewOAuthClient(cfg.GiteaBaseURL, "unauthenticated", cfg.RequestTimeout)
		if err != nil {
			log.Fatal(err)
		}
		mcpHandler := mcp.NewStreamableHTTPHandler(
			func(r *http.Request) *mcp.Server {
				token, ok := auth.GiteaAccessTokenFromContext(r.Context())
				if !ok {
					return mcpserver.NewOAuth(fallbackClient, resourceMetadataURL)
				}
				client, err := gitea.NewOAuthClient(cfg.GiteaBaseURL, token, cfg.RequestTimeout)
				if err != nil {
					log.Printf("create per-user Gitea client: %v", err)
					return mcpserver.NewOAuth(fallbackClient, resourceMetadataURL)
				}
				return mcpserver.NewOAuth(client, resourceMetadataURL)
			},
			&mcp.StreamableHTTPOptions{
				JSONResponse: true,
				Stateless:    true,
			},
		)
		mux.Handle(cfg.MCPPath, bridge.RequireAuth(mcpserver.MirrorSecuritySchemes(mcpHandler)))
		log.Printf("OAuth enabled: issuer=%s, Gitea=%s", cfg.PublicBaseURL, cfg.GiteaBaseURL)

	case "token":
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
		mux.Handle(cfg.MCPPath, handler)
	}

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("Gitea MCP listening on %s%s (auth=%s)", cfg.ListenAddr, cfg.MCPPath, cfg.AuthMode)
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
