package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/objectinventory"
	"github.com/Homiakus/repoark/internal/replication"
	"github.com/Homiakus/repoark/internal/storagehealth"
)

func TestRestoreRoutingRejectsUnhealthyStorage(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().UTC()
	for _, id := range []string{"bad", "good"} {
		a := replAgent(t, s, id, now)
		a.StorageHealth = storagehealth.Healthy
		if id == "bad" {
			a.StorageHealth = storagehealth.Unhealthy
		}
		s.mu.Lock()
		s.Agents[id] = a
		s.mu.Unlock()
	}
	g := Generation{ID: "g-storage", RepositoryID: "r", Repository: "acme/demo", MetaPath: "agent://bad/x", CreatedAt: now}
	_ = s.RecordGeneration(ctx, g)
	_ = s.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: "bad", State: ReplicaReady})
	_ = s.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: "good", State: ReplicaReady})
	rcfg := config.Default().ControlPlane.Replication
	rcfg.IncludeLocal = false
	scfg := config.Default().ControlPlane.Storage
	got, err := SelectRestoreAffinityWithStorage(ctx, s, g.ID, g.MetaPath, rcfg, scfg)
	if err != nil || got != "good" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestCASReconcilerUsesMerkleDiffAndPool(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().UTC()
	cfg := config.Default()
	cfg.ControlPlane.Replication.Enabled = true
	cfg.ControlPlane.Replication.IncludeLocal = false
	cfg.ControlPlane.Storage.ObjectReplicationFactor = 2
	cfg.ControlPlane.Storage.ObjectPoolLabel = "cas_pool"
	for i, id := range []string{"a", "b"} {
		pub, err := replication.EnsureKey(filepath.Join(t.TempDir(), id+".key"))
		if err != nil {
			t.Fatal(err)
		}
		inv := objectinventory.Inventory{Version: 1, MerkleRoot: []string{"root-a", "root-b"}[i], Objects: 1, Bytes: 10, Segments: []objectinventory.Segment{{Prefix: "aa", Root: []string{"seg-a", "seg-b"}[i], Count: 1, Bytes: 10}}}
		a := Agent{ID: id, Name: id, ReplicationPublicKey: pub, LastSeenAt: now, StorageHealth: storagehealth.Healthy, StorageFreeBytes: 100 << 30, StorageTotalBytes: 200 << 30, StorageFreePercent: 50, LabelsJSON: `{"cas_pool":"pool-1"}`, InventoryRoot: inv.MerkleRoot, InventoryObjects: 1, InventoryBytes: 10, InventoryJSON: objectinventory.EncodeCompact(inv)}
		s.mu.Lock()
		s.Agents[id] = a
		s.mu.Unlock()
	}
	n, err := (CASReconciler{Store: s, Config: cfg}).Reconcile(ctx)
	if err != nil || n == 0 {
		t.Fatalf("queued=%d err=%v", n, err)
	}
	jobs, _ := s.ListJobs(ctx, 10)
	for _, j := range jobs {
		if j.Kind == "replicate-cas" {
			var p replicateCASPayload
			if json.Unmarshal([]byte(j.Payload), &p) != nil || len(p.Prefixes) != 1 || p.Prefixes[0] != "aa" {
				t.Fatalf("bad payload %#v", p)
			}
			return
		}
	}
	t.Fatal("replicate-cas job missing")
}

func TestResumableFinalizeIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	cfg := config.Default()
	rcfg := cfg.ControlPlane.Replication
	rcfg.Enabled = true
	rcfg.SpoolRoot = t.TempDir()
	rcfg.TransferTTL = "1h"
	rcfg.MaxTransferBytes = 1 << 20
	pub, err := replication.EnsureKey(filepath.Join(t.TempDir(), "target.key"))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.HeartbeatAgent(ctx, Agent{ID: "b", Name: "b", ReplicationPublicKey: pub})
	p := replicateGenerationPayload{TransferID: "resume-1", RepositoryID: "r", Repository: "acme/demo", GenerationID: "g", SourceAgent: "a", TargetAgent: "b", TargetReplicationPubKey: pub}
	pb, _ := json.Marshal(p)
	j, _, _ := s.Enqueue(ctx, Job{Kind: "replicate-generation", Target: "g@b", Payload: string(pb), Affinity: "a", MaxAttempts: 3})
	_, _ = s.Lease(ctx, "a", 1, time.Minute)
	srv := AgentServer{Store: s, Worker: config.WorkerConfig{MaxAttempts: 3}, Replication: rcfg, Storage: config.StorageDataConfig{ChunkBytes: 1 << 20}}
	h := srv.handler()
	chunk := []byte("resumable-cipher")
	sum := sha256.Sum256(chunk)
	r := rawAgentRequest(http.MethodPatch, "/api/v1/agent/jobs/"+j.ID+"/replication/upload/resume-1", "a", chunk)
	r.Header.Set("X-RepoArk-Offset", "0")
	r.Header.Set("X-RepoArk-Chunk-SHA256", hex.EncodeToString(sum[:]))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != 200 {
		t.Fatalf("patch %d %s", rr.Code, rr.Body.String())
	}
	body, _ := json.Marshal(replicationFinalizeRequest{SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(chunk))})
	for i := 0; i < 2; i++ {
		rr = httptest.NewRecorder()
		h.ServeHTTP(rr, handlerRequest(http.MethodPost, "/api/v1/agent/jobs/"+j.ID+"/replication/upload/resume-1/finalize", "a", json.RawMessage(body)))
		if rr.Code != 200 {
			t.Fatalf("finalize[%d]=%d %s", i, rr.Code, rr.Body.String())
		}
	}
	tr, err := s.GetReplicationTransfer(ctx, "resume-1")
	if err != nil || tr.State != TransferReady || tr.Bytes != int64(len(chunk)) {
		t.Fatalf("transfer %#v %v", tr, err)
	}
	jobs, _ := s.ListJobs(ctx, 20)
	installs := 0
	for _, x := range jobs {
		if x.Kind == "install-replica" {
			installs++
		}
	}
	if installs != 1 {
		t.Fatalf("install jobs=%d %#v", installs, jobs)
	}
}

