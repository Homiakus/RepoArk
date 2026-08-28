package githubapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRepositoriesPagination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/repos" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		count := 100
		if page == 2 {
			count = 2
		}
		repos := make([]Repository, count)
		for i := range repos {
			repos[i] = Repository{ID: int64((page-1)*100 + i + 1), FullName: fmt.Sprintf("owner/repo-%03d", (page-1)*100+i+1)}
		}
		_ = json.NewEncoder(w).Encode(repos)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-token")
	repos, err := c.Repositories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 102 {
		t.Fatalf("expected 102 repositories, got %d", len(repos))
	}
}

func TestUserUsesBearerToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer abc" {
			t.Fatalf("unexpected Authorization: %q", got)
		}
		_, _ = w.Write([]byte(`{"login":"octocat"}`))
	}))
	defer srv.Close()

	u, err := New(srv.URL, "abc").User(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if u.Login != "octocat" {
		t.Fatalf("unexpected login %q", u.Login)
	}
}

func TestExportMigrationDownloadsArchiveAndChecksum(t *testing.T) {
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer migration-token" {
			sawAuth = true
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/user/migrations":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":42,"state":"pending"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/user/migrations/42":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":42,"state":"exported"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/user/migrations/42/archive":
			http.Redirect(w, r, "/download/migration-42.tar.gz", http.StatusFound)
		case r.Method == http.MethodGet && r.URL.Path == "/download/migration-42.tar.gz":
			_, _ = w.Write([]byte("official-github-export"))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "migration-token")
	dst := filepath.Join(t.TempDir(), "github-export.tar.gz")
	if err := c.ExportMigration(context.Background(), "user", []string{"octocat/repo"}, dst, nil); err != nil {
		t.Fatal(err)
	}
	if !sawAuth {
		t.Fatal("migration requests did not use bearer token")
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != "official-github-export" {
		t.Fatalf("archive content: %q err=%v", b, err)
	}
	if _, err := os.Stat(dst + ".sha256"); err != nil {
		t.Fatalf("checksum sidecar missing: %v", err)
	}
}
