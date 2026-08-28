package backup

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/githubapi"
	"github.com/Homiakus/repoark/internal/manifest"
)

// BackupRepository synchronizes exactly one repository without replacing the
// global latest manifest. It is used by the v0.5 control-plane scheduler, where
// repository generations have independent lifecycles.
func (m *Manager) BackupRepository(ctx context.Context, fullName string, emit func(Event)) (manifest.RepoResult, error) {
	if emit == nil {
		emit = func(Event) {}
	}
	fullName = strings.TrimSpace(fullName)
	if fullName == "" {
		return manifest.RepoResult{}, fmt.Errorf("repository name is empty")
	}
	repos, err := m.GitHub.Repositories(ctx)
	if err != nil {
		return manifest.RepoResult{}, fmt.Errorf("list repositories: %w", err)
	}
	var selected githubapi.Repository
	found := false
	for _, r := range repos {
		if strings.EqualFold(r.FullName, fullName) {
			selected, found = r, true
			break
		}
	}
	if !found {
		return manifest.RepoResult{}, fmt.Errorf("repository %q is not accessible to the configured GitHub account", fullName)
	}
	if selected.Fork && !m.Config.GitHub.IncludeForks {
		return manifest.RepoResult{}, fmt.Errorf("repository %q is excluded because forks are disabled", fullName)
	}
	if selected.Archived && !m.Config.GitHub.IncludeArchived {
		return manifest.RepoResult{}, fmt.Errorf("repository %q is excluded because archived repositories are disabled", fullName)
	}
	emit(Event{Time: time.Now(), Repo: selected.FullName, Stage: "control", Message: "single-repository backup started", Done: 0, Total: 1})
	res := m.backupRepo(ctx, selected, emit)
	if res.Error != "" {
		return res, fmt.Errorf("%s: %s", selected.FullName, res.Error)
	}
	emit(Event{Time: time.Now(), Repo: selected.FullName, Stage: "control", Message: "single-repository backup complete", Done: 1, Total: 1})
	return res, nil
}
