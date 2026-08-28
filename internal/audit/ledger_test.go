package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLedgerDetectsTampering(t *testing.T) {
	p := filepath.Join(t.TempDir(), "audit.jsonl")
	if _, err := Append(p, "backup", "a/b", "ok", "first", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(p, "verify", "a/b", "ok", "second", map[string]any{"n": 2}); err != nil {
		t.Fatal(err)
	}
	if n, err := Verify(p); err != nil || n != 2 {
		t.Fatalf("verify n=%d err=%v", n, err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.Replace(string(b), "second", "changed", 1))
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(p); err == nil {
		t.Fatal("expected tamper detection")
	}
}

func TestSignedCheckpointDetectsLedgerRewrite(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "audit.jsonl")
	key := filepath.Join(root, "audit.key")
	if _, err := Append(p, "backup", "a/b", "ok", "first", nil); err != nil {
		t.Fatal(err)
	}
	if err := WriteCheckpoint(p, key); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCheckpoint(p, key+".pub"); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(p, "verify", "a/b", "ok", "new head", nil); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCheckpoint(p, key+".pub"); err == nil {
		t.Fatal("stale signed checkpoint unexpectedly accepted")
	}
}

func TestPinnedTrustAnchorRejectsRewrittenLedgerCheckpoint(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "audit.jsonl")
	trustedKey := filepath.Join(t.TempDir(), "trusted.key")
	attackerKey := filepath.Join(t.TempDir(), "attacker.key")
	if _, err := Append(p, "backup", "a/b", "ok", "trusted", nil); err != nil {
		t.Fatal(err)
	}
	if err := WriteCheckpoint(p, trustedKey); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCheckpoint(p, trustedKey+".pub"); err != nil {
		t.Fatalf("trusted checkpoint should verify: %v", err)
	}

	// Rewrite the ledger head and legitimately sign it with a different key.
	// Replacing ledger-local checkpoint.pub is not enough: verification is pinned
	// to the external trust anchor.
	if _, err := Append(p, "verify", "a/b", "ok", "attacker rewrite", nil); err != nil {
		t.Fatal(err)
	}
	if err := WriteCheckpoint(p, attackerKey); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCheckpoint(p, trustedKey+".pub"); err == nil {
		t.Fatal("checkpoint signed with attacker key unexpectedly verified against pinned trust anchor")
	}
}
