package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type BridgeConfig struct {
	PublicBaseURL           string
	GiteaBaseURL            string
	MCPPath                 string
	GiteaClientID           string
	GiteaClientSecret       string
	GiteaScopes             string
	EncryptionKey           string
	HTTPClient              *http.Client
}

type Bridge struct {
	cfg       BridgeConfig
	sealer    *sealer
	http      *http.Client
	usedMu    sync.Mutex
	usedCodes map[string]time.Time
}

type contextKey string

const giteaAccessTokenKey contextKey = "gitea-access-token"

type registeredClient struct {
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	ClientName              string   `json:"client_name,omitempty"`
	IssuedAt                int64    `json:"iat"`
}

type pendingAuthorization struct {
	ClientID           string `json:"client_id"`
	RedirectURI        string `json:"redirect_uri"`
	ClientState        string `json:"client_state,omitempty"`
	CodeChallenge      string `json:"code_challenge"`
	Resource           string `json:"resource"`
	Scope              string `json:"scope"`
	GiteaCodeVerifier  string `json:"gitea_code_verifier"`
	ExpiresAt          int64  `json:"exp"`
}

type authorizationCode struct {
	ID                 string `json:"jti"`
	ClientID           string `json:"client_id"`
	RedirectURI        string `json:"redirect_uri"`
	CodeChallenge      string `json:"code_challenge"`
	Resource           string `json:"resource"`
	Scope              string `json:"scope"`
	GiteaAccessToken   string `json:"gitea_access_token"`
	GiteaRefreshToken  string `json:"gitea_refresh_token,omitempty"`
	GiteaExpiresIn     int64  `json:"gitea_expires_in,omitempty"`
	ExpiresAt          int64  `json:"exp"`
}

type accessTokenPayload struct {
	GiteaAccessToken string `json:"gitea_access_token"`
	Resource         string `json:"resource"`
	Scope            string `json:"scope"`
	IssuedAt         int64  `json:"iat"`
	ExpiresAt        int64  `json:"exp"`
}

type refreshTokenPayload struct {
	ClientID          string `json:"client_id"`
	GiteaRefreshToken string `json:"gitea_refresh_token"`
	Resource          string `json:"resource"`
	Scope             string `json:"scope"`
	IssuedAt          int64  `json:"iat"`
	ExpiresAt         int64  `json:"exp"`
}

type giteaTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func NewBridge(cfg BridgeConfig) (*Bridge, error) {
	cfg.PublicBaseURL = strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
	cfg.GiteaBaseURL = strings.TrimRight(strings.TrimSpace(cfg.GiteaBaseURL), "/")
	if cfg.PublicBaseURL == "" {
		return nil, fmt.Errorf("PUBLIC_BASE_URL is required for OAuth mode")
	}
	if _, err := parseAbsoluteURL(cfg.PublicBaseURL); err != nil {
		return nil, fmt.Errorf("PUBLIC_BASE_URL: %w", err)
	}
	if cfg.GiteaBaseURL == "" {
		return nil, fmt.Errorf("GITEA_BASE_URL is required")
	}
	if _, err := parseAbsoluteURL(cfg.GiteaBaseURL); err != nil {
		return nil, fmt.Errorf("GITEA_BASE_URL: %w", err)
	}
	if cfg.GiteaClientID == "" || cfg.GiteaClientSecret == "" {
		return nil, fmt.Errorf("GITEA_OAUTH_CLIENT_ID and GITEA_OAUTH_CLIENT_SECRET are required for OAuth mode")
	}
	if cfg.MCPPath == "" {
		cfg.MCPPath = "/mcp"
	}
	if !strings.HasPrefix(cfg.MCPPath, "/") {
		return nil, fmt.Errorf("MCP path must start with /")
	}
	if strings.TrimSpace(cfg.GiteaScopes) == "" {
		cfg.GiteaScopes = "read:user write:repository"
	}
	s, err := newSealer(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Bridge{
		cfg:       cfg,
		sealer:    s,
		http:      hc,
		usedCodes: make(map[string]time.Time),
	}, nil
}

func (b *Bridge) RegisterRoutes(mux *http.ServeMux) {
	// RFC 9728 path-aware metadata for a protected resource such as /mcp.
	mux.HandleFunc("/.well-known/oauth-protected-resource"+b.cfg.MCPPath, b.handleProtectedResource)
	// Keep the root location for compatibility with clients that probe the origin.
	mux.HandleFunc("/.well-known/oauth-protected-resource", b.handleProtectedResource)
	mux.HandleFunc("/.well-known/oauth-authorization-server", b.handleAuthorizationServerMetadata)
	mux.HandleFunc("/.well-known/openid-configuration", b.handleAuthorizationServerMetadata)
	mux.HandleFunc("/oauth/register", b.handleRegister)
	mux.HandleFunc("/oauth/authorize", b.handleAuthorize)
	mux.HandleFunc("/oauth/gitea/callback", b.handleGiteaCallback)
	mux.HandleFunc("/oauth/token", b.handleToken)
}

func (b *Bridge) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		parts := strings.Fields(header)
		if len(parts) == 0 {
			// No credentials were supplied. This is an authentication challenge,
			// not an invalid-token response. OpenAI uses this challenge to start
			// OAuth discovery for a newly connected MCP server.
			b.challenge(w, "", "")
			return
		}
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			b.challenge(w, "invalid_token", "The Authorization header is invalid")
			return
		}
		var token accessTokenPayload
		if err := b.sealer.open("access-token", parts[1], &token); err != nil {
			b.challenge(w, "invalid_token", "The access token is invalid")
			return
		}
		now := time.Now().Unix()
		if token.ExpiresAt <= now || token.Resource != b.resource() || token.GiteaAccessToken == "" {
			b.challenge(w, "invalid_token", "The access token is expired or was issued for another resource")
			return
		}
		ctx := context.WithValue(r.Context(), giteaAccessTokenKey, token.GiteaAccessToken)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func GiteaAccessTokenFromContext(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(giteaAccessTokenKey).(string)
	return token, ok && token != ""
}

