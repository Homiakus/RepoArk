package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/storagehealth"
)

// TestChaosStorageNodeLoss exercises the data-plane invariant that restore
// availability follows verified replica placement, not control-plane liveness.
// No GitHub or GitLab API is needed once a durable generation exists.
func TestChaosStorageNodeLossFailoverAndFailback(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().UTC()
	a := replAgent(t, s, "storage-a", now)
	b := replAgent(t, s, "storage-b", now)
	c := replAgent(t, s, "storage-c", now)
	a.LabelsJSON = `{"zone":"z1"}`
	b.LabelsJSON = `{"zone":"z2"}`
	c.LabelsJSON = `{"zone":"z3"}`
	s.mu.Lock()
	s.Agents[a.ID] = a
	s.Agents[b.ID] = b
	s.Agents[c.ID] = c
	s.mu.Unlock()

	g := Generation{ID: "chaos-gen", RepositoryID: "chaos-repo", Repository: "acme/critical", MetaPath: "agent://storage-a/generation.json", CreatedAt: now, Verified: true}
	_ = s.RecordGeneration(ctx, g)
	_ = s.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: "storage-a", State: ReplicaReady, VerifiedAt: now})
	_ = s.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: "storage-b", State: ReplicaReady, VerifiedAt: now})

	cfg := config.Default().ControlPlane.Replication
	cfg.Enabled = true
	cfg.IncludeLocal = false
	cfg.Factor = 2
	cfg.MinHealthy = 2
	cfg.AgentTimeout = "2m"
	cfg.FailureDomainLabel = "zone"
	cfg.MinFailureDomains = 2

	// Simulate total loss of the original storage node. Restore must fail over
	// to B, while placement queues a replacement on C.
	s.mu.Lock()
	a = s.Agents["storage-a"]
	a.LastSeenAt = now.Add(-10 * time.Minute)
	s.Agents[a.ID] = a
	s.mu.Unlock()
	affinity, err := SelectRestoreAffinity(ctx, s, g.ID, g.MetaPath, cfg)
	if err != nil || affinity != "storage-b" {
		t.Fatalf("safe failover got=%q err=%v", affinity, err)
	}

	full := config.Default()
	full.ControlPlane.Replication = cfg
	n, err := (ReplicationReconciler{Store: s, Config: full}).Reconcile(ctx)
	if err != nil || n != 1 {
		t.Fatalf("replacement placement n=%d err=%v", n, err)
	}
	jobs, _ := s.ListJobs(ctx, 10)
	if len(jobs) != 1 || jobs[0].Target != "chaos-gen@storage-c" || jobs[0].Affinity != "storage-b" {
		t.Fatalf("unexpected replacement job %#v", jobs)
	}

	// Original node comes back. Preferred ownership is selected again: safe
	// failback is routing-only and never deletes the replacement replica.
	s.mu.Lock()
	a = s.Agents["storage-a"]
	a.LastSeenAt = time.Now().UTC()
	s.Agents[a.ID] = a
	s.mu.Unlock()
	affinity, err = SelectRestoreAffinity(ctx, s, g.ID, g.MetaPath, cfg)
	if err != nil || affinity != "storage-a" {
		t.Fatalf("safe failback got=%q err=%v", affinity, err)
	}
}

func TestChaosDegradedStorageEvacuatesToCapacityAwareTarget(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().UTC()
	for _, id := range []string{"degraded", "too-small", "healthy"} {
		_ = replAgent(t, s, id, now)
	}
	s.mu.Lock()
	a := s.Agents["degraded"]
	a.StorageHealth = storagehealth.Degraded
	s.Agents[a.ID] = a
	b := s.Agents["too-small"]
	b.StorageFreeBytes = 12 << 30
	b.StorageTotalBytes = 100 << 30
	b.StorageFreePercent = 12
	s.Agents[b.ID] = b
	c := s.Agents["healthy"]
	c.StorageFreeBytes = 80 << 30
	c.StorageTotalBytes = 100 << 30
	c.StorageFreePercent = 80
	s.Agents[c.ID] = c
	s.mu.Unlock()

	g := Generation{ID: "gen-evacuate", RepositoryID: "repo-evacuate", Repository: "acme/evacuate", MetaPath: "agent://degraded/x", CreatedAt: now, Verified: true}
	_ = s.RecordGeneration(ctx, g)
	_ = s.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: "degraded", State: ReplicaReady, Bytes: 5 << 30, VerifiedAt: now})

	cfg := config.Default()
	cfg.ControlPlane.Replication.Enabled = true
	cfg.ControlPlane.Replication.IncludeLocal = false
	cfg.ControlPlane.Replication.Factor = 2
	cfg.ControlPlane.Replication.MinHealthy = 1
	cfg.ControlPlane.Storage.EvacuateDegraded = true
	cfg.ControlPlane.Storage.MinFreeBytes = 10 << 30
	cfg.ControlPlane.Storage.MinFreePercent = 10

	n, err := (ReplicationReconciler{Store: s, Config: cfg}).Reconcile(ctx)
	if err != nil || n != 1 {
		t.Fatalf("evacuation n=%d err=%v", n, err)
	}
	jobs, _ := s.ListJobs(ctx, 10)
	if len(jobs) != 1 || jobs[0].Affinity != "degraded" || jobs[0].Target != "gen-evacuate@healthy" {
		t.Fatalf("capacity-aware evacuation selected wrong job: %#v", jobs)
	}
}
