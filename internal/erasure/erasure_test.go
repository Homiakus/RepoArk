package erasure

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEncodeReconstructWithMissingShards(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "source.bin")
	data := bytes.Repeat([]byte("repoark-v07-erasure\x00"), 180000)
	if err := os.WriteFile(src, data, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(d, "ec")
	m, err := EncodeFile(src, dir, Config{DataShards: 4, ParityShards: 2, BlockBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, m.Shards[1].Name)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, m.Shards[4].Name)); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(d, "restored.bin")
	if err := Reconstruct(dir, out); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("reconstructed content mismatch")
	}
}
