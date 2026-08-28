package cas

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIngestDeduplicates(t *testing.T) {
	root := t.TempDir()
	s := New(filepath.Join(root, ".cas"), 1)
	a := filepath.Join(root, "a.bin")
	b := filepath.Join(root, "b.bin")
	data := []byte("same immutable payload")
	if err := os.WriteFile(a, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ra, err := s.Ingest(a)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := s.Ingest(b)
	if err != nil {
		t.Fatal(err)
	}
	if ra.SHA256 == "" || ra.SHA256 != rb.SHA256 {
		t.Fatalf("hash mismatch: %#v %#v", ra, rb)
	}
	if !rb.Existing {
		t.Fatal("second object should reuse CAS blob")
	}
	st, err := s.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if st.Objects != 1 {
		t.Fatalf("objects=%d", st.Objects)
	}
}

func TestVerifyDetectsTamper(t *testing.T) {
	root := t.TempDir()
	s := New(filepath.Join(root, ".cas"), 1)
	p := filepath.Join(root, "x.bin")
	_ = os.WriteFile(p, []byte("abc"), 0o600)
	r, err := s.Ingest(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.BlobPath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(); err == nil {
		t.Fatal("expected tamper detection")
	}
}

func TestGCRemovesOnlyUnreachable(t *testing.T) {
	root := t.TempDir()
	logical := filepath.Join(root, "backups")
	casRoot := filepath.Join(root, "cas")
	if err := os.MkdirAll(logical, 0o700); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(logical, "a.bin")
	b := filepath.Join(root, "orphan.bin")
	if err := os.WriteFile(a, []byte("reachable payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("orphan payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := New(casRoot, 0)
	ra, err := st.Ingest(a)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := st.Ingest(b)
	if err != nil {
		t.Fatal(err)
	}
	dry, err := st.GC([]string{logical}, true)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Removed != 1 || !dry.DryRun {
		t.Fatalf("unexpected dry-run: %+v", dry)
	}
	if _, err := os.Stat(rb.BlobPath); err != nil {
		t.Fatalf("dry-run removed object: %v", err)
	}
	got, err := st.GC([]string{logical}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Removed != 1 {
		t.Fatalf("removed=%d", got.Removed)
	}
	if _, err := os.Stat(ra.BlobPath); err != nil {
		t.Fatalf("reachable removed: %v", err)
	}
	if _, err := os.Stat(rb.BlobPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan remains: %v", err)
	}
}

func TestGCProtectedKeepsDurableObjectRef(t *testing.T) {
	root := t.TempDir()
	st := New(filepath.Join(root, "cas"), 0)
	p := filepath.Join(root, "shard.bin")
	if err := os.WriteFile(p, []byte("distributed erasure shard"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := st.Ingest(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	got, err := st.GCProtected(nil, false, map[string]struct{}{r.SHA256: {}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Removed != 0 {
		t.Fatalf("protected object removed: %+v", got)
	}
	if _, err := os.Stat(r.BlobPath); err != nil {
		t.Fatal(err)
	}
}
