package backup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/manifest"
)

func TestLFSArchiveRoundTrip(t *testing.T) {
	src := t.TempDir()
	want := []byte("repoark-lfs-payload")
	object := filepath.Join(src, "objects", "aa", "bb", "object")
	if err := os.MkdirAll(filepath.Dir(object), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(object, want, 0o600); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(t.TempDir(), "lfs.tar.gz")
	if err := archiveDirGzip(src, archive); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if sum, err := sha256File(archive); err != nil || sum == "" {
		t.Fatalf("checksum: sum=%q err=%v", sum, err)
	}

	dst := t.TempDir()
	if err := extractTarGzip(archive, dst); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "objects", "aa", "bb", "object"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("payload mismatch: got %q want %q", got, want)
	}
}

func TestResolveArtifactPath(t *testing.T) {
	root := t.TempDir()
	got := resolveArtifactPath(root, "bundles/acme/repo.bundle")
	want := filepath.Join(root, "bundles", "acme", "repo.bundle")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	absolute := filepath.Join(root, "legacy.bundle")
	if runtime.GOOS == "windows" {
		absolute, _ = filepath.Abs(absolute)
	}
	if got := resolveArtifactPath(root, absolute); got != absolute {
		t.Fatalf("legacy absolute path changed: got %q want %q", got, absolute)
	}
}

func TestRestoreFromRelativeBundle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	ctx := context.Background()
	work := t.TempDir()
	src := filepath.Join(work, "src")
	mustGit(t, "", "init", src)
	mustGit(t, src, "config", "user.email", "repoark@example.invalid")
	mustGit(t, src, "config", "user.name", "RepoArk Test")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("recovery works\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, src, "add", "README.md")
	mustGit(t, src, "commit", "-m", "seed")

	root := filepath.Join(work, "backup")
	mirrorRel := filepath.Join("mirrors", "acme", "demo.git")
	bundleRel := filepath.Join("bundles", "acme", "demo.bundle")
	mirror := filepath.Join(root, mirrorRel)
	bundle := filepath.Join(root, bundleRel)
	if err := os.MkdirAll(filepath.Dir(bundle), 0o700); err != nil {
		t.Fatal(err)
	}
	mustGit(t, "", "clone", "--mirror", src, mirror)
	mustGit(t, mirror, "bundle", "create", bundle, "--all")
	sum, err := sha256File(bundle)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manifest.Write(root, manifest.Manifest{
		Version:    1,
		StartedAt:  time.Now().UTC(),
		GitHubUser: "acme",
		Repositories: []manifest.RepoResult{{
			FullName:     "acme/demo",
			MirrorPath:   filepath.ToSlash(mirrorRel),
			BundlePath:   filepath.ToSlash(bundleRel),
			BundleSHA256: sum,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Backup.Root = root
	target := filepath.Join(work, "restored")
	if err := New(cfg).Restore(ctx, "acme/demo", target); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(target, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "recovery works\n" {
		t.Fatalf("unexpected restored content: %q", got)
	}
}

func TestRestoreEmptyRepositoryFromMirror(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	work := t.TempDir()
	root := filepath.Join(work, "backup")
	mirrorRel := filepath.Join("mirrors", "acme", "empty.git")
	mirror := filepath.Join(root, mirrorRel)
	if err := os.MkdirAll(filepath.Dir(mirror), 0o700); err != nil {
		t.Fatal(err)
	}
	mustGit(t, "", "init", "--bare", mirror)
	_, err := manifest.Write(root, manifest.Manifest{
		Version:   1,
		StartedAt: time.Now().UTC(),
		Repositories: []manifest.RepoResult{{
			FullName:   "acme/empty",
			MirrorPath: filepath.ToSlash(mirrorRel),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Backup.Root = root
	target := filepath.Join(work, "restored-empty")
	if err := New(cfg).Restore(context.Background(), "acme/empty", target); err != nil {
		t.Fatalf("restore empty: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		t.Fatalf("restored empty repo missing .git: %v", err)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestRecoveryDrillRestoresAndChecksRefs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	ctx := context.Background()
	work := t.TempDir()
	src := filepath.Join(work, "src")
	mustGit(t, "", "init", src)
	mustGit(t, src, "config", "user.email", "repoark@example.invalid")
	mustGit(t, src, "config", "user.name", "RepoArk Test")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("drill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, src, "add", "README.md")
	mustGit(t, src, "commit", "-m", "seed")
	mustGit(t, src, "tag", "v1")

	root := filepath.Join(work, "backup")
	mirrorRel := filepath.Join("mirrors", "acme", "drill.git")
	bundleRel := filepath.Join("bundles", "acme", "drill.bundle")
	mirror := filepath.Join(root, mirrorRel)
	bundle := filepath.Join(root, bundleRel)
	if err := os.MkdirAll(filepath.Dir(bundle), 0o700); err != nil {
		t.Fatal(err)
	}
	mustGit(t, "", "clone", "--mirror", src, mirror)
	mustGit(t, mirror, "bundle", "create", bundle, "--all")
	sum, err := sha256File(bundle)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manifest.Write(root, manifest.Manifest{
		Version:   2,
		StartedAt: time.Now().UTC(),
		Succeeded: 1,
		Repositories: []manifest.RepoResult{{
			FullName:     "acme/drill",
			MirrorPath:   filepath.ToSlash(mirrorRel),
			BundlePath:   filepath.ToSlash(bundleRel),
			BundleSHA256: sum,
			Verified:     true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Backup.Root = root
	cfg.Security.SignManifests = false
	cfg.RecoveryDrill.WorkDir = filepath.Join(work, "drills")
	cfg.RecoveryDrill.VerifyRefs = true
	report, err := New(cfg).Drill(ctx, 1, nil)
	if err != nil {
		t.Fatalf("drill: %v", err)
	}
	if report.Succeeded != 1 || report.Failed != 0 {
		t.Fatalf("unexpected drill report: %+v", report)
	}
}

func TestVerifySHA256Sidecars(t *testing.T) {
	root := t.TempDir()
	artifact := filepath.Join(root, "export.tar.gz")
	if err := os.WriteFile(artifact, []byte("official-export"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, err := sha256File(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact+".sha256", []byte(sum+"  export.tar.gz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifySHA256Sidecars(root); err != nil {
		t.Fatalf("valid sidecar rejected: %v", err)
	}
	if err := os.WriteFile(artifact, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifySHA256Sidecars(root); err == nil {
		t.Fatal("tampered artifact unexpectedly passed checksum verification")
	}
}
