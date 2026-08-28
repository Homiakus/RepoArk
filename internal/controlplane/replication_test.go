package controlplane

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/replication"
	"github.com/Homiakus/repoark/internal/storagehealth"
)

func replAgent(t *testing.T, store *MemoryStore, id string, seen time.Time) Agent {
	t.Helper()
	key := filepath.Join(t.TempDir(), id+".key")
	pub, err := replication.EnsureKey(key)
	if err != nil {
		t.Fatal(err)
	}
	a := Agent{ID: id, Name: id, ReplicationPublicKey: pub, Status: "online", LastSeenAt: seen, UpdatedAt: seen, StorageHealth: storagehealth.Healthy, StorageTotalBytes: 200 << 30, StorageFreeBytes: 100 << 30, StorageFreePercent: 50}
	store.mu.Lock()
	store.Agents[id] = a
	store.mu.Unlock()
	return a
}

func TestReplicationReconcilerQueuesMissingReplicaAndDedupes(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now().UTC()
	a := replAgent(t, store, "agent-a", now)
	_ = a
	_ = replAgent(t, store, "agent-b", now)
	g := Generation{ID: "gen-1", RepositoryID: "repo-1", Repository: "acme/demo", MetaPath: "agent://agent-a/x", CreatedAt: now, Verified: true}
	if err := store.RecordGeneration(ctx, g); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: "agent-a", State: ReplicaReady, VerifiedAt: now}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.ControlPlane.Replication.Enabled = true
	cfg.ControlPlane.Replication.IncludeLocal = false
	cfg.ControlPlane.Replication.Factor = 2
	cfg.ControlPlane.Replication.MinHealthy = 2
	cfg.ControlPlane.Replication.AgentTimeout = "2m"
	r := ReplicationReconciler{Store: store, Config: cfg}
	n, err := r.Reconcile(ctx)
	if err != nil || n != 1 {
		t.Fatalf("reconcile n=%d err=%v", n, err)
	}
	jobs, _ := store.ListJobs(ctx, 20)
	if len(jobs) != 1 || jobs[0].Kind != "replicate-generation" || jobs[0].Affinity != "agent-a" {
		t.Fatalf("unexpected jobs %#v", jobs)
	}
	n, err = r.Reconcile(ctx)
	if err != nil || n != 0 {
		t.Fatalf("active placement must dedupe n=%d err=%v", n, err)
	}
}

func TestReplicationReplacesOfflineReplicaToMaintainHealthyQuorum(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now().UTC()
	_ = replAgent(t, store, "agent-a", now.Add(-10*time.Minute))
	_ = replAgent(t, store, "agent-b", now)
	_ = replAgent(t, store, "agent-c", now)
	g := Generation{ID: "gen-2", RepositoryID: "repo-1", Repository: "acme/demo", MetaPath: "agent://agent-a/x", CreatedAt: now}
	_ = store.RecordGeneration(ctx, g)
	_ = store.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: "agent-a", State: ReplicaReady})
	_ = store.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: "agent-b", State: ReplicaReady})
	cfg := config.Default()
	cfg.ControlPlane.Replication.Enabled = true
	cfg.ControlPlane.Replication.IncludeLocal = false
	cfg.ControlPlane.Replication.Factor = 2
	cfg.ControlPlane.Replication.MinHealthy = 2
	cfg.ControlPlane.Replication.AgentTimeout = "2m"
	n, err := (ReplicationReconciler{Store: store, Config: cfg}).Reconcile(ctx)
	if err != nil || n != 1 {
		t.Fatalf("expected replacement replica n=%d err=%v", n, err)
	}
	jobs, _ := store.ListJobs(ctx, 10)
	if len(jobs) != 1 || jobs[0].Affinity != "agent-b" || jobs[0].Target != "gen-2@agent-c" {
		t.Fatalf("wrong replacement job %#v", jobs)
	}
}

func TestSelectRestoreAffinityFailsOverOnlyToReadyOnlineReplica(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now().UTC()
	_ = replAgent(t, store, "agent-a", now.Add(-10*time.Minute))
	_ = replAgent(t, store, "agent-b", now)
	g := Generation{ID: "gen-3", RepositoryID: "repo-1", Repository: "acme/demo", MetaPath: "agent://agent-a/x", CreatedAt: now}
	_ = store.RecordGeneration(ctx, g)
	_ = store.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: "agent-a", State: ReplicaReady})
	_ = store.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: "agent-b", State: ReplicaReady})
	cfg := config.Default().ControlPlane.Replication
	cfg.AgentTimeout = "2m"
	got, err := SelectRestoreAffinity(ctx, store, g.ID, g.MetaPath, cfg)
	if err != nil || got != "agent-b" {
		t.Fatalf("failover got=%q err=%v", got, err)
	}
	store.mu.Lock()
	b := store.Agents["agent-b"]
	b.LastSeenAt = now.Add(-10 * time.Minute)
	store.Agents["agent-b"] = b
	store.mu.Unlock()
	if _, err := SelectRestoreAffinity(ctx, store, g.ID, g.MetaPath, cfg); err == nil {
		t.Fatal("restore should fail without online ready replica")
	}
}