func (b *Bridge) resource() string {
	return b.cfg.PublicBaseURL + b.cfg.MCPPath
}

func (b *Bridge) issuer() string {
	return b.cfg.PublicBaseURL
}

func (b *Bridge) giteaCallbackURL() string {
	return b.cfg.PublicBaseURL + "/oauth/gitea/callback"
}

func (b *Bridge) metadataURL() string {
	return b.cfg.PublicBaseURL + "/.well-known/oauth-protected-resource" + b.cfg.MCPPath
}

func (b *Bridge) handleProtectedResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":              b.resource(),
		"authorization_servers": []string{b.issuer()},
		"scopes_supported":        []string{"gitea"},
		"bearer_methods_supported": []string{"header"},
	})
}

func (b *Bridge) handleAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                      b.issuer(),
		"authorization_endpoint":                      b.cfg.PublicBaseURL + "/oauth/authorize",
		"token_endpoint":                              b.cfg.PublicBaseURL + "/oauth/token",
		"registration_endpoint":                       b.cfg.PublicBaseURL + "/oauth/register",
		"response_types_supported":                    []string{"code"},
		"grant_types_supported":                       []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":            []string{"S256"},
		"token_endpoint_auth_methods_supported":        []string{"none"},
		"scopes_supported":                            []string{"gitea"},
		"authorization_response_iss_parameter_supported": true,
	})
}

