package generation

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/manifest"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

func TestGenerationPreservesPointInTimeBundle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	ctx := context.Background()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, work, "init")
	git(t, work, "config", "user.email", "test@example.com")
	git(t, work, "config", "user.name", "RepoArk Test")
	if err := os.WriteFile(filepath.Join(work, "value.txt"), []byte("v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "value.txt")
	git(t, work, "commit", "-m", "v1")
	backupRoot := filepath.Join(root, "backup")
	mirror := filepath.Join(backupRoot, "mirrors", "acme", "demo.git")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, "", "clone", "--mirror", work, mirror)
	bundle := filepath.Join(backupRoot, "bundles", "acme", "demo.bundle")
	if err := os.MkdirAll(filepath.Dir(bundle), 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, mirror, "bundle", "create", bundle, "--all")
	sum, err := fileSHA256(bundle)
	if err != nil {
		t.Fatal(err)
	}
	res := manifest.RepoResult{FullName: "acme/demo", MirrorPath: "mirrors/acme/demo.git", BundlePath: "bundles/acme/demo.bundle", BundleSHA256: sum, Verified: true, UpdatedAt: time.Now().UTC()}
	genRoot := filepath.Join(root, "generations")
	meta, err := Capture(backupRoot, genRoot, res, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "value.txt"), []byte("v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "value.txt")
	git(t, work, "commit", "-m", "v2")
	git(t, mirror, "remote", "update", "--prune")
	tmp := bundle + ".tmp"
	git(t, mirror, "bundle", "create", tmp, "--all")
	if err := os.Rename(tmp, bundle); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "restore")
	if err := Restore(ctx, genRoot, "acme/demo", meta.ID, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(target, "value.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v1\n" {
		t.Fatalf("point-in-time restore changed: %q", got)
	}
}

func TestEmptyRepositoryGenerationUsesMirrorArchive(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	ctx := context.Background()
	root := t.TempDir()
	backup := filepath.Join(root, "backup")
	mirror := filepath.Join(backup, "mirrors", "acme", "empty.git")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, "", "init", "--bare", mirror)
	m, err := Capture(backup, filepath.Join(root, "gens"), manifest.RepoResult{FullName: "acme/empty", MirrorPath: "mirrors/acme/empty.git", Verified: true}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if m.MirrorArchive == "" {
		t.Fatal("expected mirror archive")
	}
	target := filepath.Join(root, "restored")
	if err := Restore(ctx, filepath.Join(root, "gens"), "acme/empty", m.ID, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		t.Fatal(err)
	}
}

func TestSignedGenerationRejectsMetadataReplacement(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	backup := filepath.Join(root, "backup")
	mirror := filepath.Join(backup, "mirrors", "acme", "empty.git")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, "", "init", "--bare", mirror)
	key := filepath.Join(root, "keys", "manifest.key")
	m, err := CaptureSigned(backup, filepath.Join(root, "gens"), manifest.RepoResult{FullName: "acme/empty", MirrorPath: "mirrors/acme/empty.git", Verified: true}, 5, key)
	if err != nil {
		t.Fatal(err)
	}
	_, dir, err := Find(filepath.Join(root, "gens"), "acme/empty", m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySignature(dir, key+".pub"); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(dir, "generation.json")
	b, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, []byte("\n ")...)
	if err := os.WriteFile(metaPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifySignature(dir, key+".pub"); err == nil {
		t.Fatal("expected tampered generation signature failure")
	}
}
