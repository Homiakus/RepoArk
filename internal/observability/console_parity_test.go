package observability

import (
	"testing"

	"github.com/Homiakus/repoark/internal/config"
)

func TestConsoleActionsCoverLegacyTUIOperations(t *testing.T) {
	cfg := config.Default()
	cfg.Fleet.Enabled = true
	cfg.Policy.Enabled = true
	cfg.CAS.Enabled = true
	cfg.RecoveryDrill.Enabled = true
	cfg.GitLab.Enabled = true
	cfg.GitLab.RestoreDrill.Enabled = true
	cfg.Offsite.Enabled = true

	want := []string{
		"backup",
		"fleet-backup",
		"verify",
		"policy",
		"cas-compact",
		"repo-drill",
		"gitlab-drill",
		"gitlab-deploy",
		"gitlab-migrate",
		"gitlab-backup",
		"offsite",
	}
	actions := consoleActions(cfg)
	if len(actions) != len(want) {
		t.Fatalf("console actions=%d want=%d: %#v", len(actions), len(want), actions)
	}

	for i, name := range want {
		if actions[i].Name != name {
			t.Fatalf("action[%d]=%q want %q", i, actions[i].Name, name)
		}
		if !actions[i].Enabled {
			t.Fatalf("legacy TUI operation %q unexpectedly disabled in fully enabled config", name)
		}
		if _, err := consoleOperation(cfg, name); err != nil {
			t.Fatalf("legacy TUI operation %q has no web adapter: %v", name, err)
		}
	}
}
