package githubapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ActionArtifact struct {
	ID                 int64     `json:"id"`
	NodeID             string    `json:"node_id,omitempty"`
	Name               string    `json:"name"`
	SizeInBytes        int64     `json:"size_in_bytes"`
	URL                string    `json:"url,omitempty"`
	ArchiveDownloadURL string    `json:"archive_download_url,omitempty"`
	Expired            bool      `json:"expired"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	ExpiresAt          time.Time `json:"expires_at"`
	WorkflowRun        any       `json:"workflow_run,omitempty"`
}

func (c *Client) ActionArtifacts(ctx context.Context, fullName string) ([]ActionArtifact, error) {
	var out []ActionArtifact
	for page := 1; page <= c.pageLimit(); page++ {
		q := url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}
		var resp struct {
			TotalCount int              `json:"total_count"`
			Artifacts  []ActionArtifact `json:"artifacts"`
		}
		if err := c.getJSON(ctx, "/repos/"+fullName+"/actions/artifacts", q, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Artifacts...)
		if len(resp.Artifacts) < 100 || len(out) >= resp.TotalCount {
			return out, nil
		}
	}
	return out, fmt.Errorf("actions artifact listing exceeded configured page limit %d", c.pageLimit())
}

func (c *Client) DownloadActionArtifact(ctx context.Context, fullName string, artifact ActionArtifact, dst string, maxBytes int64) error {
	if artifact.Expired {
		return fmt.Errorf("artifact %d (%s) is expired", artifact.ID, artifact.Name)
	}
	if maxBytes > 0 && artifact.SizeInBytes > maxBytes {
		return fmt.Errorf("artifact %s is %d bytes and exceeds limit %d", artifact.Name, artifact.SizeInBytes, maxBytes)
	}
	path := fmt.Sprintf("/repos/%s/actions/artifacts/%d/zip", fullName, artifact.ID)
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
	if resp.StatusCode == http.StatusGone {
		return fmt.Errorf("artifact %d expired while downloading", artifact.ID)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("artifact %d returned %d: %s", artifact.ID, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	limit := maxBytes
	if limit <= 0 {
		limit = 1<<63 - 2
	}
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
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
		return fmt.Errorf("artifact %s exceeded configured size limit while downloading", artifact.Name)
	}
	return os.Rename(tmp, dst)
}

// ProjectsV2ForOwner exports project metadata plus all project items for a
// user or organization. Field values are deliberately preserved only where
// the public GraphQL schema offers stable, typed fields; project/item identity
// and linked issue/PR/draft content are always captured.
func (c *Client) ProjectsV2ForOwner(ctx context.Context, login string) (json.RawMessage, error) {
	login = strings.TrimSpace(login)
	if login == "" {
		return nil, errors.New("project owner login is empty")
	}
	query := `query($login:String!,$cursor:String){
  user(login:$login){projectsV2(first:50,after:$cursor){nodes{id number title shortDescription readme public closed url updatedAt} pageInfo{hasNextPage endCursor}}}
  organization(login:$login){projectsV2(first:50,after:$cursor){nodes{id number title shortDescription readme public closed url updatedAt} pageInfo{hasNextPage endCursor}}}
}`
	type project struct {
		ID               string    `json:"id"`
		Number           int       `json:"number"`
		Title            string    `json:"title"`
		ShortDescription string    `json:"shortDescription,omitempty"`
		Readme           string    `json:"readme,omitempty"`
		Public           bool      `json:"public"`
		Closed           bool      `json:"closed"`
		URL              string    `json:"url,omitempty"`
		UpdatedAt        time.Time `json:"updatedAt,omitempty"`
		Items            any       `json:"items,omitempty"`
	}
	type conn struct {
		Nodes    []project `json:"nodes"`
		PageInfo struct {
			HasNextPage bool    `json:"hasNextPage"`
			EndCursor   *string `json:"endCursor"`
		} `json:"pageInfo"`
	}
	var projects []project
	ownerType := ""
	var cursor any
	for page := 1; page <= c.pageLimit(); page++ {
		payload := map[string]any{"query": query, "variables": map[string]any{"login": login, "cursor": cursor}}
		var resp struct {
			Data struct {
				User *struct {
					Projects conn `json:"projectsV2"`
				} `json:"user"`
				Organization *struct {
					Projects conn `json:"projectsV2"`
				} `json:"organization"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := c.graphQL(ctx, payload, &resp); err != nil {
			return nil, err
		}
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("GitHub GraphQL projects: %s", resp.Errors[0].Message)
		}
		var current conn
		switch {
		case resp.Data.Organization != nil:
			ownerType = "organization"
			current = resp.Data.Organization.Projects
		case resp.Data.User != nil:
			ownerType = "user"
			current = resp.Data.User.Projects
		default:
			return nil, fmt.Errorf("GitHub Projects owner %q not found or inaccessible", login)
		}
		projects = append(projects, current.Nodes...)
		if !current.PageInfo.HasNextPage {
			break
		}
		if current.PageInfo.EndCursor == nil {
			return nil, errors.New("GitHub Projects pagination missing end cursor")
		}
		cursor = *current.PageInfo.EndCursor
		if page == c.pageLimit() {
			return nil, fmt.Errorf("projects listing exceeded configured page limit %d", c.pageLimit())
		}
	}
	for i := range projects {
		items, err := c.projectV2Items(ctx, projects[i].ID)
		if err != nil {
			projects[i].Items = map[string]any{"warning": err.Error()}
			continue
		}
		projects[i].Items = items
	}
	return json.MarshalIndent(map[string]any{"owner": login, "owner_type": ownerType, "projects": projects}, "", "  ")
}

func (c *Client) projectV2Items(ctx context.Context, id string) ([]json.RawMessage, error) {
	query := `query($id:ID!,$cursor:String){node(id:$id){... on ProjectV2{items(first:100,after:$cursor){nodes{
  id type isArchived
  content{__typename
    ... on DraftIssue{title body}
    ... on Issue{number title body url state repository{nameWithOwner}}
    ... on PullRequest{number title body url state repository{nameWithOwner}}
  }
} pageInfo{hasNextPage endCursor}}}}}`
	var out []json.RawMessage
	var cursor any
	for page := 1; page <= c.pageLimit(); page++ {
		payload := map[string]any{"query": query, "variables": map[string]any{"id": id, "cursor": cursor}}
		var resp struct {
			Data struct {
				Node *struct {
					Items struct {
						Nodes    []json.RawMessage `json:"nodes"`
						PageInfo struct {
							HasNextPage bool    `json:"hasNextPage"`
							EndCursor   *string `json:"endCursor"`
						} `json:"pageInfo"`
					} `json:"items"`
				} `json:"node"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := c.graphQL(ctx, payload, &resp); err != nil {
			return nil, err
		}
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("GitHub GraphQL project items: %s", resp.Errors[0].Message)
		}
		if resp.Data.Node == nil {
			return nil, errors.New("GitHub project node not found")
		}
		out = append(out, resp.Data.Node.Items.Nodes...)
		if !resp.Data.Node.Items.PageInfo.HasNextPage {
			return out, nil
		}
		if resp.Data.Node.Items.PageInfo.EndCursor == nil {
			return nil, errors.New("GitHub project items pagination missing end cursor")
		}
		cursor = *resp.Data.Node.Items.PageInfo.EndCursor
	}
	return nil, fmt.Errorf("project item listing exceeded configured page limit %d", c.pageLimit())
}