func TestResumableDownloadAcceptsAlreadyCompleteVerifiedPartial(t *testing.T) {
	data := []byte("already-complete-ciphertext")
	sum := sha256.Sum256(data)
	path := filepath.Join(t.TempDir(), "transfer.cipher.part")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("network request made for an already complete verified file")
		return nil, fmt.Errorf("unexpected network request")
	})}
	r := NewRemoteStore(client, "https://control.invalid", "agent-a")
	r.SetJob("job-1", "target")
	if err := r.DownloadReplicationFile(context.Background(), "xfer-1", path, int64(len(data)), hex.EncodeToString(sum[:]), 3, 0); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUnknownStorageHealthIsReadableButNotNewPlacement(t *testing.T) {
	cfg := config.Default().ControlPlane.Storage
	a := Agent{ID: "old-agent"}
	if !agentReadable(a, cfg) {
		t.Fatal("mixed-version agent should remain readable during upgrade")
	}
	if agentAcceptsPlacement(a, cfg) || agentCountsDurable(a, cfg) {
		t.Fatal("unknown storage health must not receive or satisfy new durable placement")
	}
}

func TestCASRendezvousTargetsExcludeDegradedAgents(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().UTC()
	cfg := config.Default()
	cfg.ControlPlane.Replication.Enabled = true
	cfg.ControlPlane.Replication.IncludeLocal = false
	cfg.ControlPlane.Storage.ObjectReplicationFactor = 2
	cfg.ControlPlane.Storage.ObjectPoolLabel = "cas_pool"
	cfg.ControlPlane.Storage.EvacuateDegraded = true

	for _, id := range []string{"source", "healthy", "degraded"} {
		pub, err := replication.EnsureKey(filepath.Join(t.TempDir(), id+".key"))
		if err != nil {
			t.Fatal(err)
		}
		segRoot := "empty"
		root := "root-empty"
		count := 0
		bytesN := int64(0)
		if id == "source" {
			segRoot, root, count, bytesN = "source-seg", "source-root", 1, 1024
		}
		inv := objectinventory.Inventory{Version: 1, MerkleRoot: root, Objects: count, Bytes: bytesN, Segments: []objectinventory.Segment{{Prefix: "aa", Root: segRoot, Count: count, Bytes: bytesN}}}
		health := storagehealth.Healthy
		if id != "healthy" {
			health = storagehealth.Degraded
		}
		a := Agent{ID: id, Name: id, ReplicationPublicKey: pub, LastSeenAt: now, StorageHealth: health, StorageTotalBytes: 100 << 30, StorageFreeBytes: 80 << 30, StorageFreePercent: 80, LabelsJSON: `{"cas_pool":"pool-1"}`, InventoryRoot: root, InventoryJSON: objectinventory.EncodeCompact(inv)}
		s.mu.Lock()
		s.Agents[id] = a
		s.mu.Unlock()
	}
	n, err := (CASReconciler{Store: s, Config: cfg}).Reconcile(ctx)
	if err != nil || n != 1 {
		t.Fatalf("queued=%d err=%v", n, err)
	}
	jobs, _ := s.ListJobs(ctx, 10)
	for _, j := range jobs {
		if j.Kind != "replicate-cas" {
			continue
		}
		var p replicateCASPayload
		if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
			t.Fatal(err)
		}
		if p.SourceAgent != "source" || p.TargetAgent != "healthy" || p.Factor != 1 || len(p.PoolAgents) != 1 || p.PoolAgents[0] != "healthy" {
			t.Fatalf("degraded target leaked into rendezvous placement: %#v", p)
		}
		return
	}
	t.Fatal("replicate-cas job not found")
}
