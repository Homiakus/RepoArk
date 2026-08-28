package controlplane

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/storagehealth"
)

func TestDesiredShardAgentsSpreadsPrimaryIndicesAcrossDomains(t *testing.T) {
	agents := map[string]Agent{
		"a": agentWithZone("a", "z1"),
		"b": agentWithZone("b", "z2"),
		"c": agentWithZone("c", "z3"),
	}
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		x := desiredShardAgents("deadbeef", i, 1, agents, "zone")
		if len(x) != 1 {
			t.Fatalf("index %d targets=%v", i, x)
		}
		seen[agentFailureDomain(agents[x[0]], "zone")] = true
	}
	if len(seen) != 3 {
		t.Fatalf("expected all zones represented, got %v", seen)
	}
}

func TestDistributedErasureHealthSurvivesAnyOneZone(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	now := time.Now().UTC()
	for _, a := range []Agent{agentWithZone("a", "z1"), agentWithZone("b", "z2"), agentWithZone("c", "z3")} {
		a.LastSeenAt = now
		a.StorageHealth = storagehealth.Healthy
		if err := st.HeartbeatAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	set := ErasureSet{ObjectSHA256: "obj", OriginalBytes: 100, DataShards: 2, ParityShards: 2, BlockBytes: 65536, State: ErasureReady}
	if err := st.RecordErasureSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	// Four unique indices, deliberately spread such that losing any one zone
	// still leaves at least two indices.
	copies := []ErasureShard{
		{ObjectSHA256: "obj", ShardIndex: 0, ShardSHA256: "s0", AgentID: "a", State: ShardReady},
		{ObjectSHA256: "obj", ShardIndex: 1, ShardSHA256: "s1", AgentID: "b", State: ShardReady},
		{ObjectSHA256: "obj", ShardIndex: 2, ShardSHA256: "s2", AgentID: "c", State: ShardReady},
		{ObjectSHA256: "obj", ShardIndex: 3, ShardSHA256: "s3", AgentID: "a", State: ShardReady},
	}
	for _, sh := range copies {
		if err := st.RecordErasureShard(ctx, sh); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.ControlPlane.Storage.Enabled = true
	cfg.ControlPlane.Storage.Erasure.Enabled = true
	cfg.ControlPlane.Storage.Erasure.Distributed = true
	cfg.ControlPlane.Storage.Erasure.DataShards = 2
	cfg.ControlPlane.Storage.Erasure.ParityShards = 2
	cfg.ControlPlane.Storage.Erasure.FailureDomainLabel = "zone"
	cfg.ControlPlane.Storage.Erasure.MinFailureDomains = 2
	cfg.ControlPlane.Storage.Erasure.ShardPoolLabel = "cas_pool"
	cfg.ControlPlane.Replication.AgentTimeout = "2m"
	h, err := EvaluateDistributedErasureHealth(ctx, st, cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	if h.Healthy != 1 || h.Unrecoverable != 0 || h.FailureDomainDeficits != 0 {
		t.Fatalf("health=%+v", h)
	}
}

func TestDistributedErasureHealthDetectsSingleZoneTrap(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	now := time.Now().UTC()
	for _, id := range []string{"a", "b"} {
		a := agentWithZone(id, "z1")
		a.LastSeenAt = now
		a.StorageHealth = storagehealth.Healthy
		_ = st.HeartbeatAgent(ctx, a)
	}
	_ = st.RecordErasureSet(ctx, ErasureSet{ObjectSHA256: "obj", DataShards: 2, ParityShards: 1, State: ErasureReady})
	for i, id := range []string{"a", "b", "a"} {
		_ = st.RecordErasureShard(ctx, ErasureShard{ObjectSHA256: "obj", ShardIndex: i, ShardSHA256: "s", AgentID: id, State: ShardReady})
	}
	cfg := config.Default()
	cfg.ControlPlane.Storage.Enabled = true
	cfg.ControlPlane.Storage.Erasure.Enabled = true
	cfg.ControlPlane.Storage.Erasure.Distributed = true
	cfg.ControlPlane.Storage.Erasure.FailureDomainLabel = "zone"
	cfg.ControlPlane.Storage.Erasure.MinFailureDomains = 2
	cfg.ControlPlane.Storage.Erasure.ShardPoolLabel = "cas_pool"
	cfg.ControlPlane.Replication.AgentTimeout = "2m"
	h, err := EvaluateDistributedErasureHealth(ctx, st, cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	if h.Healthy != 0 || h.FailureDomainDeficits == 0 {
		t.Fatalf("health=%+v", h)
	}
}

func agentWithZone(id, zone string) Agent {
	labels, _ := json.Marshal(map[string]string{"zone": zone, "cas_pool": "default"})
	return Agent{ID: id, Name: id, LabelsJSON: string(labels), ReplicationPublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", Status: "online", StorageHealth: storagehealth.Healthy, StorageFreeBytes: 1 << 40, StorageFreePercent: 90}
}
