package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Homiakus/repoark/internal/audit"
	"github.com/Homiakus/repoark/internal/config"
)

func TestAuditResultSignedPinsCheckpointToCLIRecord(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Audit.Enabled = true
	cfg.Audit.Required = true
	cfg.Audit.Path = filepath.Join(root, "audit", "ledger.jsonl")
	cfg.Security.SignManifests = true
	cfg.Security.SigningKeyPath = filepath.Join(root, "keys", "audit.key")

	if err := auditResult(cfg, "cli-test", "repo", nil, map[string]any{"surface": "cli"}); err != nil {
		t.Fatal(err)
	}
	records, err := audit.Recent(cfg.Audit.Path, 1, "cli-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("records=%d want 1", len(records))
	}

	b, err := os.ReadFile(cfg.Audit.Path + ".checkpoint.json")
	if err != nil {
		t.Fatal(err)
	}
	var cp audit.Checkpoint
	if err := json.Unmarshal(b, &cp); err != nil {
		t.Fatal(err)
	}
	if cp.Seq != records[0].Seq || !strings.EqualFold(cp.Hash, records[0].Hash) {
		t.Fatalf("checkpoint head=%d/%s want cli record=%d/%s", cp.Seq, cp.Hash, records[0].Seq, records[0].Hash)
	}
	if err := audit.VerifyCheckpoint(cfg.Audit.Path, cfg.Security.SigningKeyPath+".pub"); err != nil {
		t.Fatalf("signed CLI checkpoint should verify: %v", err)
	}
}
