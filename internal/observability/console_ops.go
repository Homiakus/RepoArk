package observability

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/Homiakus/repoark/internal/backup"
	"github.com/Homiakus/repoark/internal/cas"
	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/fleet"
	"github.com/Homiakus/repoark/internal/gitlab"
	"github.com/Homiakus/repoark/internal/offsite"
	"github.com/Homiakus/repoark/internal/policy"
)

var errConsoleJobRunning = errors.New("another RepoArk operation is already running")

type consoleLog struct {
	At      time.Time `json:"at"`
	Message string    `json:"message"`
}

type consoleJob struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	State     string       `json:"state"`
	StartedAt time.Time    `json:"started_at"`
	EndedAt   *time.Time   `json:"ended_at,omitempty"`
	Error     string       `json:"error,omitempty"`
	Logs      []consoleLog `json:"logs"`
}

type consoleJobManager struct {
	mu      sync.RWMutex
	parent  context.Context
	current *consoleJob
	cancel  context.CancelFunc
}

func newConsoleJobManager(parent context.Context) *consoleJobManager {
	return &consoleJobManager{parent: parent}
}

func (m *consoleJobManager) Start(name string, run func(context.Context, func(string)) error) (consoleJob, error) {
	m.mu.Lock()
	if m.current != nil && m.current.State == "running" {
		job := cloneConsoleJob(m.current)
		m.mu.Unlock()
		return job, errConsoleJobRunning
	}
	ctx, cancel := context.WithCancel(m.parent)
	job := &consoleJob{
		ID:        fmt.Sprintf("%s-%d", name, time.Now().UTC().UnixNano()),
		Name:      name,
		State:     "running",
		StartedAt: time.Now().UTC(),
		Logs:      make([]consoleLog, 0, 64),
	}
	m.current = job
	m.cancel = cancel
	m.appendLocked(job, "operation started")
	snapshot := cloneConsoleJob(job)
	m.mu.Unlock()

	go func() {
		err := run(ctx, func(line string) { m.append(job.ID, line) })
		ended := time.Now().UTC()
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.current == nil || m.current.ID != job.ID {
			return
		}
		m.current.EndedAt = &ended
		m.cancel = nil
		switch {
		case errors.Is(err, context.Canceled):
			m.current.State = "cancelled"
			m.current.Error = err.Error()
			m.appendLocked(m.current, "operation cancelled")
		case err != nil:
			m.current.State = "failed"
			m.current.Error = err.Error()
			m.appendLocked(m.current, "operation failed: "+err.Error())
		default:
			m.current.State = "succeeded"
			m.appendLocked(m.current, "operation completed successfully")
		}
	}()

	return snapshot, nil
}

func (m *consoleJobManager) Snapshot() *consoleJob {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current == nil {
		return nil
	}
	job := cloneConsoleJob(m.current)
	return &job
}

func (m *consoleJobManager) Cancel() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil || m.current.State != "running" || m.cancel == nil {
		return errors.New("no running operation")
	}
	m.appendLocked(m.current, "cancellation requested")
	m.cancel()
	return nil
}

func (m *consoleJobManager) append(id, line string) {
	if line == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil || m.current.ID != id {
		return
	}
	m.appendLocked(m.current, line)
}

func (m *consoleJobManager) appendLocked(job *consoleJob, line string) {
	job.Logs = append(job.Logs, consoleLog{At: time.Now().UTC(), Message: line})
	if len(job.Logs) > 400 {
		job.Logs = append([]consoleLog(nil), job.Logs[len(job.Logs)-400:]...)
	}
}

func cloneConsoleJob(src *consoleJob) consoleJob {
	out := *src
	out.Logs = append([]consoleLog(nil), src.Logs...)
	if src.EndedAt != nil {
		t := *src.EndedAt
		out.EndedAt = &t
	}
	return out
}

type consoleAction struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Risk        string `json:"risk"`
}

func consoleActions(cfg config.Config) []consoleAction {
	return []consoleAction{
		{Name: "backup", Label: "Backup now", Description: "Back up the primary GitHub account and metadata.", Enabled: true, Risk: "normal"},
		{Name: "fleet-backup", Label: "Fleet backup", Description: "Back up every configured GitHub account.", Enabled: cfg.Fleet.Enabled, Risk: "normal"},
		{Name: "verify", Label: "Verify", Description: "Verify the latest repository backup set.", Enabled: true, Risk: "normal"},
		{Name: "policy", Label: "Policy check", Description: "Evaluate RPO/RTO and integrity policy gates.", Enabled: cfg.Policy.Enabled, Risk: "normal"},
		{Name: "cas-compact", Label: "Compact CAS", Description: "Deduplicate large backup payloads into the content-addressed store.", Enabled: cfg.CAS.Enabled, Risk: "normal"},
		{Name: "repo-drill", Label: "Repository drill", Description: "Run a sampled repository recovery drill.", Enabled: cfg.RecoveryDrill.Enabled, Risk: "elevated"},
		{Name: "gitlab-drill", Label: "GitLab DR drill", Description: "Exercise the GitLab backup restore path.", Enabled: cfg.GitLab.Enabled && cfg.GitLab.RestoreDrill.Enabled, Risk: "elevated"},
		{Name: "gitlab-deploy", Label: "Deploy GitLab", Description: "Generate deployment state and deploy the configured GitLab instance.", Enabled: cfg.GitLab.Enabled, Risk: "danger"},
		{Name: "gitlab-migrate", Label: "Migrate to GitLab", Description: "Mirror the latest GitHub backup into GitLab.", Enabled: cfg.GitLab.Enabled, Risk: "danger"},
		{Name: "gitlab-backup", Label: "GitLab backup", Description: "Create a GitLab application backup.", Enabled: cfg.GitLab.Enabled, Risk: "elevated"},
		{Name: "offsite", Label: "Offsite sync", Description: "Replicate the backup set to the configured offsite backend.", Enabled: cfg.Offsite.Enabled, Risk: "elevated"},
	}
}

