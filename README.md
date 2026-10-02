# Gitea ChatGPT MCP

A Streamable HTTP MCP server that lets ChatGPT read and modify repositories on a self-hosted Gitea instance.

It is API-first: no repository clone, shell, SSH key, or Git credential is exposed to the model.

## Authentication modes

### OAuth mode (recommended for ChatGPT)

Each ChatGPT connection authenticates through your Gitea instance. The MCP server implements the MCP OAuth 2.1-facing endpoints and bridges them to Gitea's OAuth2 Authorization Code flow.

Flow:

```text
ChatGPT
  -> MCP OAuth authorize
  -> Gitea login / consent
  -> MCP OAuth callback
  -> ChatGPT receives an MCP access token
  -> each MCP request uses that user's Gitea OAuth access token
```

Users therefore only see and modify repositories that their own Gitea account can access.

The bridge supports:

- OAuth protected resource metadata
- OAuth authorization server metadata
- Dynamic Client Registration (DCR)
- Authorization Code + PKCE S256
- Gitea Authorization Code + PKCE
- access-token refresh
- per-request Gitea Bearer tokens

The MCP-facing access/refresh tokens are encrypted opaque tokens. Gitea tokens are never sent to the model.

### Token mode

Legacy/service-account mode. One `GITEA_TOKEN` is used for every MCP caller.

## Current tools

Read-only:

- `get_current_user`
- `list_repositories`
- `get_repository`
- `get_tree`
- `get_file`
- `list_branches`
- `get_branch`
- `get_commit`
- `compare_refs`
- `get_pull_request`
- `get_pull_request_diff`

Write:

- `create_branch`
- `apply_changes`
- `create_pull_request`

`apply_changes` maps to Gitea's multi-file change API and supports `expected_head_sha` to reject stale agent edits.

## Gitea OAuth setup

Create an OAuth2 application in Gitea:

```text
User Settings / Site Admin
-> Applications
-> OAuth2 Applications
```

Use this redirect URI:

```text
https://gitea-mcp.example.com/oauth/gitea/callback
```

Keep the generated Client ID and Client Secret.

The default upstream scopes are:

```text
read:user write:repository
```

On Gitea 1.23+, granular OAuth scopes make `write:repository` cover repository reads/writes, files, pull requests and related `/repos/*` operations, while `read:user` covers authenticated-user operations.

## Configuration

```bash
cp .env.example .env
openssl rand -base64 32
```

OAuth configuration:

```env
AUTH_MODE=oauth

GITEA_BASE_URL=https://git.example.com
PUBLIC_BASE_URL=https://gitea-mcp.example.com

GITEA_OAUTH_CLIENT_ID=...
GITEA_OAUTH_CLIENT_SECRET=...
GITEA_OAUTH_SCOPES=read:user write:repository

OAUTH_ENCRYPTION_KEY=<base64 32-byte random key>

LISTEN_ADDR=:8080
MCP_PATH=/mcp
REQUEST_TIMEOUT=30s
```

`PUBLIC_BASE_URL` is the externally reachable HTTPS origin of this MCP server. Do not include `/mcp`.

The encryption key must remain stable. Rotating it intentionally invalidates existing ChatGPT OAuth clients, authorization codes, access tokens, and refresh tokens.

Token-mode configuration:

```env
AUTH_MODE=token
GITEA_BASE_URL=https://git.example.com
GITEA_TOKEN=...
```

## Run

Using the published GHCR image:

```yaml
services:
  gitea-chatgpt-mcp:
    image: ghcr.io/bingyanchi/gitea-chatgpt-mcp:latest
    restart: unless-stopped
    env_file:
      - .env
    ports:
      - "8080:8080"
```

Then:

```bash
docker compose pull
docker compose up -d
```

Health:

```bash
curl http://127.0.0.1:8080/healthz
```

MCP:

```text
https://gitea-mcp.example.com/mcp
```

OAuth discovery:

```text
https://gitea-mcp.example.com/.well-known/oauth-protected-resource
https://gitea-mcp.example.com/.well-known/oauth-authorization-server
```

## Reverse proxy

Expose the entire origin, not only `/mcp`, because OAuth also needs:

```text
/.well-known/oauth-protected-resource
/.well-known/oauth-authorization-server
/.well-known/openid-configuration
/oauth/register
/oauth/authorize
/oauth/token
/oauth/gitea/callback
/mcp
```

For example, proxy `https://gitea-mcp.example.com/*` to `http://127.0.0.1:8080`.

## ChatGPT connection

Add the HTTPS MCP endpoint:

```text
https://gitea-mcp.example.com/mcp
```

ChatGPT should discover the protected-resource metadata, register an OAuth public client, and show the Gitea login/consent flow. After linking, MCP calls carry an MCP Bearer token, which the bridge validates and maps to the authenticated user's Gitea OAuth token.

## Recommended edit flow

1. `get_branch` and record the head SHA.
2. `get_tree` / `get_file`.
3. `apply_changes` with `new_branch` and `expected_head_sha`.
4. `compare_refs`.
5. `create_pull_request`.

## Security notes

- No raw arbitrary-HTTP MCP tool.
- No shell or Git CLI tool.
- Gitea Client Secret remains server-side.
- Gitea OAuth access and refresh tokens are encrypted inside opaque MCP tokens.
- OAuth authorization codes require PKCE S256 and are one-time-use while the server process is running.
- MCP tokens are bound to the configured MCP resource URL.
- The Gitea OAuth callback state is authenticated/encrypted and short-lived.
- Run OAuth mode behind HTTPS.
- `OAUTH_ENCRYPTION_KEY` is sensitive and should be supplied as a secret.

## Compatibility

OAuth granular scopes require Gitea 1.23+ for the default restrictive scope set. Older Gitea installations can override `GITEA_OAUTH_SCOPES`, for example to the legacy `repo` scope.

## License

MIT
