package observability

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/manifest"
	"github.com/Homiakus/repoark/internal/webauth"
)

func TestHealthReflectsLatestManifest(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Backup.Root = root
	cfg.Policy.Enabled = false
	if _, err := manifest.Write(root, manifest.Manifest{
		Version:   2,
		StartedAt: time.Now().UTC(),
		EndedAt:   time.Now().UTC(),
		Succeeded: 1,
		Repositories: []manifest.RepoResult{{
			FullName: "acme/repo",
			Verified: true,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	s := New(cfg)
	rr := httptest.NewRecorder()
	s.health(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestWebRestoreAllowlistMatchesOIDCSubjectOrEmail(t *testing.T) {
	id := webauth.Identity{Subject: "oidc-subject-123", Email: "alice@example.com"}
	if !webIdentityAllowed(id, nil) {
		t.Fatal("empty allowlist must allow role-authorized identity")
	}
	if !webIdentityAllowed(id, []string{"OIDC-SUBJECT-123"}) {
		t.Fatal("subject allowlist did not match")
	}
	if !webIdentityAllowed(id, []string{"ALICE@example.com"}) {
		t.Fatal("email allowlist did not match")
	}
	if webIdentityAllowed(id, []string{"bob@example.com"}) {
		t.Fatal("unlisted identity accepted")
	}
}
