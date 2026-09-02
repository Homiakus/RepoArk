package observability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Homiakus/repoark/internal/audit"
	"github.com/Homiakus/repoark/internal/config"
)

func TestAppendConsoleAuditSignedPinsCheckpointToWebRecord(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Audit.Enabled = true
	cfg.Audit.Required = true
	cfg.Audit.Path = filepath.Join(root, "audit", "ledger.jsonl")
	cfg.Security.SignManifests = true
	cfg.Security.SigningKeyPath = filepath.Join(root, "keys", "audit.key")
	c := &consoleServer{base: New(cfg)}

	if err := c.appendConsoleAudit("web-operation", "verify", "requested", "", map[string]any{"surface": "web", "request_id": "r-1"}); err != nil {
		t.Fatal(err)
	}
	records, err := audit.Recent(cfg.Audit.Path, 1, "web-operation")
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
		t.Fatalf("checkpoint head=%d/%s want web record=%d/%s", cp.Seq, cp.Hash, records[0].Seq, records[0].Hash)
	}
	if err := audit.VerifyCheckpoint(cfg.Audit.Path, cfg.Security.SigningKeyPath+".pub"); err != nil {
		t.Fatalf("signed web checkpoint should verify: %v", err)
	}
}
