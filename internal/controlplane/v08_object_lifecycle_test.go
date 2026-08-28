package controlplane

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/storagehealth"
)

func TestObjectRefOwnerIsIdempotentAndProtectsGC(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	d := strings.Repeat("a", 64)
	created, err := st.EnsureObjectRef(ctx, ObjectRef{Digest: d, Kind: "erasure-shard", Bytes: 42}, "set:0")
	if err != nil || !created {
		t.Fatalf("first ensure %t %v", created, err)
	}
	created, err = st.EnsureObjectRef(ctx, ObjectRef{Digest: d, Kind: "erasure-shard", Bytes: 42}, "set:0")
	if err != nil || created {
		t.Fatalf("retry ensure %t %v", created, err)
	}
	r, ok, err := st.GetObjectRef(ctx, d)
	if err != nil || !ok || r.RefCount != 1 {
		t.Fatalf("ref=%+v ok=%t err=%v", r, ok, err)
	}
	roots, err := st.ProtectedObjectDigests(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := roots[d]; !ok {
		t.Fatal("ref-counted digest not protected")
	}
	released, err := st.ReleaseObjectRef(ctx, d, "set:0")
	if err != nil || !released {
		t.Fatalf("release %t %v", released, err)
	}
}

func TestScheduleDistributedRepairPinsToCorruptTarget(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	now := time.Now().UTC()
	for _, id := range []string{"source-a", "source-b", "target"} {
		a := agentWithZone(id, map[string]string{"source-a": "z1", "source-b": "z2", "target": "z3"}[id])
		a.LastSeenAt = now
		a.StorageHealth = storagehealth.Healthy
		a.ReplicationPublicKey = strings.Repeat("A", 43)
		if err := st.HeartbeatAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	obj := strings.Repeat("b", 64)
	_ = st.RecordErasureSet(ctx, ErasureSet{ObjectSHA256: obj, OriginalBytes: 100, DataShards: 2, ParityShards: 1, BlockBytes: 65536, State: ErasureReady})
	for i, id := range []string{"source-a", "source-b", "source-a"} {
		_ = st.RecordErasureShard(ctx, ErasureShard{ObjectSHA256: obj, ShardIndex: i, ShardSHA256: strings.Repeat(string(rune('c'+i)), 64), AgentID: id, State: ShardReady, Bytes: 65536})
	}
	cfg := config.Default()
	cfg.ControlPlane.Workers.MaxAttempts = 5
	cfg.ControlPlane.Replication.AgentTimeout = "2m"
	cfg.ControlPlane.Replication.TransferTTL = "1h"
	cfg.ControlPlane.Storage.Enabled = true
	n, err := ScheduleDistributedRepair(ctx, st, cfg, "target", obj)
	if err != nil {
		t.Fatal(err)
	}
	if n < 3 {
		t.Fatalf("expected shard transfers + repair job, queued=%d", n)
	}
	jobs, _ := st.ListJobs(ctx, 100)
	repair := 0
	for _, j := range jobs {
		if j.Kind == "repair-object" {
			repair++
			if j.Affinity != "target" {
				t.Fatalf("repair affinity=%q", j.Affinity)
			}
		}
	}
	if repair != 1 {
		t.Fatalf("repair jobs=%d", repair)
	}
}
