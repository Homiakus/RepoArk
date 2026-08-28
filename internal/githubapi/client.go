package githubapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const apiVersion = "2026-03-10"

type Client struct {
	BaseURL    string
	GraphQLURL string
	Token      string
	HTTP       *http.Client
	MaxPages   int
}

type User struct {
	Login string `json:"login"`
	Name  string `json:"name"`
}

type Repository struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	FullName       string    `json:"full_name"`
	Private        bool      `json:"private"`
	Fork           bool      `json:"fork"`
	Archived       bool      `json:"archived"`
	Disabled       bool      `json:"disabled"`
	CloneURL       string    `json:"clone_url"`
	SSHURL         string    `json:"ssh_url"`
	HTMLURL        string    `json:"html_url"`
	DefaultBranch  string    `json:"default_branch"`
	Visibility     string    `json:"visibility"`
	HasWiki        bool      `json:"has_wiki"`
	HasDiscussions bool      `json:"has_discussions"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Release struct {
	ID      int64          `json:"id"`
	TagName string         `json:"tag_name"`
	Name    string         `json:"name"`
	Assets  []ReleaseAsset `json:"assets"`
}

type ReleaseAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type Package struct {
	ID          int64              `json:"id"`
	Name        string             `json:"name"`
	PackageType string             `json:"package_type"`
	Visibility  string             `json:"visibility"`
	Repository  *PackageRepository `json:"repository,omitempty"`
	Versions    []json.RawMessage  `json:"versions,omitempty"`
}

type PackageRepository struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
}

func ResolveToken(envName string) string {
	for _, key := range []string{envName, "GH_TOKEN", "GITHUB_TOKEN"} {
		if key != "" {
			if v := strings.TrimSpace(os.Getenv(key)); v != "" {
				return v
			}
		}
	}
	if p, err := exec.LookPath("gh"); err == nil {
		cmd := exec.Command(p, "auth", "token")
		if b, err := cmd.Output(); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}

func New(baseURL, token string) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	graphQL := "https://api.github.com/graphql"
	if strings.Contains(baseURL, "/api/v3") {
		graphQL = strings.TrimSuffix(baseURL, "/api/v3") + "/api/graphql"
	}
	return &Client{BaseURL: baseURL, GraphQLURL: graphQL, Token: token, HTTP: &http.Client{Timeout: 90 * time.Second}, MaxPages: 100}
}

func (c *Client) User(ctx context.Context) (User, error) {
	var u User
	err := c.getJSON(ctx, "/user", nil, &u)
	return u, err
}

func (c *Client) Repositories(ctx context.Context) ([]Repository, error) {
	var all []Repository
	for page := 1; page <= c.pageLimit(); page++ {
		q := url.Values{}
		q.Set("per_page", "100")
		q.Set("page", strconv.Itoa(page))
		q.Set("visibility", "all")
		q.Set("affiliation", "owner,collaborator,organization_member")
		q.Set("sort", "full_name")
		var batch []Repository
		if err := c.getJSON(ctx, "/user/repos", q, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			return all, nil
		}
	}
	return all, fmt.Errorf("repository listing exceeded configured page limit %d", c.pageLimit())
}

func (c *Client) Metadata(ctx context.Context, repo Repository, level string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	basic, err := json.Marshal(repo)
	if err != nil {
		return nil, err
	}
	out["repository"] = basic
	if level != "full" {
		return out, nil
	}
	endpoints := map[string]string{
		"issues":               "/repos/" + repo.FullName + "/issues?state=all",
		"issue_comments":       "/repos/" + repo.FullName + "/issues/comments",
		"pulls":                "/repos/" + repo.FullName + "/pulls?state=all",
		"pull_review_comments": "/repos/" + repo.FullName + "/pulls/comments",
		"releases":             "/repos/" + repo.FullName + "/releases",
		"labels":               "/repos/" + repo.FullName + "/labels",
		"milestones":           "/repos/" + repo.FullName + "/milestones?state=all",
		"workflows":            "/repos/" + repo.FullName + "/actions/workflows",
		"deployments":          "/repos/" + repo.FullName + "/deployments",
		"environments":         "/repos/" + repo.FullName + "/environments",
		"hooks":                "/repos/" + repo.FullName + "/hooks",
		"branches":             "/repos/" + repo.FullName + "/branches",
		"tags":                 "/repos/" + repo.FullName + "/tags",
	}
	for name, ep := range endpoints {
		b, err := c.getAllPagesRaw(ctx, ep)
		if err != nil {
			msg, _ := json.Marshal(map[string]string{"warning": err.Error()})
			out[name] = msg
			continue
		}
		out[name] = b
	}

	// Pull-request reviews are nested under each PR and therefore require a second pass.
	var pulls []struct {
		Number int `json:"number"`
	}
	if raw := out["pulls"]; len(raw) > 0 && json.Unmarshal(raw, &pulls) == nil {
		reviews := make(map[string]json.RawMessage, len(pulls))
		for _, pr := range pulls {
			ep := fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo.FullName, pr.Number)
			raw, err := c.getAllPagesRaw(ctx, ep)
			if err != nil {
				raw, _ = json.Marshal(map[string]string{"warning": err.Error()})
			}
			reviews[strconv.Itoa(pr.Number)] = raw
		}
		if b, err := json.Marshal(reviews); err == nil {
			out["pull_reviews"] = b
		}
	}
	return out, nil
}

func (c *Client) Releases(ctx context.Context, fullName string) ([]Release, error) {
	var all []Release
	for page := 1; page <= c.pageLimit(); page++ {
		q := url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}
		var batch []Release
		if err := c.getJSON(ctx, "/repos/"+fullName+"/releases", q, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			return all, nil
		}
	}
	return all, fmt.Errorf("release listing exceeded configured page limit %d", c.pageLimit())
}

func (c *Client) DownloadReleaseAsset(ctx context.Context, asset ReleaseAsset, dst string, maxBytes int64) error {
	if maxBytes > 0 && asset.Size > maxBytes {
		return fmt.Errorf("asset %s is %d bytes and exceeds limit %d", asset.Name, asset.Size, maxBytes)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return err
	}
	c.headers(req)
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("release asset %s returned %d: %s", asset.Name, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	limit := maxBytes
	if limit <= 0 {
		limit = 1<<63 - 2
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if n > limit {
		_ = os.Remove(tmp)
		return fmt.Errorf("asset %s exceeded configured size limit while downloading", asset.Name)
	}
	return os.Rename(tmp, dst)
}

func (c *Client) Discussions(ctx context.Context, repo Repository) (json.RawMessage, error) {
	owner, name, ok := strings.Cut(repo.FullName, "/")
	if !ok {
		return nil, fmt.Errorf("invalid repository name %q", repo.FullName)
	}
	query := `query($owner:String!,$name:String!,$cursor:String){repository(owner:$owner,name:$name){discussions(first:100,after:$cursor,orderBy:{field:UPDATED_AT,direction:DESC}){nodes{id number title body url createdAt updatedAt author{login} category{name slug} answer{id body url author{login}} comments(first:100){totalCount nodes{id body url createdAt updatedAt author{login} replies(first:100){totalCount nodes{id body url createdAt updatedAt author{login}}}}}} pageInfo{hasNextPage endCursor}}}}`
	cursor := any(nil)
	var nodes []json.RawMessage
	truncatedNested := false
	for page := 1; page <= c.pageLimit(); page++ {
		payload := map[string]any{"query": query, "variables": map[string]any{"owner": owner, "name": name, "cursor": cursor}}
		var resp struct {
			Data struct {
				Repository *struct {
					Discussions struct {
						Nodes    []json.RawMessage `json:"nodes"`
						PageInfo struct {
							HasNextPage bool    `json:"hasNextPage"`
							EndCursor   *string `json:"endCursor"`
						} `json:"pageInfo"`
					} `json:"discussions"`
				} `json:"repository"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := c.graphQL(ctx, payload, &resp); err != nil {
			return nil, err
		}
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("GitHub GraphQL: %s", resp.Errors[0].Message)
		}
		if resp.Data.Repository == nil {
			return nil, errors.New("GitHub GraphQL repository not found or discussions unavailable")
		}
		for _, n := range resp.Data.Repository.Discussions.Nodes {
			var counts struct {
				Comments struct {
					TotalCount int `json:"totalCount"`
					Nodes      []struct {
						Replies struct {
							TotalCount int `json:"totalCount"`
						} `json:"replies"`
					} `json:"nodes"`
				} `json:"comments"`
			}
			if json.Unmarshal(n, &counts) == nil {
				if counts.Comments.TotalCount > 100 {
					truncatedNested = true
				}
				for _, comment := range counts.Comments.Nodes {
					if comment.Replies.TotalCount > 100 {
						truncatedNested = true
					}
				}
			}
			nodes = append(nodes, n)
		}
		if !resp.Data.Repository.Discussions.PageInfo.HasNextPage {
			wrapper := map[string]any{"discussions": nodes, "nested_comments_truncated": truncatedNested}
			return json.MarshalIndent(wrapper, "", "  ")
		}
		if resp.Data.Repository.Discussions.PageInfo.EndCursor == nil {
			break
		}
		cursor = *resp.Data.Repository.Discussions.PageInfo.EndCursor
	}
	return nil, fmt.Errorf("discussion listing exceeded configured page limit %d", c.pageLimit())
}