func (b *Bridge) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		RedirectURIs            []string `json:"redirect_uris"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		ClientName              string   `json:"client_name"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid registration request")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 16 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "at least one redirect URI is required")
		return
	}
	for _, raw := range req.RedirectURIs {
		if err := validateRedirectURI(raw); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return
		}
	}
	if req.TokenEndpointAuthMethod == "" {
		req.TokenEndpointAuthMethod = "none"
	}
	if req.TokenEndpointAuthMethod != "none" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "only public clients using token_endpoint_auth_method=none are supported")
		return
	}
	client := registeredClient{
		RedirectURIs:            req.RedirectURIs,
		TokenEndpointAuthMethod: "none",
		ClientName:              req.ClientName,
		IssuedAt:                time.Now().Unix(),
	}
	clientID, err := b.sealer.seal("client-id", client)
	if err != nil {
		http.Error(w, "failed to create client", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  clientID,
		"client_id_issued_at":        client.IssuedAt,
		"redirect_uris":              client.RedirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}

func (b *Bridge) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	var client registeredClient
	clientID := q.Get("client_id")
	if clientID == "" || b.sealer.open("client-id", clientID, &client) != nil {
		http.Error(w, "invalid client", http.StatusBadRequest)
		return
	}
	redirectURI := q.Get("redirect_uri")
	if !contains(client.RedirectURIs, redirectURI) {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	state := q.Get("state")
	if q.Get("response_type") != "code" {
		b.redirectOAuthError(w, r, redirectURI, state, "unsupported_response_type", "only response_type=code is supported")
		return
	}
	resource := q.Get("resource")
	if resource == "" {
		resource = b.resource()
	}
	if resource != b.resource() {
		b.redirectOAuthError(w, r, redirectURI, state, "invalid_target", "resource does not match this MCP server")
		return
	}
	scope, err := normalizeScope(q.Get("scope"))
	if err != nil {
		b.redirectOAuthError(w, r, redirectURI, state, "invalid_scope", err.Error())
		return
	}
	challenge := q.Get("code_challenge")
	if challenge == "" || q.Get("code_challenge_method") != "S256" {
		b.redirectOAuthError(w, r, redirectURI, state, "invalid_request", "PKCE with code_challenge_method=S256 is required")
		return
	}

	upstreamVerifier, err := randomURLString(48)
	if err != nil {
		http.Error(w, "failed to create PKCE verifier", http.StatusInternalServerError)
		return
	}
	pending := pendingAuthorization{
		ClientID:          clientID,
		RedirectURI:       redirectURI,
		ClientState:       state,
		CodeChallenge:     challenge,
		Resource:          resource,
		Scope:             scope,
		GiteaCodeVerifier: upstreamVerifier,
		ExpiresAt:         time.Now().Add(10 * time.Minute).Unix(),
	}
	sealedState, err := b.sealer.seal("gitea-state", pending)
	if err != nil {
		http.Error(w, "failed to create authorization state", http.StatusInternalServerError)
		return
	}

	authURL, _ := url.Parse(b.cfg.GiteaBaseURL + "/login/oauth/authorize")
	params := authURL.Query()
	params.Set("client_id", b.cfg.GiteaClientID)
	params.Set("redirect_uri", b.giteaCallbackURL())
	params.Set("response_type", "code")
	params.Set("state", sealedState)
	params.Set("scope", b.cfg.GiteaScopes)
	params.Set("code_challenge_method", "S256")
	params.Set("code_challenge", pkceChallenge(upstreamVerifier))
	authURL.RawQuery = params.Encode()
	http.Redirect(w, r, authURL.String(), http.StatusFound)
}

func (b *Bridge) handleGiteaCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var pending pendingAuthorization
	if err := b.sealer.open("gitea-state", r.URL.Query().Get("state"), &pending); err != nil || pending.ExpiresAt <= time.Now().Unix() {
		http.Error(w, "invalid or expired OAuth state", http.StatusBadRequest)
		return
	}
	if upstreamErr := r.URL.Query().Get("error"); upstreamErr != "" {
		desc := r.URL.Query().Get("error_description")
		b.redirectOAuthError(w, r, pending.RedirectURI, pending.ClientState, upstreamErr, desc)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		b.redirectOAuthError(w, r, pending.RedirectURI, pending.ClientState, "server_error", "Gitea returned no authorization code")
		return
	}

	token, err := b.exchangeGiteaToken(r.Context(), url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {b.cfg.GiteaClientID},
		"client_secret": {b.cfg.GiteaClientSecret},
		"code":          {code},
		"redirect_uri":  {b.giteaCallbackURL()},
		"code_verifier": {pending.GiteaCodeVerifier},
	})
	if err != nil {
		b.redirectOAuthError(w, r, pending.RedirectURI, pending.ClientState, "server_error", "failed to exchange Gitea authorization code")
		return
	}

	jti, err := randomURLString(24)
	if err != nil {
		http.Error(w, "failed to create authorization code", http.StatusInternalServerError)
		return
	}
	bridgeCode, err := b.sealer.seal("authorization-code", authorizationCode{
		ID:                jti,
		ClientID:          pending.ClientID,
		RedirectURI:       pending.RedirectURI,
		CodeChallenge:     pending.CodeChallenge,
		Resource:          pending.Resource,
		Scope:             pending.Scope,
		GiteaAccessToken:  token.AccessToken,
		GiteaRefreshToken: token.RefreshToken,
		GiteaExpiresIn:    token.ExpiresIn,
		ExpiresAt:         time.Now().Add(5 * time.Minute).Unix(),
	})
	if err != nil {
		http.Error(w, "failed to create authorization code", http.StatusInternalServerError)
		return
	}

	target, _ := url.Parse(pending.RedirectURI)
	params := target.Query()
	params.Set("code", bridgeCode)
	if pending.ClientState != "" {
		params.Set("state", pending.ClientState)
	}
	params.Set("iss", b.issuer())
	target.RawQuery = params.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (b *Bridge) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		b.exchangeBridgeAuthorizationCode(w, r)
	case "refresh_token":
		b.refreshBridgeToken(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "supported grant types are authorization_code and refresh_token")
	}
}