func TestRestoreApprovalRequiresSecondActorAndExpires(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().UTC()
	a := RestoreApproval{ID: "r1", Repository: "acme/demo", GenerationID: "g1", RequestedBy: "alice", Status: ApprovalPending, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := s.CreateRestoreApproval(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.ApproveRestore(ctx, "r1", "alice", true); err == nil {
		t.Fatal("self approval accepted")
	}
	if err := s.ApproveRestore(ctx, "r1", "bob", true); err != nil {
		t.Fatal(err)
	}
	if err := s.ScheduleRestore(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRestoreExecuted(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetRestoreApproval(ctx, "r1")
	if got.Status != ApprovalExecuted || got.ApprovedBy != "bob" {
		t.Fatalf("unexpected approval %#v", got)
	}
	exp := RestoreApproval{ID: "r2", Repository: "acme/demo", GenerationID: "g1", RequestedBy: "alice", Status: ApprovalPending, CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Second)}
	_ = s.CreateRestoreApproval(ctx, exp)
	if err := s.ApproveRestore(ctx, "r2", "bob", true); err == nil {
		t.Fatal("expired approval accepted")
	}
}

func TestRestoreJobCompletionConsumesScheduledApprovalAtomically(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().UTC()
	a := RestoreApproval{ID: "r3", Repository: "acme/demo", GenerationID: "g1", RequestedBy: "alice", ApprovedBy: "bob", Status: ApprovalApproved, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := s.CreateRestoreApproval(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.ScheduleRestore(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	payload := `{"repository":"acme/demo","generation_id":"g1","approval_id":"r3"}`
	j, _, err := s.Enqueue(ctx, Job{Kind: "restore-generation", Target: "restore:r3", Payload: payload, MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	xs, err := s.Lease(ctx, "worker", 1, time.Minute)
	if err != nil || len(xs) != 1 {
		t.Fatalf("lease %#v %v", xs, err)
	}
	if err := s.Complete(ctx, j.ID, "worker"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetRestoreApproval(ctx, a.ID)
	if got.Status != ApprovalExecuted {
		t.Fatalf("approval not executed: %#v", got)
	}
}

func TestReplicationHealthDetectsOfflineDeficit(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().UTC()
	_ = replAgent(t, s, "agent-a", now)
	_ = replAgent(t, s, "agent-b", now.Add(-10*time.Minute))
	g := Generation{ID: "gen-health", RepositoryID: "repo-health", Repository: "acme/health", CreatedAt: now}
	_ = s.RecordGeneration(ctx, g)
	_ = s.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: "agent-a", State: ReplicaReady})
	_ = s.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: "agent-b", State: ReplicaReady})
	cfg := config.Default().ControlPlane.Replication
	cfg.Enabled = true
	cfg.IncludeLocal = false
	cfg.MinHealthy = 2
	cfg.AgentTimeout = "2m"
	rep, err := ReplicationHealth(ctx, s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Deficits != 1 || rep.HealthyGenerations != 0 || rep.OnlineReadyReplicas != 1 {
		t.Fatalf("unexpected health %#v", rep)
	}
}

func TestExpiredReplicationTransferCleanup(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	cfg := config.Default().ControlPlane.Replication
	cfg.SpoolRoot = t.TempDir()
	cfg.TransferTTL = "1h"
	now := time.Now().UTC()
	old := ReplicationTransfer{ID: "old", GenerationID: "g", RepositoryID: "r", SourceAgent: "a", TargetAgent: "b", State: TransferReady, ExpiresAt: now.Add(-time.Minute)}
	fresh := ReplicationTransfer{ID: "fresh", GenerationID: "g", RepositoryID: "r", SourceAgent: "a", TargetAgent: "b", State: TransferReady, ExpiresAt: now.Add(time.Hour)}
	if err := s.RecordReplicationTransfer(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordReplicationTransfer(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replicationSpoolPath(cfg.SpoolRoot, old.ID), []byte("cipher"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := CleanupExpiredReplicationTransfers(ctx, s, cfg, now)
	if err != nil || n != 1 {
		t.Fatalf("cleanup n=%d err=%v", n, err)
	}
	if _, err := s.GetReplicationTransfer(ctx, old.ID); err == nil {
		t.Fatal("expired transfer persisted")
	}
	if _, err := s.GetReplicationTransfer(ctx, fresh.ID); err != nil {
		t.Fatal("fresh transfer removed")
	}
	if _, err := os.Stat(replicationSpoolPath(cfg.SpoolRoot, old.ID)); !os.IsNotExist(err) {
		t.Fatalf("spool not removed: %v", err)
	}
}

func TestReplicationPlacementPrefersNewFailureDomain(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().UTC()
	for id, zone := range map[string]string{"agent-a": "z1", "agent-b": "z1", "agent-c": "z2"} {
		a := replAgent(t, s, id, now)
		a.LabelsJSON = `{"zone":"` + zone + `"}`
		s.mu.Lock()
		s.Agents[id] = a
		s.mu.Unlock()
	}
	g := Generation{ID: "gen-zone", RepositoryID: "repo-zone", Repository: "acme/zone", CreatedAt: now}
	_ = s.RecordGeneration(ctx, g)
	_ = s.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: "agent-a", State: ReplicaReady})
	cfg := config.Default()
	cfg.ControlPlane.Replication.Enabled = true
	cfg.ControlPlane.Replication.IncludeLocal = false
	cfg.ControlPlane.Replication.Factor = 2
	cfg.ControlPlane.Replication.MinHealthy = 2
	cfg.ControlPlane.Replication.FailureDomainLabel = "zone"
	cfg.ControlPlane.Replication.MinFailureDomains = 2
	n, err := (ReplicationReconciler{Store: s, Config: cfg}).Reconcile(ctx)
	if err != nil || n != 1 {
		t.Fatalf("reconcile n=%d err=%v", n, err)
	}
	jobs, _ := s.ListJobs(ctx, 10)
	if len(jobs) != 1 || jobs[0].Target != "gen-zone@agent-c" {
		t.Fatalf("expected new failure domain agent-c, got %#v", jobs)
	}
}

func TestReplicationHealthRejectsSingleFailureDomain(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().UTC()
	for _, id := range []string{"agent-a", "agent-b"} {
		a := replAgent(t, s, id, now)
		a.LabelsJSON = `{"zone":"z1"}`
		s.mu.Lock()
		s.Agents[id] = a
		s.mu.Unlock()
	}
	g := Generation{ID: "gen-domain-health", RepositoryID: "repo-domain", Repository: "acme/domain", CreatedAt: now}
	_ = s.RecordGeneration(ctx, g)
	for _, id := range []string{"agent-a", "agent-b"} {
		_ = s.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: id, State: ReplicaReady})
	}
	cfg := config.Default().ControlPlane.Replication
	cfg.Enabled = true
	cfg.IncludeLocal = false
	cfg.MinHealthy = 2
	cfg.FailureDomainLabel = "zone"
	cfg.MinFailureDomains = 2
	rep, err := ReplicationHealth(ctx, s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Deficits != 1 || rep.FailureDomainDeficits != 1 {
		t.Fatalf("unexpected report %#v", rep)
	}
}

func TestExcludedReplicaDoesNotSatisfyPlacementFactor(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().UTC()
	_ = replAgent(t, s, "retiring", now)
	_ = replAgent(t, s, "primary", now)
	_ = replAgent(t, s, "replacement", now)
	g := Generation{ID: "gen-retire", RepositoryID: "repo-retire", Repository: "acme/retire", CreatedAt: now}
	_ = s.RecordGeneration(ctx, g)
	_ = s.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: "retiring", State: ReplicaReady})
	_ = s.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: "primary", State: ReplicaReady})
	cfg := config.Default()
	r := &cfg.ControlPlane.Replication
	r.Enabled = true
	r.IncludeLocal = false
	r.Factor = 2
	r.MinHealthy = 1
	r.ExcludedAgents = []string{"retiring"}
	n, err := (ReplicationReconciler{Store: s, Config: cfg}).Reconcile(ctx)
	if err != nil || n != 1 {
		t.Fatalf("expected replacement n=%d err=%v", n, err)
	}
	jobs, _ := s.ListJobs(ctx, 10)
	if len(jobs) != 1 || jobs[0].Target != "gen-retire@replacement" {
		t.Fatalf("unexpected replacement %#v", jobs)
	}
}
