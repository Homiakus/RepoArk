package manifest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAndReadLatest(t *testing.T) {
	root := t.TempDir()
	m := Manifest{
		Version:      1,
		StartedAt:    time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC),
		EndedAt:      time.Date(2026, 8, 19, 12, 1, 0, 0, time.UTC),
		GitHubUser:   "octocat",
		Succeeded:    1,
		Repositories: []RepoResult{{FullName: "octocat/hello", Verified: true}},
	}
	path, err := Write(root, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLatest(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.GitHubUser != "octocat" || len(got.Repositories) != 1 {
		t.Fatalf("unexpected manifest: %+v", got)
	}
	if filepath.Base(path) != "20260819T120000Z.json" {
		t.Fatalf("unexpected manifest filename: %s", path)
	}
}

func TestPrune(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "manifests")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"20260817T000000Z.json", "20260818T000000Z.json", "20260819T000000Z.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "latest.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Prune(root, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "20260817T000000Z.json")); !os.IsNotExist(err) {
		t.Fatalf("oldest manifest should be pruned, stat err=%v", err)
	}
}

func TestWriteSignedAndDetectTampering(t *testing.T) {
	root := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "manifest.key")
	m := Manifest{
		Version:    2,
		StartedAt:  time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
		EndedAt:    time.Date(2026, 8, 20, 12, 1, 0, 0, time.UTC),
		GitHubUser: "octocat",
		Succeeded:  1,
	}
	path, err := WriteSigned(root, m, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyLatestSignature(root, keyPath+".pub"); err != nil {
		t.Fatalf("verify signed manifest: %v", err)
	}
	if _, err := os.Stat(path + ".sig"); err != nil {
		t.Fatalf("missing detached signature: %v", err)
	}
	latest := filepath.Join(root, "manifests", "latest.json")
	b, err := os.ReadFile(latest)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	b = append(b, []byte("tamper")...)
	if err := os.WriteFile(latest, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyLatestSignature(root, keyPath+".pub"); err == nil {
		t.Fatal("tampered latest manifest unexpectedly verified")
	}
}

func TestPinnedTrustAnchorRejectsManifestResignedWithAttackerKey(t *testing.T) {
	root := t.TempDir()
	trustedKey := filepath.Join(t.TempDir(), "trusted.key")
	attackerKey := filepath.Join(t.TempDir(), "attacker.key")
	m := Manifest{
		Version:    4,
		StartedAt:  time.Date(2026, 8, 20, 13, 0, 0, 0, time.UTC),
		EndedAt:    time.Date(2026, 8, 20, 13, 1, 0, 0, time.UTC),
		GitHubUser: "trusted-owner",
		Succeeded:  1,
	}
	if _, err := WriteSigned(root, m, trustedKey); err != nil {
		t.Fatal(err)
	}
	if err := VerifyLatestSignature(root, trustedKey+".pub"); err != nil {
		t.Fatalf("trusted manifest should verify: %v", err)
	}

	// Simulate an attacker replacing the whole transport set: manifest,
	// detached signature, and the convenience public key inside the backup.
	m.GitHubUser = "attacker-rewrite"
	if _, err := WriteSigned(root, m, attackerKey); err != nil {
		t.Fatal(err)
	}
	if err := VerifyLatestSignature(root, trustedKey+".pub"); err == nil {
		t.Fatal("manifest resigned with attacker key unexpectedly verified against pinned trust anchor")
	}
}