func (b *Bridge) exchangeBridgeAuthorizationCode(w http.ResponseWriter, r *http.Request) {
	clientID := r.Form.Get("client_id")
	if err := b.validateClient(clientID, r.Form.Get("redirect_uri")); err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", err.Error())
		return
	}
	var code authorizationCode
	if err := b.sealer.open("authorization-code", r.Form.Get("code"), &code); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "invalid authorization code")
		return
	}
	if code.ExpiresAt <= time.Now().Unix() ||
		code.ClientID != clientID ||
		code.RedirectURI != r.Form.Get("redirect_uri") {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code is expired or does not match this client")
		return
	}
	resource := r.Form.Get("resource")
	if resource == "" {
		resource = code.Resource
	}
	if resource != code.Resource || resource != b.resource() {
		writeOAuthError(w, http.StatusBadRequest, "invalid_target", "resource does not match the authorization request")
		return
	}
	verifier := r.Form.Get("code_verifier")
	if verifier == "" || subtle.ConstantTimeCompare([]byte(pkceChallenge(verifier)), []byte(code.CodeChallenge)) != 1 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}
	if !b.consumeCode(code.ID, time.Unix(code.ExpiresAt, 0)) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code has already been used")
		return
	}
	b.issueTokens(w, clientID, code.Resource, code.Scope, giteaTokenResponse{
		AccessToken:  code.GiteaAccessToken,
		RefreshToken: code.GiteaRefreshToken,
		ExpiresIn:    code.GiteaExpiresIn,
		TokenType:    "bearer",
	})
}

func (b *Bridge) refreshBridgeToken(w http.ResponseWriter, r *http.Request) {
	clientID := r.Form.Get("client_id")
	var client registeredClient
	if clientID == "" || b.sealer.open("client-id", clientID, &client) != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "invalid client")
		return
	}
	var refresh refreshTokenPayload
	if err := b.sealer.open("refresh-token", r.Form.Get("refresh_token"), &refresh); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "invalid refresh token")
		return
	}
	if refresh.ExpiresAt <= time.Now().Unix() || refresh.ClientID != clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token is expired or belongs to another client")
		return
	}
	resource := r.Form.Get("resource")
	if resource == "" {
		resource = refresh.Resource
	}
	if resource != refresh.Resource || resource != b.resource() {
		writeOAuthError(w, http.StatusBadRequest, "invalid_target", "resource does not match this MCP server")
		return
	}
	token, err := b.exchangeGiteaToken(r.Context(), url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {b.cfg.GiteaClientID},
		"client_secret": {b.cfg.GiteaClientSecret},
		"refresh_token": {refresh.GiteaRefreshToken},
	})
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Gitea refresh token exchange failed")
		return
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refresh.GiteaRefreshToken
	}
	b.issueTokens(w, clientID, refresh.Resource, refresh.Scope, token)
}

func (b *Bridge) issueTokens(w http.ResponseWriter, clientID, resource, scope string, token giteaTokenResponse) {
	if token.AccessToken == "" {
		writeOAuthError(w, http.StatusBadGateway, "server_error", "Gitea returned no access token")
		return
	}
	now := time.Now()
	accessTTL := 55 * time.Minute
	if token.ExpiresIn > 0 {
		upstreamTTL := time.Duration(token.ExpiresIn) * time.Second
		if upstreamTTL < accessTTL {
			accessTTL = upstreamTTL
		}
	}
	if accessTTL > 30*time.Second {
		accessTTL -= 30 * time.Second
	}
	if accessTTL < time.Minute {
		accessTTL = time.Minute
	}
	accessExp := now.Add(accessTTL)
	access, err := b.sealer.seal("access-token", accessTokenPayload{
		GiteaAccessToken: token.AccessToken,
		Resource:         resource,
		Scope:            scope,
		IssuedAt:         now.Unix(),
		ExpiresAt:        accessExp.Unix(),
	})
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to issue access token")
		return
	}
	response := map[string]any{
		"access_token": access,
		"token_type":   "Bearer",
		"expires_in":   int64(time.Until(accessExp).Seconds()),
		"scope":        scope,
	}
	if token.RefreshToken != "" {
		refresh, err := b.sealer.seal("refresh-token", refreshTokenPayload{
			ClientID:          clientID,
			GiteaRefreshToken: token.RefreshToken,
			Resource:          resource,
			Scope:             scope,
			IssuedAt:          now.Unix(),
			ExpiresAt:         now.Add(30 * 24 * time.Hour).Unix(),
		})
		if err != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to issue refresh token")
			return
		}
		response["refresh_token"] = refresh
	}
	writeJSON(w, http.StatusOK, response)
}

