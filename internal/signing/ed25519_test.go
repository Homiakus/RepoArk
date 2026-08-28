package signing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSignVerifyRoundTrip(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "manifest.key")
	priv, pub, err := EnsureKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatal(err)
	}
	payload := []byte("repoark-manifest")
	sig := Sign(priv, payload)
	pubPath := keyPath + ".pub"
	loadedPub, err := LoadPublic(pubPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(pub) != string(loadedPub) {
		t.Fatal("public key changed after reload")
	}
	if err := Verify(loadedPub, payload, sig); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := Verify(loadedPub, []byte("tampered"), sig); err == nil {
		t.Fatal("tampered payload unexpectedly verified")
	}
}
