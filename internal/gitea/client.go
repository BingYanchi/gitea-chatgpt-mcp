package gitea

import (
	"bytes"
	"context"
	"errors"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type HTTPError struct {
	Method     string
	Endpoint   string
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("gitea %s %s: status %d: %s", e.Method, e.Endpoint, e.StatusCode, e.Body)
}

func IsUnauthorized(err error) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusUnauthorized
}

type Client struct {
	baseURL    string
	token      string
	authScheme string
	http       *http.Client
}

func NewClient(baseURL, token string, timeout time.Duration) (*Client, error) {
	return newClient(baseURL, token, "token", timeout)
}

func NewOAuthClient(baseURL, token string, timeout time.Duration) (*Client, error) {
	return newClient(baseURL, token, "Bearer", timeout)
}

func newClient(baseURL, token, authScheme string, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid Gitea base URL")
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("Gitea access token is required")
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		authScheme: authScheme,
		http:       &http.Client{Timeout: timeout},
	}, nil
}

func (c *Client) do(ctx context.Context, method, endpoint string, query url.Values, body any) (any, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api/v1"+endpoint, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", c.authScheme+" "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.URL.RawQuery = query.Encode()

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{
			Method:     method,
			Endpoint:   endpoint,
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(raw)),
		}
	}
	if len(raw) == 0 {
		return map[string]any{"ok": true}, nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode Gitea response: %w", err)
	}
	return out, nil
}

func esc(v string) string { return url.PathEscape(v) }

func (c *Client) CurrentUser(ctx context.Context) (any, error) {
	return c.do(ctx, http.MethodGet, "/user", nil, nil)
}

func (c *Client) ListRepositories(ctx context.Context, page, limit int) (any, error) {
	q := url.Values{}
	if page > 0 { q.Set("page", fmt.Sprint(page)) }
	if limit > 0 { q.Set("limit", fmt.Sprint(limit)) }
	return c.do(ctx, http.MethodGet, "/user/repos", q, nil)
}

func (c *Client) GetRepository(ctx context.Context, owner, repo string) (any, error) {
	return c.do(ctx, http.MethodGet, "/repos/"+esc(owner)+"/"+esc(repo), nil, nil)
}

func (c *Client) GetContents(ctx context.Context, owner, repo, filePath, ref string) (any, error) {
	endpoint := "/repos/"+esc(owner)+"/"+esc(repo)+"/contents"
	if filePath != "" {
		parts := strings.Split(strings.TrimPrefix(filePath, "/"), "/")
		for i := range parts { parts[i] = esc(parts[i]) }
		endpoint += "/" + strings.Join(parts, "/")
	}
	q := url.Values{}
	if ref != "" { q.Set("ref", ref) }
	return c.do(ctx, http.MethodGet, endpoint, q, nil)
}

func (c *Client) ListBranches(ctx context.Context, owner, repo string, page, limit int) (any, error) {
	q := url.Values{}
	if page > 0 { q.Set("page", fmt.Sprint(page)) }
	if limit > 0 { q.Set("limit", fmt.Sprint(limit)) }
	return c.do(ctx, http.MethodGet, "/repos/"+esc(owner)+"/"+esc(repo)+"/branches", q, nil)
}

func (c *Client) GetBranch(ctx context.Context, owner, repo, branch string) (any, error) {
	return c.do(ctx, http.MethodGet, "/repos/"+esc(owner)+"/"+esc(repo)+"/branches/"+esc(branch), nil, nil)
}

func (c *Client) GetCommit(ctx context.Context, owner, repo, sha string) (any, error) {
	return c.do(ctx, http.MethodGet, "/repos/"+esc(owner)+"/"+esc(repo)+"/git/commits/"+esc(sha), nil, nil)
}

func (c *Client) Compare(ctx context.Context, owner, repo, base, head string) (any, error) {
	return c.do(ctx, http.MethodGet, "/repos/"+esc(owner)+"/"+esc(repo)+"/compare/"+esc(base)+"..."+esc(head), nil, nil)
}

func (c *Client) CreateBranch(ctx context.Context, owner, repo, newBranch, oldBranch string) (any, error) {
	body := map[string]any{"new_branch_name": newBranch}
	if oldBranch != "" { body["old_branch_name"] = oldBranch }
	return c.do(ctx, http.MethodPost, "/repos/"+esc(owner)+"/"+esc(repo)+"/branches", nil, body)
}

func (c *Client) ApplyChanges(ctx context.Context, owner, repo, branch, newBranch, message, expectedHead string, files []ChangeFile) (any, error) {
	if expectedHead != "" {
		current, err := c.GetBranch(ctx, owner, repo, branch)
		if err != nil { return nil, err }
		m, _ := current.(map[string]any)
		commit, _ := m["commit"].(map[string]any)
		sha, _ := commit["id"].(string)
		if sha == "" {
			sha, _ = commit["sha"].(string)
		}
		if sha != expectedHead {
			return nil, fmt.Errorf("branch head changed: expected %s, got %s", expectedHead, sha)
		}
	}

	ops := make([]map[string]any, 0, len(files))
	for _, f := range files {
		op := map[string]any{
			"operation": f.Operation,
			"path":      path.Clean(strings.TrimPrefix(f.Path, "/")),
		}
		if f.Content != "" {
			op["content"] = base64.StdEncoding.EncodeToString([]byte(f.Content))
		}
		if f.SHA != "" { op["sha"] = f.SHA }
		if f.FromPath != "" { op["from_path"] = path.Clean(strings.TrimPrefix(f.FromPath, "/")) }
		ops = append(ops, op)
	}
	body := map[string]any{
		"branch":  branch,
		"files":   ops,
		"message": message,
	}
	if newBranch != "" { body["new_branch"] = newBranch }
	return c.do(ctx, http.MethodPost, "/repos/"+esc(owner)+"/"+esc(repo)+"/contents", nil, body)
}

func (c *Client) CreatePullRequest(ctx context.Context, owner, repo, title, bodyText, head, base string, draft bool) (any, error) {
	body := map[string]any{"title": title, "body": bodyText, "head": head, "base": base, "draft": draft}
	return c.do(ctx, http.MethodPost, "/repos/"+esc(owner)+"/"+esc(repo)+"/pulls", nil, body)
}

func (c *Client) GetPullRequest(ctx context.Context, owner, repo string, index int) (any, error) {
	return c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/pulls/%d", esc(owner), esc(repo), index), nil, nil)
}

func (c *Client) GetPullRequestDiff(ctx context.Context, owner, repo string, index int) (any, error) {
	endpoint := fmt.Sprintf("/repos/%s/%s/pulls/%d.diff", esc(owner), esc(repo), index)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1"+endpoint, nil)
	if err != nil { return nil, err }
	req.Header.Set("Authorization", c.authScheme+" "+c.token)
	req.Header.Set("Accept", "text/plain")
	resp, err := c.http.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil { return nil, err }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{
			Method:     http.MethodGet,
			Endpoint:   endpoint,
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(raw)),
		}
	}
	return map[string]any{"diff": string(raw)}, nil
}

type ChangeFile struct {
	Operation string
	Path      string
	Content   string
	SHA       string
	FromPath  string
}