func actionEnabled(cfg config.Config, name string) bool {
	for _, action := range consoleActions(cfg) {
		if action.Name == name {
			return action.Enabled
		}
	}
	return false
}

func consoleOperation(cfg config.Config, name string) (func(context.Context, func(string)) error, error) {
	if !actionEnabled(cfg, name) {
		return nil, fmt.Errorf("operation %q is disabled by configuration or unknown", name)
	}

	switch name {
	case "backup":
		return func(ctx context.Context, log func(string)) error {
			_, err := backup.New(cfg).Run(ctx, func(e backup.Event) {
				if e.Repo == "" {
					log(fmt.Sprintf("%-10s %s", e.Stage, e.Message))
					return
				}
				log(fmt.Sprintf("[%s] %-10s %s", e.Repo, e.Stage, e.Message))
			})
			return err
		}, nil
	case "fleet-backup":
		return func(ctx context.Context, log func(string)) error {
			results, err := fleet.RunBackup(ctx, cfg, func(e fleet.Event) {
				log(fmt.Sprintf("[%s] %-10s %s", e.Account, e.Stage, e.Message))
			})
			log(fmt.Sprintf("fleet finished: %d accounts", len(results)))
			return err
		}, nil
	case "verify":
		return func(ctx context.Context, log func(string)) error {
			n, err := backup.New(cfg).Verify(ctx, func(e backup.Event) {
				log(fmt.Sprintf("[%s] verify %s", e.Repo, e.Message))
			})
			log(fmt.Sprintf("verified repositories: %d", n))
			return err
		}, nil
	case "policy":
		return func(ctx context.Context, log func(string)) error {
			r := policy.Evaluate(ctx, cfg, time.Now())
			log(fmt.Sprintf("policy healthy=%t violations=%d", r.Healthy, len(r.Violations)))
			return policy.Error(r)
		}, nil
	case "cas-compact":
		return func(ctx context.Context, log func(string)) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			store := cas.New(cfg.CAS.Root, cfg.CAS.MinFileSize)
			paths := []string{
				filepath.Join(cfg.Backup.Root, "release-assets"),
				filepath.Join(cfg.Backup.Root, "actions-artifacts"),
				filepath.Join(cfg.Backup.Root, "oci"),
				filepath.Join(cfg.Backup.Root, "packages"),
				filepath.Join(cfg.Backup.Root, "official-exports"),
				filepath.Join(cfg.Backup.Root, "lfs"),
				filepath.Join(cfg.Backup.Root, "bundles"),
			}
			st, err := store.Compact(paths)
			log(fmt.Sprintf("CAS objects=%d reclaimed=%d bytes", st.Objects, st.Reclaimed))
			return err
		}, nil
	case "repo-drill":
		return func(ctx context.Context, log func(string)) error {
			report, err := backup.New(cfg).Drill(ctx, cfg.RecoveryDrill.SampleSize, func(e backup.Event) {
				log(fmt.Sprintf("[%s] drill %s", e.Repo, e.Message))
			})
			log(fmt.Sprintf("drill finished: %d OK, %d failed", report.Succeeded, report.Failed))
			return err
		}, nil
	case "gitlab-drill":
		return func(ctx context.Context, log func(string)) error {
			report, err := gitlab.New(cfg).RestoreDrill(ctx, "", func(msg string) { log("[gitlab-drill] " + msg) })
			log(fmt.Sprintf("GitLab drill: backup=%s healthy=%t", report.BackupID, report.Healthy))
			return err
		}, nil
	case "gitlab-deploy":
		return func(ctx context.Context, log func(string)) error {
			log("GitLab: generating Compose and deploying")
			return gitlab.New(cfg).Deploy(ctx, cfg.GitLab.RemoteHost)
		}, nil
	case "gitlab-migrate":
		return func(ctx context.Context, log func(string)) error {
			return gitlab.New(cfg).MigrateLatest(ctx, func(e gitlab.Event) { log(fmt.Sprintf("[%s] %s", e.Repo, e.Message)) })
		}, nil
	case "gitlab-backup":
		return func(ctx context.Context, log func(string)) error {
			path, err := gitlab.New(cfg).Backup(ctx, cfg.GitLab.RemoteHost)
			if err == nil {
				log("GitLab backup: " + path)
			}
			return err
		}, nil
	case "offsite":
		return func(ctx context.Context, log func(string)) error {
			log("offsite: replicating backup set with configured backend")
			return offsite.Sync(ctx, cfg)
		}, nil
	default:
		return nil, fmt.Errorf("unknown operation %q", name)
	}
}
