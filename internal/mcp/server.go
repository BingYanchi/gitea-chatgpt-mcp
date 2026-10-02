package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/BingYanchi/gitea-chatgpt-mcp/internal/gitea"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func New(client *gitea.Client) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "gitea-chatgpt-mcp",
		Version: "0.1.0",
	}, nil)

	addReadTools(s, client)
	addWriteTools(s, client)
	return s
}

func ro() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		ReadOnlyHint:  true,
		OpenWorldHint: true,
	}
}

func write() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: false,
		IdempotentHint:  false,
		OpenWorldHint:   true,
	}
}

type emptyArgs struct{}

type repoArgs struct {
	Owner string `json:"owner" jsonschema:"Gitea repository owner"`
	Repo  string `json:"repo" jsonschema:"Gitea repository name"`
}

func addReadTools(s *mcp.Server, c *gitea.Client) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_current_user",
		Description: "Get the authenticated Gitea user.",
		Annotations: ro(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
		v, err := c.CurrentUser(ctx)
		return nil, v, err
	})

	type listReposArgs struct {
		Page  int `json:"page,omitempty" jsonschema:"1-based page number"`
		Limit int `json:"limit,omitempty" jsonschema:"results per page"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_repositories",
		Description: "List repositories visible to the authenticated Gitea user.",
		Annotations: ro(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listReposArgs) (*mcp.CallToolResult, any, error) {
		v, err := c.ListRepositories(ctx, in.Page, in.Limit)
		return nil, v, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_repository",
		Description: "Get metadata for a Gitea repository.",
		Annotations: ro(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in repoArgs) (*mcp.CallToolResult, any, error) {
		v, err := c.GetRepository(ctx, in.Owner, in.Repo)
		return nil, v, err
	})

	type contentsArgs struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Path  string `json:"path,omitempty" jsonschema:"repository-relative path; omit for repository root"`
		Ref   string `json:"ref,omitempty" jsonschema:"branch, tag, or commit"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_tree",
		Description: "List a repository directory. Use path empty for the repository root.",
		Annotations: ro(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in contentsArgs) (*mcp.CallToolResult, any, error) {
		v, err := c.GetContents(ctx, in.Owner, in.Repo, in.Path, in.Ref)
		return nil, v, err
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_file",
		Description: "Read a repository file and its metadata from a branch, tag, or commit.",
		Annotations: ro(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in contentsArgs) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Path) == "" {
			return nil, nil, fmt.Errorf("path is required")
		}
		v, err := c.GetContents(ctx, in.Owner, in.Repo, in.Path, in.Ref)
		return nil, v, err
	})

	type branchesArgs struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Page  int    `json:"page,omitempty"`
		Limit int    `json:"limit,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_branches",
		Description: "List branches in a Gitea repository.",
		Annotations: ro(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in branchesArgs) (*mcp.CallToolResult, any, error) {
		v, err := c.ListBranches(ctx, in.Owner, in.Repo, in.Page, in.Limit)
		return nil, v, err
	})

	type branchArgs struct {
		Owner  string `json:"owner"`
		Repo   string `json:"repo"`
		Branch string `json:"branch"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_branch",
		Description: "Get one branch, including its current head commit.",
		Annotations: ro(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in branchArgs) (*mcp.CallToolResult, any, error) {
		v, err := c.GetBranch(ctx, in.Owner, in.Repo, in.Branch)
		return nil, v, err
	})

	type commitArgs struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		SHA   string `json:"sha"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_commit",
		Description: "Get a Git commit by SHA.",
		Annotations: ro(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in commitArgs) (*mcp.CallToolResult, any, error) {
		v, err := c.GetCommit(ctx, in.Owner, in.Repo, in.SHA)
		return nil, v, err
	})

	type compareArgs struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Base  string `json:"base"`
		Head  string `json:"head"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "compare_refs",
		Description: "Compare two repository refs and return commits and file changes.",
		Annotations: ro(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in compareArgs) (*mcp.CallToolResult, any, error) {
		v, err := c.Compare(ctx, in.Owner, in.Repo, in.Base, in.Head)
		return nil, v, err
	})

	type prArgs struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Index int64  `json:"index"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_pull_request",
		Description: "Get a pull request by index.",
		Annotations: ro(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in prArgs) (*mcp.CallToolResult, any, error) {
		v, err := c.GetPullRequest(ctx, in.Owner, in.Repo, int(in.Index))
		return nil, v, err
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_pull_request_diff",
		Description: "Get the unified diff for a pull request.",
		Annotations: ro(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in prArgs) (*mcp.CallToolResult, any, error) {
		v, err := c.GetPullRequestDiff(ctx, in.Owner, in.Repo, int(in.Index))
		return nil, v, err
	})
}

func addWriteTools(s *mcp.Server, c *gitea.Client) {
	type createBranchArgs struct {
		Owner     string `json:"owner"`
		Repo      string `json:"repo"`
		NewBranch string `json:"new_branch"`
		OldBranch string `json:"old_branch,omitempty" jsonschema:"base branch; omit to use the repository default"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_branch",
		Description: "Create a new branch from an existing branch.",
		Annotations: write(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in createBranchArgs) (*mcp.CallToolResult, any, error) {
		v, err := c.CreateBranch(ctx, in.Owner, in.Repo, in.NewBranch, in.OldBranch)
		return nil, v, err
	})

	type change struct {
		Operation string `json:"operation" jsonschema:"one of create, update, upload, rename, delete"`
		Path      string `json:"path"`
		Content   string `json:"content,omitempty" jsonschema:"plain UTF-8 content; the server base64-encodes it for Gitea"`
		SHA       string `json:"sha,omitempty" jsonschema:"existing blob SHA, required by Gitea for update/delete"`
		FromPath  string `json:"from_path,omitempty" jsonschema:"source path for rename"`
	}
	type applyArgs struct {
		Owner           string   `json:"owner"`
		Repo            string   `json:"repo"`
		Branch          string   `json:"branch"`
		NewBranch       string   `json:"new_branch,omitempty" jsonschema:"optional new branch to create and commit into"`
		Message         string   `json:"message"`
		ExpectedHeadSHA string   `json:"expected_head_sha,omitempty" jsonschema:"optimistic concurrency guard; fail if branch head no longer matches"`
		Changes         []change `json:"changes"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "apply_changes",
		Description: "Apply multiple file create/update/upload/rename/delete operations in one Gitea commit. Prefer a new branch and expected_head_sha for safe agent edits.",
		Annotations: write(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in applyArgs) (*mcp.CallToolResult, any, error) {
		if len(in.Changes) == 0 {
			return nil, nil, fmt.Errorf("changes must not be empty")
		}
		files := make([]gitea.ChangeFile, 0, len(in.Changes))
		for _, f := range in.Changes {
			switch f.Operation {
			case "create", "update", "upload", "rename", "delete":
			default:
				return nil, nil, fmt.Errorf("unsupported operation %q", f.Operation)
			}
			files = append(files, gitea.ChangeFile{
				Operation: f.Operation,
				Path:      f.Path,
				Content:   f.Content,
				SHA:       f.SHA,
				FromPath:  f.FromPath,
			})
		}
		v, err := c.ApplyChanges(ctx, in.Owner, in.Repo, in.Branch, in.NewBranch, in.Message, in.ExpectedHeadSHA, files)
		return nil, v, err
	})

	type createPRArgs struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Title string `json:"title"`
		Body  string `json:"body,omitempty"`
		Head  string `json:"head" jsonschema:"source branch"`
		Base  string `json:"base" jsonschema:"target branch"`
		Draft bool   `json:"draft,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_pull_request",
		Description: "Create a Gitea pull request.",
		Annotations: write(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in createPRArgs) (*mcp.CallToolResult, any, error) {
		v, err := c.CreatePullRequest(ctx, in.Owner, in.Repo, in.Title, in.Body, in.Head, in.Base, in.Draft)
		return nil, v, err
	})
}
