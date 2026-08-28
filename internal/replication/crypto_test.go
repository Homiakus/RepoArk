package replication

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecryptAndTamper(t *testing.T) {
	d := t.TempDir()
	key := filepath.Join(d, "key")
	pub, err := EnsureKey(key)
	if err != nil {
		t.Fatal(err)
	}
	plain := bytes.Repeat([]byte("repoark-ha-"), 200000)
	var enc bytes.Buffer
	if err := Encrypt(&enc, bytes.NewReader(plain), pub); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Decrypt(&out, bytes.NewReader(enc.Bytes()), key); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, out.Bytes()) {
		t.Fatal("round trip mismatch")
	}
	bad := append([]byte(nil), enc.Bytes()...)
	bad[len(bad)-20] ^= 0x40
	if err := Decrypt(&bytes.Buffer{}, bytes.NewReader(bad), key); err == nil {
		t.Fatal("tamper was not rejected")
	}
	if info, err := os.Stat(key); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("private key permissions: %v %v", info, err)
	}
}

func TestArchiveRejectsTraversal(t *testing.T) {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	data := []byte("owned")
	if err := tw.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := ExtractGeneration(bytes.NewReader(b.Bytes()), dst); err == nil {
		t.Fatal("archive traversal was accepted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "escape")); !os.IsNotExist(err) {
		t.Fatalf("archive escaped destination: %v", err)
	}
}
