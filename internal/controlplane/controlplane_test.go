package controlplane

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/config"
)

func TestQueueLeaseRetryAndDedup(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	j, created, err := s.Enqueue(ctx, Job{Kind: "backup-repo", Target: "acme/demo", Priority: 80, MaxAttempts: 2})
	if err != nil || !created {
		t.Fatalf("enqueue: created=%v err=%v", created, err)
	}
	j2, created, err := s.Enqueue(ctx, Job{Kind: "backup-repo", Target: "acme/demo"})
	if err != nil || created || j2.ID != j.ID {
		t.Fatalf("dedupe failed: %#v %#v", j, j2)
	}
	leased, err := s.Lease(ctx, "worker-a", 1, time.Minute)
	if err != nil || len(leased) != 1 {
		t.Fatalf("lease: %v %#v", err, leased)
	}
	if leased[0].Attempts != 1 || leased[0].Status != JobRunning {
		t.Fatalf("unexpected leased job: %#v", leased[0])
	}
	if err := s.Fail(ctx, j.ID, "worker-a", "boom", 0); err != nil {
		t.Fatal(err)
	}
	leased, err = s.Lease(ctx, "worker-b", 1, time.Minute)
	if err != nil || len(leased) != 1 {
		t.Fatalf("retry lease: %v %#v", err, leased)
	}
	if err := s.Fail(ctx, j.ID, "worker-b", "boom again", 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJob(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != JobFailed || got.Attempts != 2 {
		t.Fatalf("terminal retry policy not enforced: %#v", got)
	}
	if err := s.RetryJob(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetJob(ctx, j.ID)
	if got.Status != JobQueued || got.Attempts != 0 {
		t.Fatalf("manual retry failed: %#v", got)
	}
}

func TestExpiredLeaseCanBeReclaimed(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	j, _, _ := s.Enqueue(ctx, Job{Kind: "backup-repo", Target: "acme/demo"})
	xs, _ := s.Lease(ctx, "dead-worker", 1, -time.Second)
	if len(xs) != 1 {
		t.Fatal("initial lease missing")
	}
	xs, _ = s.Lease(ctx, "new-worker", 1, time.Minute)
	if len(xs) != 1 || xs[0].ID != j.ID || xs[0].LeaseOwner != "new-worker" {
		t.Fatalf("lease not reclaimed: %#v", xs)
	}
}

func TestSchedulerEnqueuesDueRepositoryOnlyOnce(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	cfg := config.Default()
	cfg.ControlPlane.Enabled = true
	cfg.ControlPlane.Workers.MaxAttempts = 4
	cfg.ControlPlane.Scheduler.DefaultInterval = "1h"
	sched := Scheduler{Store: s, Config: cfg}
	now := time.Now().UTC()
	r := Repository{ID: RepositoryID("alice", "acme/demo"), Account: "alice", FullName: "acme/demo", BackupRoot: "/tmp/backups", IntervalSeconds: 3600, Priority: 90, Enabled: true, NextRunAt: now.Add(-time.Minute)}
	if err := s.UpsertRepository(ctx, r); err != nil {
		t.Fatal(err)
	}
	n, err := sched.EnqueueDue(ctx, now)
	if err != nil || n != 1 {
		t.Fatalf("first enqueue n=%d err=%v", n, err)
	}
	n, err = sched.EnqueueDue(ctx, now)
	if err != nil || n != 0 {
		t.Fatalf("second enqueue n=%d err=%v", n, err)
	}
	jobs, _ := s.ListJobs(ctx, 10)
	if len(jobs) != 1 || jobs[0].Priority != 90 {
		t.Fatalf("unexpected jobs: %#v", jobs)
	}
}

func TestSchedulePolicyMatching(t *testing.T) {
	cfg := config.Default()
	cfg.ControlPlane.Scheduler.Policies = []config.RepoSchedulePolicy{{Pattern: "critical/*", Interval: "15m", Priority: 100, MirrorGitLab: true}, {Pattern: "*", Interval: "12h", Priority: 40}}
	s := Scheduler{Config: cfg}
	d, p, m := s.scheduleFor("critical/api")
	if d != 15*time.Minute || p != 100 || !m {
		t.Fatalf("critical policy mismatch: %v %d %v", d, p, m)
	}
	d, p, m = s.scheduleFor("misc/tool")
	if d != 12*time.Hour || p != 40 || m {
		t.Fatalf("fallback policy mismatch: %v %d %v", d, p, m)
	}
}

func TestDisableMissingRepositoriesStopsScheduling(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now().UTC()
	keep := Repository{ID: "acct/keep", Account: "acct", FullName: "acct/keep", Enabled: true, NextRunAt: now}
	gone := Repository{ID: "acct/gone", Account: "acct", FullName: "acct/gone", Enabled: true, NextRunAt: now}
	other := Repository{ID: "other/repo", Account: "other", FullName: "other/repo", Enabled: true, NextRunAt: now}
	for _, r := range []Repository{keep, gone, other} {
		if err := store.UpsertRepository(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DisableMissingRepositories(ctx, "acct", map[string]struct{}{keep.ID: {}}); err != nil {
		t.Fatal(err)
	}
	repos, err := store.ListRepositories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]bool{}
	for _, r := range repos {
		states[r.ID] = r.Enabled
	}
	if !states[keep.ID] || states[gone.ID] || !states[other.ID] {
		t.Fatalf("unexpected enabled states: %#v", states)
	}
	due, err := store.DueRepositories(ctx, now.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range due {
		if r.ID == gone.ID {
			t.Fatalf("disabled repository was returned as due: %#v", r)
		}
	}
}

func TestPruneGenerationRecordsMatchesRetention(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	base := time.Now().UTC().Add(-10 * time.Minute)
	for i := 0; i < 5; i++ {
		if err := store.RecordGeneration(ctx, Generation{
			ID:           fmt.Sprintf("g-%d", i),
			RepositoryID: "repo-1",
			Repository:   "acct/repo",
			CreatedAt:    base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.PruneGenerationRecords(ctx, "repo-1", 2); err != nil {
		t.Fatal(err)
	}
	gens, err := store.ListGenerations(ctx, "repo-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(gens) != 2 || gens[0].ID != "g-4" || gens[1].ID != "g-3" {
		t.Fatalf("unexpected retained generations: %#v", gens)
	}
}

func TestJobAffinityRestrictsLeaseAndDedupeScope(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	ja, created, err := store.Enqueue(ctx, Job{Kind: "mirror-gitlab", Target: "acme/demo", Affinity: "agent-a"})
	if err != nil || !created {
		t.Fatalf("enqueue a: created=%v err=%v", created, err)
	}
	jb, created, err := store.Enqueue(ctx, Job{Kind: "mirror-gitlab", Target: "acme/demo", Affinity: "agent-b"})
	if err != nil || !created || jb.ID == ja.ID {
		t.Fatalf("different affinity must be independent: a=%s b=%s created=%v err=%v", ja.ID, jb.ID, created, err)
	}
	if _, created, err := store.Enqueue(ctx, Job{Kind: "mirror-gitlab", Target: "acme/demo", Affinity: "agent-a"}); err != nil || created {
		t.Fatalf("same affinity should dedupe: created=%v err=%v", created, err)
	}
	got, err := store.Lease(ctx, "agent-b", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != jb.ID {
		t.Fatalf("agent-b leased wrong affinity: %#v", got)
	}
	got, err = store.Lease(ctx, "local-worker", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("unrelated worker leased affinitized jobs: %#v", got)
	}
	got, err = store.Lease(ctx, "agent-a", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != ja.ID {
		t.Fatalf("agent-a leased wrong affinity: %#v", got)
	}
}

func TestExpiredFinalLeaseBecomesTerminalInsteadOfAttemptMaxPlusOne(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	j, _, err := store.Enqueue(ctx, Job{Kind: "backup-repo", Target: "acme/final", MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	leased, err := store.Lease(ctx, "worker-a", 1, time.Millisecond)
	if err != nil || len(leased) != 1 || leased[0].Attempts != 1 {
		t.Fatalf("initial lease: %#v %v", leased, err)
	}
	store.mu.Lock()
	x := store.Jobs[j.ID]
	x.LeaseUntil = time.Now().UTC().Add(-time.Second)
	store.Jobs[j.ID] = x
	store.mu.Unlock()

	again, err := store.Lease(ctx, "worker-b", 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("final expired lease was issued again: %#v", again)
	}
	got, err := store.GetJob(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != JobFailed || got.Attempts != 1 {
		t.Fatalf("expected terminal failed at attempt 1, got %#v", got)
	}
}

func TestStatsExposeStrandedAffinitizedJobs(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, _, err := store.Enqueue(ctx, Job{Kind: "mirror-gitlab", Target: "acme/demo", Affinity: "agent-a"}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Stats(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if st.StrandedJobs != 1 {
		t.Fatalf("expected one stranded job, got %#v", st)
	}
	if err := store.HeartbeatAgent(ctx, Agent{ID: "agent-a", Name: "agent-a"}); err != nil {
		t.Fatal(err)
	}
	st, err = store.Stats(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if st.StrandedJobs != 0 || st.ConnectedAgents != 1 {
		t.Fatalf("expected connected agent to clear stranded count, got %#v", st)
	}
}

func TestAffinityFromMetaPath(t *testing.T) {
	cases := map[string]string{
		"agent://worker-a/home/user/generation.json": "worker-a",
		"agent://worker-b/generation.json":           "worker-b",
		"/srv/repoark/generation.json":               LocalWorkerAffinity,
		"":                                           LocalWorkerAffinity,
	}
	for in, want := range cases {
		if got := AffinityFromMetaPath(in); got != want {
			t.Fatalf("AffinityFromMetaPath(%q)=%q want %q", in, got, want)
		}
	}
}
