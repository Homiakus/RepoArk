package policy

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/manifest"
	"github.com/Homiakus/repoark/internal/state"
)

func TestPolicyHealthy(t *testing.T) {
	cfg := config.Default()
	cfg.Backup.Root = t.TempDir()
	cfg.Security.SignManifests = false
	cfg.Policy.RequireSignedManifest = false
	cfg.Policy.RequireAudit = false
	cfg.Policy.MaxBackupAge = "2h"
	cfg.Policy.MaxRecoveryDrillAge = "2h"
	cfg.Policy.MaxGitLabDrillAge = ""
	cfg.Policy.MaxOffsiteAge = ""
	now := time.Now().UTC()
	m := manifest.Manifest{Version: 4, StartedAt: now.Add(-time.Minute), EndedAt: now, GitHubUser: "x"}
	if _, err := manifest.Write(cfg.Backup.Root, m); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(filepath.Join(cfg.Backup.Root, "state", "recovery-drill.json"), state.Record{Kind: "recovery-drill", OK: true, EndedAt: now}); err != nil {
		t.Fatal(err)
	}
	r := Evaluate(context.Background(), cfg, now)
	if !r.Healthy {
		t.Fatalf("violations=%#v", r.Violations)
	}
}

func TestPolicyDetectsStale(t *testing.T) {
	cfg := config.Default()
	cfg.Backup.Root = t.TempDir()
	cfg.Security.SignManifests = false
	cfg.Policy.RequireSignedManifest = false
	cfg.Policy.RequireAudit = false
	cfg.Policy.MaxRecoveryDrillAge = ""
	cfg.Policy.MaxGitLabDrillAge = ""
	cfg.Policy.MaxOffsiteAge = ""
	cfg.Policy.MaxBackupAge = "1h"
	now := time.Now().UTC()
	m := manifest.Manifest{Version: 4, StartedAt: now.Add(-3 * time.Hour), EndedAt: now.Add(-2 * time.Hour)}
	_, _ = manifest.Write(cfg.Backup.Root, m)
	r := Evaluate(context.Background(), cfg, now)
	if r.Healthy {
		t.Fatal("expected stale backup violation")
	}
}

func TestPolicyDetectsRecoveryRTOBreach(t *testing.T) {
	cfg := config.Default()
	cfg.Backup.Root = t.TempDir()
	cfg.Security.SignManifests = false
	cfg.Policy.RequireSignedManifest = false
	cfg.Policy.RequireAudit = false
	cfg.Policy.MaxBackupAge = "2h"
	cfg.Policy.MaxRecoveryDrillAge = "2h"
	cfg.Policy.MaxRecoveryDrillDuration = "5m"
	cfg.Policy.MaxGitLabDrillAge = ""
	cfg.Policy.MaxGitLabDrillDuration = ""
	cfg.Policy.MaxOffsiteAge = ""
	now := time.Now().UTC()
	m := manifest.Manifest{Version: 4, StartedAt: now.Add(-time.Minute), EndedAt: now}
	if _, err := manifest.Write(cfg.Backup.Root, m); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(filepath.Join(cfg.Backup.Root, "state", "recovery-drill.json"), state.Record{Kind: "recovery-drill", OK: true, StartedAt: now.Add(-10 * time.Minute), EndedAt: now}); err != nil {
		t.Fatal(err)
	}
	r := Evaluate(context.Background(), cfg, now)
	if r.Healthy {
		t.Fatal("expected recovery RTO violation")
	}
	found := false
	for _, v := range r.Violations {
		if v.Code == "recovery_drill_rto" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected recovery_drill_rto, got %#v", r.Violations)
	}
}
