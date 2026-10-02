# Gitea ChatGPT MCP

A Streamable HTTP MCP server that lets ChatGPT read and modify repositories on a self-hosted Gitea instance.

The recommended deployment is now **OpenAI Secure MCP Tunnel + a dedicated Gitea PAT**. The MCP service remains private on the Docker network; `tunnel-client` makes outbound HTTPS connections to OpenAI and forwards MCP requests locally. No inbound MCP port or public MCP hostname is required.

## Architecture

```text
ChatGPT
   |
   | OpenAI-hosted tunnel endpoint
   v
OpenAI Tunnel control plane
   ^
   | outbound HTTPS :443
   |
tunnel-client
   |
   | Docker private network
   v
gitea-chatgpt-mcp:8080/mcp
   |
   | HTTPS / Gitea REST API
   v
Gitea
```

The tunnel container uses the official OpenAI `tunnel-client` image as its base and is pinned in `Dockerfile.tunnel`. Runtime secrets are never baked into either image.

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

`apply_changes` uses Gitea's multi-file change API and supports `expected_head_sha` to reject stale agent edits.

## Recommended: Secure MCP Tunnel

### 1. Create a dedicated Gitea PAT

Use a dedicated Gitea account or PAT with only the repository permissions ChatGPT needs.

Do not commit the PAT.

### 2. Create an OpenAI MCP tunnel

Create a tunnel in OpenAI Platform tunnel settings. Record the resulting tunnel ID:

```text
tunnel_...
```

Create or choose the control-plane API key permitted to use that tunnel.

The tunnel client needs outbound HTTPS to OpenAI and network access to the MCP service. It does not need inbound Internet access.

### 3. Configure

```bash
cp .env.example .env
```

Minimal tunnel configuration:

```env
AUTH_MODE=token

GITEA_BASE_URL=https://git.example.com
GITEA_TOKEN=your-dedicated-gitea-pat

TUNNEL_ID=tunnel_...
CONTROL_PLANE_API_KEY=sk-...
```

Optional image pinning:

```env
MCP_IMAGE_TAG=latest
TUNNEL_IMAGE_TAG=latest
```

For production you can pin the generated `sha-...` tags instead of tracking `latest`.

### 4. Start

```bash
docker compose pull
docker compose up -d
```

Watch logs:

```bash
docker compose logs -f gitea-chatgpt-mcp tunnel-client
```

The default Compose deployment intentionally has **no `ports:` mapping** for the MCP service. `tunnel-client` reaches it internally at:

```text
http://gitea-chatgpt-mcp:8080/mcp
```

The tunnel sidecar receives these runtime values through Compose:

```text
CONTROL_PLANE_API_KEY
CONTROL_PLANE_TUNNEL_ID
MCP_SERVER_URL
MCP_STARTUP_WAIT_TIMEOUT
```

The repository-facing variables `TUNNEL_ID` and `CONTROL_PLANE_API_KEY` are mapped to the names expected by the official OpenAI client.

### 5. Connect from ChatGPT

When creating the custom MCP connection in ChatGPT:

```text
Connection: Tunnel
Tunnel: select the tunnel you created
Authentication: None
```

Do not enter the private Docker URL in ChatGPT. ChatGPT calls the OpenAI-hosted tunnel endpoint; the locally running tunnel client forwards those calls to the private MCP container.

With `AUTH_MODE=token`, Gitea authentication is handled by the MCP service using the dedicated PAT, so ChatGPT itself does not need OAuth.

## Docker images

A push to `main` builds and publishes both Linux `amd64` and `arm64` images:

```text
ghcr.io/bingyanchi/gitea-chatgpt-mcp:latest
ghcr.io/bingyanchi/gitea-chatgpt-mcp:sha-...

ghcr.io/bingyanchi/gitea-chatgpt-mcp-tunnel:latest
ghcr.io/bingyanchi/gitea-chatgpt-mcp-tunnel:sha-...
```

The tunnel wrapper is based on a pinned release of:

```text
ghcr.io/openai/tunnel-client
```

No OpenAI or Gitea secrets are present at build time.

## Updating

```bash
docker compose pull
docker compose up -d --force-recreate
```

## Optional: direct/public MCP mode

If you need to access the MCP server directly for debugging or a public HTTPS deployment, use the override:

```bash
docker compose -f compose.yml -f compose.public.yml up -d
```

By default it publishes only on loopback:

```text
127.0.0.1:8080 -> container:8080
```

Override with:

```env
MCP_PUBLISH_ADDR=0.0.0.0:8080
```

Do not expose token-mode MCP directly to the public Internet unless another trusted authentication/access-control layer protects it.

## Optional: per-user Gitea OAuth mode

OAuth support remains available for direct/public deployments.

Example:

```env
AUTH_MODE=oauth

GITEA_BASE_URL=https://git.example.com
PUBLIC_BASE_URL=https://gitea-mcp.example.com

GITEA_OAUTH_CLIENT_ID=...
GITEA_OAUTH_CLIENT_SECRET=...
GITEA_OAUTH_SCOPES=read:user write:repository
OAUTH_ENCRYPTION_KEY=<base64 32-byte random key>
```

Create the Gitea OAuth2 application with callback:

```text
https://gitea-mcp.example.com/oauth/gitea/callback
```

Generate the bridge encryption key with:

```bash
openssl rand -base64 32
```

OAuth endpoints include:

```text
/.well-known/oauth-protected-resource/mcp
/.well-known/oauth-authorization-server
/oauth/register
/oauth/authorize
/oauth/token
/oauth/gitea/callback
/mcp
```

For the private Tunnel deployment, `AUTH_MODE=token` is simpler and is the recommended configuration for a single trusted user/team service account.

## Security notes

- No arbitrary raw HTTP MCP tool is exposed.
- No shell or Git CLI tool is exposed.
- Gitea PATs and OpenAI API keys are runtime secrets only.
- The default Compose file does not publish the MCP port.
- Tunnel traffic is initiated outbound from your environment.
- Use a dedicated Gitea service account/PAT and scope it as narrowly as practical.
- Prefer Docker secrets or another secret manager over plaintext environment files where available.
- Do not commit `.env`.
- The Secure MCP Tunnel is intended for private MCP connections; public plugin distribution requires a stable public HTTPS MCP endpoint.

## License

MIT