func (c *Client) PackagesForRepository(ctx context.Context, repo Repository, authenticatedUser string) ([]Package, []string, error) {
	owner, _, ok := strings.Cut(repo.FullName, "/")
	if !ok {
		return nil, nil, fmt.Errorf("invalid repository name %q", repo.FullName)
	}
	packageTypes := []string{"container", "npm", "maven", "rubygems", "nuget"}
	var out []Package
	var warnings []string
	for _, typ := range packageTypes {
		var base string
		if strings.EqualFold(owner, authenticatedUser) {
			base = "/user/packages"
		} else {
			base = "/orgs/" + url.PathEscape(owner) + "/packages"
		}
		q := url.Values{"package_type": {typ}, "per_page": {"100"}}
		raw, err := c.getAllPagesRaw(ctx, base+"?"+q.Encode())
		if err != nil {
			// Owner may be a user namespace rather than an organization.
			fallback := "/users/" + url.PathEscape(owner) + "/packages"
			raw, err = c.getAllPagesRaw(ctx, fallback+"?"+q.Encode())
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("packages %s: listing unavailable for %s: %v", typ, owner, err))
				continue
			}
			base = fallback
		}
		var pkgs []Package
		if err := json.Unmarshal(raw, &pkgs); err != nil {
			warnings = append(warnings, fmt.Sprintf("packages %s: decode: %v", typ, err))
			continue
		}
		for _, pkg := range pkgs {
			if pkg.Repository == nil || !strings.EqualFold(pkg.Repository.FullName, repo.FullName) {
				continue
			}
			versionsPath := base + "/" + url.PathEscape(pkg.PackageType) + "/" + url.PathEscape(pkg.Name) + "/versions"
			versions, err := c.getAllPagesRaw(ctx, versionsPath)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("package %s/%s versions: %v", pkg.PackageType, pkg.Name, err))
			} else if err := json.Unmarshal(versions, &pkg.Versions); err != nil {
				warnings = append(warnings, fmt.Sprintf("package %s/%s versions decode: %v", pkg.PackageType, pkg.Name, err))
			}
			out = append(out, pkg)
		}
	}
	return out, warnings, nil
}

