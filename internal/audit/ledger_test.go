package audit

import (
	"encoding/json"
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

func TestAppendRefusesToExtendTamperedLedger(t *testing.T) {
	p := filepath.Join(t.TempDir(), "audit.jsonl")
	if _, err := Append(p, "backup", "a/b", "ok", "trusted", nil); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	tampered := []byte(strings.Replace(string(before), "trusted", "altered", 1))
	if err := os.WriteFile(p, tampered, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Append(p, "verify", "a/b", "ok", "must not append", nil); err == nil {
		t.Fatal("append unexpectedly extended a tampered audit ledger")
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(tampered) {
		t.Fatal("failed append modified the tampered ledger")
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

func TestAppendWithCheckpointPinsExactlyReturnedHead(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "audit.jsonl")
	key := filepath.Join(root, "audit.key")

	r, err := AppendWithCheckpoint(p, key, "backup", "a/b", "ok", "atomic", map[string]any{"request_id": "r-1"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p + ".checkpoint.json")
	if err != nil {
		t.Fatal(err)
	}
	var cp Checkpoint
	if err := json.Unmarshal(b, &cp); err != nil {
		t.Fatal(err)
	}
	if cp.Seq != r.Seq || !strings.EqualFold(cp.Hash, r.Hash) {
		t.Fatalf("checkpoint head=%d/%s want appended=%d/%s", cp.Seq, cp.Hash, r.Seq, r.Hash)
	}
	if err := VerifyCheckpoint(p, key+".pub"); err != nil {
		t.Fatalf("checkpoint for appended record should verify: %v", err)
	}
}

func TestWriteCheckpointRejectsTamperedLedger(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "audit.jsonl")
	key := filepath.Join(root, "audit.key")
	if _, err := Append(p, "backup", "a/b", "ok", "trusted", nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.Replace(string(b), "trusted", "altered", 1))
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteCheckpoint(p, key); err == nil {
		t.Fatal("checkpoint unexpectedly signed a tampered ledger")
	}
	if _, err := os.Stat(p + ".checkpoint.json"); !os.IsNotExist(err) {
		t.Fatalf("checkpoint file should not be committed after failed verification: %v", err)
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

	// Rewriting an existing record is now rejected by Append itself. Extend the
	// valid ledger first, then legitimately sign its new head with a different
	// key to preserve the trust-anchor attack scenario.
	if _, err := Append(p, "verify", "a/b", "ok", "attacker head", nil); err != nil {
		t.Fatal(err)
	}
	if err := WriteCheckpoint(p, attackerKey); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCheckpoint(p, trustedKey+".pub"); err == nil {
		t.Fatal("checkpoint signed with attacker key unexpectedly verified against pinned trust anchor")
	}
}
