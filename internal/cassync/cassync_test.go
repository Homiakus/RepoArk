package cassync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Homiakus/repoark/internal/cas"
)

func TestArchiveExtract(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	data := bytes.Repeat([]byte("cas-object"), 1000)
	sum := sha256.Sum256(data)
	d := hex.EncodeToString(sum[:])
	p := filepath.Join(src, "sha256", d[:2], d)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	_, err := Archive(src, &b, []string{d[:2]}, []string{"a", "b"}, 2, "b")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Extract(&b, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Objects) != 1 {
		t.Fatalf("objects=%d", len(m.Objects))
	}
	got, err := os.ReadFile(filepath.Join(dst, "sha256", d[:2], d))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("content mismatch")
	}
}

func TestArchiveDigestsOnlyRequestedObjects(t *testing.T) {
	root := t.TempDir()
	casRoot := filepath.Join(root, "cas")
	st := cas.New(casRoot, 0)
	var digests []string
	for _, body := range []string{"alpha", "beta", "gamma"} {
		p := filepath.Join(root, body+".bin")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		r, err := st.Ingest(p)
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, r.SHA256)
	}
	var buf bytes.Buffer
	m, err := ArchiveDigests(casRoot, &buf, []string{digests[1]})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Objects) != 1 || m.Objects[0].Digest != digests[1] {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	dst := filepath.Join(root, "dst")
	if _, err := Extract(bytes.NewReader(buf.Bytes()), dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "sha256", digests[1][:2], digests[1])); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "sha256", digests[0][:2], digests[0])); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected extra object: %v", err)
	}
}
