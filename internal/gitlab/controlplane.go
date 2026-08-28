package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/execx"
	"github.com/Homiakus/repoark/internal/manifest"
)

// MigrateRepository mirrors one already-backed-up repository to the configured
// GitLab recovery target. It is the unit used by the v0.5 job queue.
func (m *Manager) MigrateRepository(ctx context.Context, r manifest.RepoResult, emit func(Event)) error {
	if emit == nil {
		emit = func(Event) {}
	}
	if r.Error != "" {
		return fmt.Errorf("%s is not a successful backup: %s", r.FullName, r.Error)
	}
	token := strings.TrimSpace(os.Getenv(m.Config.GitLab.TokenEnv))
	if token == "" {
		return fmt.Errorf("%s is not set", m.Config.GitLab.TokenEnv)
	}
	client := apiClient{base: strings.TrimRight(m.Config.GitLab.URL, "/"), token: token, http: defaultAPIHTTPClient()}
	emit(Event{Repo: r.FullName, Stage: "gitlab", Message: "creating/updating project", Done: 0, Total: 1})
	var project Project
	var err error
	if m.Config.GitLab.PreserveNamespaces {
		project, err = client.ensureNamespacedProject(ctx, r.FullName, r.SourceVisibility)
	} else {
		project, err = client.ensureProject(ctx, r.FullName)
	}
	if err != nil {
		return err
	}
	pushURL := withUsername(project.HTTPURLToRepo, "oauth2")
	env, _ := execx.AskPassEnv(token, "oauth2")
	mirrorPath := artifactPath(m.Config.Backup.Root, r.MirrorPath)
	if _, err := execx.Run(ctx, mirrorPath, env, "git", "push", "--mirror", pushURL); err != nil {
		return fmt.Errorf("%s: %w", r.FullName, err)
	}
	if r.LFSArchivePath != "" {
		if !execx.Exists("git-lfs") {
			return fmt.Errorf("%s: git-lfs required for LFS mirroring", r.FullName)
		}
		if _, err := os.Stat(filepath.Join(mirrorPath, "lfs")); err == nil {
			if _, err := execx.Run(ctx, mirrorPath, env, "git", "lfs", "push", "--all", pushURL); err != nil {
				return fmt.Errorf("%s LFS: %w", r.FullName, err)
			}
		}
	}
	emit(Event{Repo: r.FullName, Stage: "gitlab", Message: "mirrored", Done: 1, Total: 1})
	return nil
}

func defaultAPIHTTPClient() *http.Client { return &http.Client{Timeout: 60 * time.Second} }
