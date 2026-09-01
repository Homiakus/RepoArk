package gitlab

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/Homiakus/repoark/internal/config"
)

type archiveTestEntry struct {
	name     string
	typeflag byte
	body     string
	linkname string
}

func writeArchiveForTest(t *testing.T, entries []archiveTestEntry) string {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "repoark-gitlab-test.tar.gz")
	f, err := os.OpenFile(archive, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		h := &tar.Header{Name: entry.name, Typeflag: entry.typeflag, Mode: 0o600, Linkname: entry.linkname}
		if entry.typeflag == tar.TypeDir {
			h.Mode = 0o700
		} else if entry.typeflag == tar.TypeReg || entry.typeflag == tar.TypeRegA {
			h.Size = int64(len(entry.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			if _, err := tw.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return archive
}

func TestExtractGitLabArchiveAcceptsExpectedLayout(t *testing.T) {
	archive := writeArchiveForTest(t, []archiveTestEntry{
		{name: "config", typeflag: tar.TypeDir},
		{name: "config/gitlab.rb", typeflag: tar.TypeReg, body: "external_url 'http://gitlab.test'\n"},
		{name: "data", typeflag: tar.TypeDir},
		{name: "data/backups", typeflag: tar.TypeDir},
		{name: "data/backups/123_gitlab_backup.tar", typeflag: tar.TypeReg, body: "backup"},
	})
	dest := t.TempDir()
	if err := extractGitLabArchive(archive, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "config", "gitlab.rb"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "external_url 'http://gitlab.test'\n" {
		t.Fatalf("unexpected extracted config: %q", got)
	}
}

func TestExtractGitLabArchiveRejectsTraversal(t *testing.T) {
	archive := writeArchiveForTest(t, []archiveTestEntry{{name: "../escape", typeflag: tar.TypeReg, body: "owned"}})
	root := t.TempDir()
	dest := filepath.Join(root, "restore")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := extractGitLabArchive(archive, dest); err == nil {
		t.Fatal("expected traversal archive to be rejected")
	}
	if _, err := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
		t.Fatalf("traversal created a file outside restore root: %v", err)
	}
}

func TestExtractGitLabArchiveRejectsLinks(t *testing.T) {
	archive := writeArchiveForTest(t, []archiveTestEntry{{name: "config/escape", typeflag: tar.TypeSymlink, linkname: "../../escape"}})
	if err := extractGitLabArchive(archive, t.TempDir()); err == nil {
		t.Fatal("expected symlink entry to be rejected")
	}
}

func TestVerifyGitLabBackupArchiveFailsClosedWithoutMetadata(t *testing.T) {
	archive := writeArchiveForTest(t, []archiveTestEntry{{name: "config/gitlab.rb", typeflag: tar.TypeReg, body: "x"}})
	if err := verifyGitLabBackupArchive(archive, "gitlab/gitlab-ce:test"); err == nil {
		t.Fatal("expected missing metadata to fail closed")
	}
}

func TestVerifyGitLabBackupArchiveAcceptsGeneratedMetadata(t *testing.T) {
	archive := writeArchiveForTest(t, []archiveTestEntry{{name: "config/gitlab.rb", typeflag: tar.TypeReg, body: "x"}})
	cfg := config.Default()
	cfg.GitLab.DataDir = t.TempDir()
	m := New(cfg)
	if err := m.writeBackupMeta(archive); err != nil {
		t.Fatal(err)
	}
	if err := verifyGitLabBackupArchive(archive, cfg.GitLab.Image); err != nil {
		t.Fatal(err)
	}
}
