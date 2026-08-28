package tiering

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/cas"
)

func TestCopyTierKeepsHotAndCreatesVerifiedColdCopy(t *testing.T) {
	root := t.TempDir()
	hot := cas.New(filepath.Join(root, "hot"), 0)
	src := filepath.Join(root, "x.bin")
	if err := os.WriteFile(src, []byte("tier payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := hot.Ingest(src)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(r.BlobPath, old, old); err != nil {
		t.Fatal(err)
	}
	cold := filepath.Join(root, "cold")
	got, err := CopyTier(context.Background(), hot, Config{ColdRoot: cold, MinAge: 24 * time.Hour, MinBytes: 1}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.CopiedLocal != 1 {
		t.Fatalf("result=%+v", got)
	}
	if _, err := os.Stat(r.BlobPath); err != nil {
		t.Fatalf("hot removed: %v", err)
	}
	dst := filepath.Join(cold, "sha256", r.SHA256[:2], r.SHA256)
	h, _, err := cas.HashFile(dst)
	if err != nil || h != r.SHA256 {
		t.Fatalf("cold invalid hash=%s err=%v", h, err)
	}
}
