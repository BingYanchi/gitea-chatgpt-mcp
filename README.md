# Gitea ChatGPT MCP

A small MCP server that lets ChatGPT work with a self-hosted Gitea instance using the Gitea REST API.

It is intentionally API-first: the server does **not** clone repositories or expose a shell/Git CLI to the model. The Gitea token stays on the MCP server and is never passed as a tool argument.

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

`apply_changes` maps to Gitea's multi-file change API, so one tool call can create/update/upload/rename/delete multiple files in a single commit.

For agent edits, use `expected_head_sha` with `apply_changes`. The server checks the branch head immediately before writing and rejects the operation if it changed.

## Configuration

Copy `.env.example` to `.env`:

```env
GITEA_BASE_URL=https://git.example.com
GITEA_TOKEN=replace-me
LISTEN_ADDR=:8080
MCP_PATH=/mcp
REQUEST_TIMEOUT=30s
```

`GITEA_BASE_URL` is the root URL of your Gitea instance, not `/api/v1`.

Create a Gitea personal access token with only the repository permissions you want ChatGPT to have. Prefer a dedicated Gitea account/token for this MCP server.

## Run

### Docker Compose

```bash
docker compose up -d --build
```

Endpoints:

- MCP: `http://localhost:8080/mcp`
- Health: `http://localhost:8080/healthz`

For ChatGPT, expose the MCP endpoint over HTTPS, for example:

```text
https://gitea-mcp.example.com/mcp
```

### Go

```bash
go run ./cmd/server
```

## Typical agent workflow

1. `get_repository`
2. `get_branch` and record the current head SHA
3. `get_tree` / `get_file` to inspect code
4. `apply_changes` with:
   - the base branch
   - a new branch name
   - `expected_head_sha`
   - all file modifications
5. `compare_refs`
6. `create_pull_request`

This keeps normal model-driven changes off the default branch.

## Example apply_changes input

```json
{
  "owner": "example",
  "repo": "service",
  "branch": "main",
  "new_branch": "fix/validation",
  "message": "fix: validate request",
  "expected_head_sha": "0123456789abcdef",
  "changes": [
    {
      "operation": "update",
      "path": "internal/http/handler.go",
      "sha": "existing-file-blob-sha",
      "content": "package http\n..."
    },
    {
      "operation": "create",
      "path": "internal/http/handler_test.go",
      "content": "package http\n..."
    }
  ]
}
```

The MCP server accepts plain UTF-8 content and base64-encodes it before sending it to Gitea.

## Security model

- Gitea credentials are process configuration, not MCP tool parameters.
- No arbitrary HTTP-request tool is exposed.
- No shell execution or Git credential forwarding is exposed.
- Read and write tools have MCP tool annotations.
- Multi-file edits can use optimistic concurrency with `expected_head_sha`.
- Run this behind TLS and normal ingress/auth controls before exposing it publicly.

At this stage the MCP endpoint itself does not implement an additional OAuth/login layer. Treat the endpoint as a trusted private service and protect it at your reverse proxy or network boundary.

## Compatibility

The implementation targets the current Gitea REST API shape for repository contents, branches, commits, comparisons, and pull requests. Gitea's multi-file contents endpoint is required for `apply_changes`.

## Roadmap

- OAuth / per-user Gitea connections instead of one server-wide PAT
- Search code
- Issues and PR comments/reviews
- Tags/releases
- Actions
- multiple named Gitea connections
- Forgejo compatibility testing
