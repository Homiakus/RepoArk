package backup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/githubapi"
)

func TestPackageVersionsDeduplicatesAndSorts(t *testing.T) {
	pkg := githubapi.Package{Versions: []json.RawMessage{json.RawMessage(`{"name":"2.0.0"}`), json.RawMessage(`{"name":"1.0.0"}`), json.RawMessage(`{"name":"2.0.0"}`)}}
	got := packageVersions(pkg)
	if strings.Join(got, ",") != "1.0.0,2.0.0" {
		t.Fatalf("versions=%v", got)
	}
}

func TestArchiveNPMDownloadsPayloadAndChecksum(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/%40acme%2fpkg" || r.URL.Path == "/@acme%2fpkg" || r.URL.Path == "/@acme/pkg" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"versions":{"1.0.0":{"dist":{"tarball":"` + srv.URL + `/payload.tgz"}}}}`))
			return
		}
		if r.URL.Path == "/payload.tgz" {
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				t.Fatalf("missing bearer auth")
			}
			_, _ = w.Write([]byte("npm-payload"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	cfg := config.Default()
	cfg.Backup.Root = t.TempDir()
	cfg.Packages.Enabled = true
	cfg.Packages.NPMRegistry = srv.URL
	cfg.Packages.MaxBytes = 1024
	client := githubapi.New(srv.URL, "token")
	m := &Manager{Config: cfg, GitHub: client, Token: "token", User: "acme"}
	pkg := githubapi.Package{Name: "pkg", PackageType: "npm"}
	n, records, warnings := m.archiveNPM(context.Background(), "acme", "repo", pkg, []string{"1.0.0"})
	if len(warnings) != 0 || n != 1 || len(records) != 1 {
		t.Fatalf("n=%d records=%v warnings=%v", n, records, warnings)
	}
	path := filepath.Join(cfg.Backup.Root, filepath.FromSlash(records[0].Path))
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".sha256"); err != nil {
		t.Fatal(err)
	}
}

func TestRedactSecret(t *testing.T) {
	got := redactSecret("failed https://user:very-secret@example.test", "very-secret")
	if strings.Contains(got, "very-secret") || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("not redacted: %s", got)
	}
}