func (b *Bridge) exchangeGiteaToken(ctx context.Context, form url.Values) (giteaTokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.cfg.GiteaBaseURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return giteaTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := b.http.Do(req)
	if err != nil {
		return giteaTokenResponse{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return giteaTokenResponse{}, err
	}
	var token giteaTokenResponse
	if err := json.Unmarshal(raw, &token); err != nil {
		return giteaTokenResponse{}, fmt.Errorf("decode Gitea token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || token.Error != "" {
		if token.ErrorDescription == "" {
			token.ErrorDescription = strings.TrimSpace(string(raw))
		}
		return giteaTokenResponse{}, fmt.Errorf("Gitea OAuth token exchange failed: %s %s", token.Error, token.ErrorDescription)
	}
	if token.AccessToken == "" {
		return giteaTokenResponse{}, fmt.Errorf("Gitea OAuth token exchange returned no access token")
	}
	return token, nil
}

func (b *Bridge) validateClient(clientID, redirectURI string) error {
	var client registeredClient
	if clientID == "" || b.sealer.open("client-id", clientID, &client) != nil {
		return fmt.Errorf("invalid client")
	}
	if redirectURI == "" || !contains(client.RedirectURIs, redirectURI) {
		return fmt.Errorf("redirect_uri is not registered for this client")
	}
	return nil
}

func (b *Bridge) consumeCode(id string, expiresAt time.Time) bool {
	if id == "" {
		return false
	}
	now := time.Now()
	b.usedMu.Lock()
	defer b.usedMu.Unlock()
	for key, exp := range b.usedCodes {
		if exp.Before(now) {
			delete(b.usedCodes, key)
		}
	}
	if _, exists := b.usedCodes[id]; exists {
		return false
	}
	b.usedCodes[id] = expiresAt
	return true
}

func (b *Bridge) challenge(w http.ResponseWriter, oauthError, description string) {
	value := fmt.Sprintf(`Bearer resource_metadata="%s", scope="gitea"`, b.metadataURL())
	if oauthError != "" {
		value += fmt.Sprintf(`, error="%s"`, escapeHeaderValue(oauthError))
	}
	if description != "" {
		value += fmt.Sprintf(`, error_description="%s"`, escapeHeaderValue(description))
	}
	w.Header().Set("WWW-Authenticate", value)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	body := map[string]any{}
	if oauthError != "" {
		body["error"] = oauthError
	}
	if description != "" {
		body["error_description"] = description
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (b *Bridge) redirectOAuthError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, description string) {
	target, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, description, http.StatusBadRequest)
		return
	}
	q := target.Query()
	q.Set("error", code)
	if description != "" {
		q.Set("error_description", description)
	}
	if state != "" {
		q.Set("state", state)
	}
	q.Set("iss", b.issuer())
	target.RawQuery = q.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func normalizeScope(scope string) (string, error) {
	if strings.TrimSpace(scope) == "" {
		return "gitea", nil
	}
	seen := false
	for _, item := range strings.Fields(scope) {
		if item != "gitea" {
			return "", fmt.Errorf("unsupported scope %q", item)
		}
		seen = true
	}
	if !seen {
		return "", fmt.Errorf("scope gitea is required")
	}
	return "gitea", nil
}

func validateRedirectURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("redirect URI must be absolute")
	}
	if u.Fragment != "" {
		return fmt.Errorf("redirect URI must not contain a fragment")
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	if u.Scheme == "http" && (host == "127.0.0.1" || host == "::1") {
		return nil
	}
	return fmt.Errorf("redirect URI must use HTTPS, except loopback IPs used for local development")
}

func parseAbsoluteURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return nil, fmt.Errorf("must be an absolute URL")
	}
	return u, nil
}

func randomURLString(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func escapeHeaderValue(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	return strings.ReplaceAll(value, "\"", "\\\"")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, map[string]any{
		"error":             code,
		"error_description": description,
	})
}
