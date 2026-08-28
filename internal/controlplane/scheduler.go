package controlplane

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/githubapi"
)

type Scheduler struct {
	Store  Store
	Config config.Config
}

func (s Scheduler) SyncRepositories(ctx context.Context) (int, error) {
	token := githubapi.ResolveToken(s.Config.GitHub.TokenEnv)
	if token == "" {
		return 0, fmt.Errorf("%s/GH_TOKEN/GITHUB_TOKEN is not set", s.Config.GitHub.TokenEnv)
	}
	c := githubapi.New(s.Config.GitHub.APIURL, token)
	c.MaxPages = s.Config.GitHub.MaxMetadataPages
	u, err := c.User(ctx)
	if err != nil {
		return 0, err
	}
	repos, err := c.Repositories(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	n := 0
	seen := map[string]struct{}{}
	for _, r := range repos {
		if r.Fork && !s.Config.GitHub.IncludeForks {
			continue
		}
		if r.Archived && !s.Config.GitHub.IncludeArchived {
			continue
		}
		interval, priority, mirror := s.scheduleFor(r.FullName)
		rec := Repository{ID: RepositoryID(u.Login, r.FullName), Account: u.Login, FullName: r.FullName, BackupRoot: s.Config.Backup.Root, IntervalSeconds: int64(interval.Seconds()), Priority: priority, MirrorGitLab: mirror, Enabled: true, NextRunAt: now}
		if err := s.Store.UpsertRepository(ctx, rec); err != nil {
			return n, err
		}
		seen[rec.ID] = struct{}{}
		n++
	}
	if err := s.Store.DisableMissingRepositories(ctx, u.Login, seen); err != nil {
		return n, err
	}
	return n, nil
}

func (s Scheduler) scheduleFor(fullName string) (time.Duration, int, bool) {
	fallback, _ := time.ParseDuration(s.Config.ControlPlane.Scheduler.DefaultInterval)
	if fallback <= 0 {
		fallback = 24 * time.Hour
	}
	priority := 50
	mirror := s.Config.ControlPlane.Mirroring.Enabled
	for _, p := range s.Config.ControlPlane.Scheduler.Policies {
		matched := p.Pattern == "*" || p.Pattern == "**"
		if !matched {
			matched, _ = path.Match(p.Pattern, fullName)
		}
		if matched {
			if d, err := time.ParseDuration(p.Interval); err == nil && d > 0 {
				fallback = d
			}
			priority = p.Priority
			mirror = p.MirrorGitLab
			break
		}
	}
	return fallback, priority, mirror
}

func (s Scheduler) EnqueueDue(ctx context.Context, now time.Time) (int, error) {
	repos, err := s.Store.DueRepositories(ctx, now, 500)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range repos {
		payload := fmt.Sprintf(`{"repository_id":%q,"full_name":%q}`, r.ID, r.FullName)
		_, created, err := s.Store.Enqueue(ctx, Job{Kind: "backup-repo", Target: r.FullName, Payload: payload, Priority: r.Priority, MaxAttempts: s.Config.ControlPlane.Workers.MaxAttempts, NotBefore: now})
		if err != nil {
			return n, err
		}
		next := now.Add(time.Duration(r.IntervalSeconds) * time.Second)
		if err := s.Store.MarkScheduled(ctx, r.ID, next); err != nil {
			return n, err
		}
		if created {
			n++
		}
	}
	return n, nil
}

func (s Scheduler) Run(ctx context.Context) error {
	tick, _ := time.ParseDuration(s.Config.ControlPlane.Scheduler.Tick)
	if tick <= 0 {
		tick = 30 * time.Second
	}
	discovery, _ := time.ParseDuration(s.Config.ControlPlane.Scheduler.DiscoveryInterval)
	if discovery <= 0 {
		discovery = 30 * time.Minute
	}
	_, _ = s.SyncRepositories(ctx)
	t := time.NewTicker(tick)
	defer t.Stop()
	d := time.NewTicker(discovery)
	defer d.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-t.C:
			_, _ = s.EnqueueDue(ctx, now)
		case <-d.C:
			_, _ = s.SyncRepositories(ctx)
		}
	}
}

func normalizeTarget(v string) string { return strings.TrimSpace(v) }
