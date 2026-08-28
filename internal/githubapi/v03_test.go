package githubapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActionArtifactsDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/actions/artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 1, "artifacts": []map[string]any{{"id": 7, "name": "build", "size_in_bytes": 3, "expired": false}}})
		case "/repos/o/r/actions/artifacts/7/zip":
			_, _ = w.Write([]byte("zip"))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "token")
	arts, err := c.ActionArtifacts(context.Background(), "o/r")
	if err != nil || len(arts) != 1 {
		t.Fatalf("artifacts=%v err=%v", arts, err)
	}
	dst := filepath.Join(t.TempDir(), "a.zip")
	if err := c.DownloadActionArtifact(context.Background(), "o/r", arts[0], dst, 10); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dst)
	if string(b) != "zip" {
		t.Fatalf("got %q", b)
	}
}

func TestProjectsV2ForOwner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(body.Query, "projectsV2(first:50") {
			_, _ = w.Write([]byte(`{"data":{"user":{"projectsV2":{"nodes":[{"id":"P1","number":1,"title":"Roadmap","public":false,"closed":false}],"pageInfo":{"hasNextPage":false,"endCursor":null}}},"organization":null}}`))
			return
		}
		if strings.Contains(body.Query, "items(first:100") {
			_, _ = w.Write([]byte(`{"data":{"node":{"items":{"nodes":[{"id":"I1","type":"ISSUE","isArchived":false,"content":{"__typename":"Issue","number":2,"title":"Task","state":"OPEN","repository":{"nameWithOwner":"o/r"}}}],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}`))
			return
		}
		t.Fatalf("unexpected GraphQL query: %s", body.Query)
	}))
	defer srv.Close()
	c := New(srv.URL, "token")
	c.GraphQLURL = srv.URL
	raw, err := c.ProjectsV2ForOwner(context.Background(), "octocat")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Roadmap") || !strings.Contains(string(raw), "Task") {
		t.Fatalf("unexpected export: %s", raw)
	}
}