func (c *Client) getAllPagesRaw(ctx context.Context, endpoint string) (json.RawMessage, error) {
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	var merged []json.RawMessage
	for page := 1; page <= c.pageLimit(); page++ {
		path := endpoint + sep + "per_page=100&page=" + strconv.Itoa(page)
		b, status, err := c.get(ctx, path)
		if err != nil {
			return nil, err
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("GitHub API %s returned %d", endpoint, status)
		}
		var arr []json.RawMessage
		if err := json.Unmarshal(b, &arr); err != nil {
			if page == 1 {
				return b, nil
			}
			return nil, err
		}
		merged = append(merged, arr...)
		if len(arr) < 100 {
			return json.MarshalIndent(merged, "", "  ")
		}
	}
	return nil, fmt.Errorf("GitHub API %s exceeded configured page limit %d", endpoint, c.pageLimit())
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, dst any) error {
	if q != nil && len(q) > 0 {
		path += "?" + q.Encode()
	}
	b, status, err := c.get(ctx, path)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("GitHub API %s returned %d: %s", path, status, strings.TrimSpace(string(b)))
	}
	return json.Unmarshal(b, dst)
}

func (c *Client) get(ctx context.Context, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, 0, err
	}
	c.headers(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return b, resp.StatusCode, err
}

