package objectinventory

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func put(t *testing.T, root string, b []byte) string {
	t.Helper()
	s := sha256.Sum256(b)
	h := hex.EncodeToString(s[:])
	p := filepath.Join(root, "sha256", h[:2], h)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestInventoryDeterministicAndDiff(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	put(t, a, []byte("alpha"))
	put(t, a, []byte("beta"))
	put(t, b, []byte("alpha"))
	put(t, b, []byte("beta"))
	ia, err := Build(a)
	if err != nil {
		t.Fatal(err)
	}
	ib, err := Build(b)
	if err != nil {
		t.Fatal(err)
	}
	if ia.MerkleRoot != ib.MerkleRoot || ia.Objects != 2 {
		t.Fatalf("unexpected inventory: %#v %#v", ia, ib)
	}
	put(t, b, []byte("gamma"))
	ib, err = Build(b)
	if err != nil {
		t.Fatal(err)
	}
	if ia.MerkleRoot == ib.MerkleRoot {
		t.Fatal("expected different Merkle root")
	}
	if len(DivergentPrefixes(ia, ib)) == 0 {
		t.Fatal("expected divergent prefix")
	}
}
