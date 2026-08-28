package policy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Homiakus/repoark/internal/audit"
	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/kmsattest"
	"github.com/Homiakus/repoark/internal/manifest"
	"github.com/Homiakus/repoark/internal/state"
)

type Violation struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

type Report struct {
	EvaluatedAt time.Time   `json:"evaluated_at"`
	Healthy     bool        `json:"healthy"`
	Violations  []Violation `json:"violations"`
}

func Evaluate(ctx context.Context, cfg config.Config, now time.Time) Report {
	_ = ctx
	r := Report{EvaluatedAt: now.UTC(), Healthy: true}
	if !cfg.Policy.Enabled {
		return r
	}
	add := func(code, msg string) {
		r.Healthy = false
		r.Violations = append(r.Violations, Violation{Code: code, Message: msg, Severity: "critical"})
	}

	man, err := manifest.ReadLatest(cfg.Backup.Root)
	if err != nil {
		add("manifest_unavailable", err.Error())
		return r
	}
	if d, ok := parse(cfg.Policy.MaxBackupAge); ok && now.Sub(man.EndedAt) > d {
		add("rpo_backup_age", fmt.Sprintf("latest backup is %s old; policy allows %s", now.Sub(man.EndedAt).Round(time.Minute), d))
	}
	if man.Failed > cfg.Policy.MaxFailedRepositories {
		add("backup_failures", fmt.Sprintf("%d failed repositories exceeds policy maximum %d", man.Failed, cfg.Policy.MaxFailedRepositories))
	}
	if cfg.Policy.MaxWarnings >= 0 && man.WarningCount > cfg.Policy.MaxWarnings {
		add("backup_warnings", fmt.Sprintf("%d warnings exceeds policy maximum %d", man.WarningCount, cfg.Policy.MaxWarnings))
	}
	if cfg.Policy.RequireSignedManifest {
		if _, err := os.Stat(filepath.Join(cfg.Backup.Root, "manifests", "latest.json.sig")); err != nil {
			add("manifest_unsigned", "latest manifest has no detached signature")
		} else if err := manifest.VerifyLatestSignature(cfg.Backup.Root, cfg.Security.SigningKeyPath+".pub"); err != nil {
			add("manifest_signature_invalid", err.Error())
		}
	}
	if cfg.Security.KMSAttestation.Enabled && cfg.Security.KMSAttestation.RequireValid {
		if err := kmsattest.VerifyFile(ctx, filepath.Join(cfg.Backup.Root, "manifests", "latest.json"), cfg.Security.KMSAttestation); err != nil {
			add("kms_attestation_invalid", err.Error())
		}
	}
	if cfg.Policy.RequireAudit {
		if !cfg.Audit.Enabled {
			add("audit_disabled", "audit ledger is required by policy")
		} else if _, err := audit.Verify(cfg.Audit.Path); err != nil {
			add("audit_invalid", err.Error())
		}
	}
	checkAge := func(code, path, configured string) {
		d, ok := parse(configured)
		if !ok {
			return
		}
		age, exists, err := state.Age(path, now)
		if err != nil {
			add(code+"_invalid", err.Error())
			return
		}
		if !exists {
			add(code+"_missing", "no successful state record found")
			return
		}
		rec, err := state.Read(path)
		if err != nil {
			add(code+"_invalid", err.Error())
			return
		}
		if !rec.OK {
			add(code+"_failed", "latest recorded operation failed: "+rec.Detail)
			return
		}
		if age > d {
			add(code+"_age", fmt.Sprintf("latest successful operation is %s old; policy allows %s", age.Round(time.Minute), d))
		}
	}
	recoveryState := filepath.Join(cfg.Backup.Root, "state", "recovery-drill.json")
	checkAge("recovery_drill", recoveryState, cfg.Policy.MaxRecoveryDrillAge)
	checkDuration := func(code, path, configured string) {
		d, ok := parse(configured)
		if !ok {
			return
		}
		rec, err := state.Read(path)
		if errors.Is(err, os.ErrNotExist) {
			add(code+"_missing", "no restore-drill state record found")
			return
		}
		if err != nil {
			add(code+"_invalid", err.Error())
			return
		}
		if !rec.OK {
			return
		}
		if rec.StartedAt.IsZero() || rec.EndedAt.IsZero() {
			add(code+"_duration_missing", "restore-drill state has no duration")
			return
		}
		duration := rec.EndedAt.Sub(rec.StartedAt)
		if duration > d {
			add(code+"_rto", fmt.Sprintf("latest restore drill took %s; RTO policy allows %s", duration.Round(time.Second), d))
		}
	}
	checkDuration("recovery_drill", recoveryState, cfg.Policy.MaxRecoveryDrillDuration)
	if cfg.GitLab.Enabled {
		gitlabState := filepath.Join(cfg.GitLab.DataDir, "state", "restore-drill.json")
		checkAge("gitlab_drill", gitlabState, cfg.Policy.MaxGitLabDrillAge)
		checkDuration("gitlab_drill", gitlabState, cfg.Policy.MaxGitLabDrillDuration)
	}
	if cfg.Offsite.Enabled {
		checkAge("offsite", filepath.Join(cfg.Backup.Root, "state", "offsite.json"), cfg.Policy.MaxOffsiteAge)
	}
	return r
}

func parse(s string) (time.Duration, bool) {
	if s == "" {
		return 0, false
	}
	d, err := time.ParseDuration(s)
	return d, err == nil && d > 0
}

func Error(report Report) error {
	if report.Healthy {
		return nil
	}
	if len(report.Violations) == 0 {
		return errors.New("policy unhealthy")
	}
	return fmt.Errorf("policy failed with %d violation(s): %s", len(report.Violations), report.Violations[0].Message)
}