func (c *Client) graphQL(ctx context.Context, payload any, dst any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.GraphQLURL, bytes.NewReader(b))
	if err != nil {
		return err
	}
	c.headers(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub GraphQL returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return json.Unmarshal(data, dst)
}

func (c *Client) headers(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", "RepoArk/0.2")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

func (c *Client) pageLimit() int {
	if c.MaxPages < 1 {
		return 100
	}
	return c.MaxPages
}

type Migration struct {
	ID    int64  `json:"id"`
	State string `json:"state"`
}

// ExportMigration creates an official GitHub migration archive without locking repositories.
// scope is "user" or "org:<name>". These legacy migration endpoints require a compatible
// classic/OAuth token; GitHub fine-grained PATs are not accepted by the API.
func (c *Client) ExportMigration(ctx context.Context, scope string, repositories []string, dst string, progress func(string)) error {
	if len(repositories) == 0 {
		return errors.New("migration export requires at least one repository")
	}
	if progress == nil {
		progress = func(string) {}
	}
	base, err := migrationBase(scope)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"repositories":        repositories,
		"lock_repositories":   false,
		"exclude_metadata":    false,
		"exclude_git_data":    false,
		"exclude_attachments": false,
		"exclude_releases":    false,
	})
	var mig Migration
	if err := c.doJSON(ctx, http.MethodPost, base, payload, &mig, http.StatusCreated); err != nil {
		return err
	}
	progress(fmt.Sprintf("migration %d started (%d repositories)", mig.ID, len(repositories)))
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("%s/%d", base, mig.ID), nil, &mig, http.StatusOK); err != nil {
			return err
		}
		progress(fmt.Sprintf("migration %d state=%s", mig.ID, mig.State))
		switch mig.State {
		case "exported":
			return c.downloadMigrationArchive(ctx, fmt.Sprintf("%s/%d/archive", base, mig.ID), dst)
		case "failed":
			return fmt.Errorf("GitHub migration %d failed", mig.ID)
		}
		t := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

func migrationBase(scope string) (string, error) {
	if scope == "user" {
		return "/user/migrations", nil
	}
	if strings.HasPrefix(scope, "org:") {
		org := strings.TrimSpace(strings.TrimPrefix(scope, "org:"))
		if org == "" {
			return "", errors.New("empty organization name")
		}
		return "/orgs/" + url.PathEscape(org) + "/migrations", nil
	}
	return "", fmt.Errorf("unsupported migration scope %q", scope)
}

func (c *Client) downloadMigrationArchive(ctx context.Context, path, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	c.headers(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("download migration archive returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	in, err := os.Open(dst)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, copyHashErr := io.Copy(h, in)
	closeHashErr := in.Close()
	if copyHashErr != nil {
		return copyHashErr
	}
	if closeHashErr != nil {
		return closeHashErr
	}
	sum := hex.EncodeToString(h.Sum(nil))
	return os.WriteFile(dst+".sha256", []byte(sum+"  "+filepath.Base(dst)+"\n"), 0o600)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body []byte, dst any, want int) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	c.headers(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != want {
		return fmt.Errorf("GitHub API %s returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if dst != nil {
		return json.Unmarshal(b, dst)
	}
	return nil
}
