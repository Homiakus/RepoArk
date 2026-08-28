package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/cas"
	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/erasure"
)

type repairShard struct {
	Index  int    `json:"index"`
	Digest string `json:"digest"`
	Bytes  int64  `json:"bytes"`
}
type repairObjectPayload struct {
	Digest        string        `json:"digest"`
	OriginalBytes int64         `json:"original_bytes"`
	DataShards    int           `json:"data_shards"`
	ParityShards  int           `json:"parity_shards"`
	BlockBytes    int           `json:"block_bytes"`
	Shards        []repairShard `json:"shards"`
}

type corruptionReporter interface {
	ReportCorruptObject(context.Context, string) error
}

func (r Runner) repairObject(ctx context.Context, job Job) error {
	var p repairObjectPayload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		return err
	}
	if len(p.Digest) != 64 || p.DataShards < 2 || p.BlockBytes <= 0 {
		return fmt.Errorf("invalid repair-object payload")
	}
	stage, err := os.MkdirTemp(filepath.Join(r.Config.CAS.Root, ".repoark-repair"), "obj-")
	if err != nil {
		if e := os.MkdirAll(filepath.Join(r.Config.CAS.Root, ".repoark-repair"), 0o700); e != nil {
			return e
		}
		stage, err = os.MkdirTemp(filepath.Join(r.Config.CAS.Root, ".repoark-repair"), "obj-")
	}
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	manifest := erasure.Manifest{Version: 1, ObjectSHA256: p.Digest, OriginalBytes: p.OriginalBytes, DataShards: p.DataShards, ParityShards: p.ParityShards, BlockBytes: p.BlockBytes, CreatedAt: time.Now().UTC()}
	var blocks int64
	valid := 0
	for _, sh := range p.Shards {
		path := cas.New(r.Config.CAS.Root, 0).BlobPath(sh.Digest)
		got, n, e := cas.HashFile(path)
		if e != nil || !strings.EqualFold(got, sh.Digest) {
			continue
		}
		name := fmt.Sprintf("shard-%03d.rs", sh.Index)
		dst := filepath.Join(stage, name)
		if e := os.Link(path, dst); e != nil {
			if e = copyRegularFile(path, dst); e != nil {
				return e
			}
		}
		manifest.Shards = append(manifest.Shards, erasure.Shard{Index: sh.Index, Name: name, Bytes: n, SHA256: sh.Digest})
		if p.BlockBytes > 0 && n/int64(p.BlockBytes) > blocks {
			blocks = n / int64(p.BlockBytes)
		}
		valid++
	}
	if valid < p.DataShards {
		return fmt.Errorf("repair shards not available yet: %d < %d", valid, p.DataShards)
	}
	manifest.Blocks = blocks
	b, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(stage, "manifest.json"), append(b, '\n'), 0o600); err != nil {
		return err
	}
	tmp := filepath.Join(stage, "reconstructed.object")
	if err := erasure.Reconstruct(stage, tmp); err != nil {
		return err
	}
	got, _, err := cas.HashFile(tmp)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, p.Digest) {
		return fmt.Errorf("distributed repair digest mismatch")
	}
	// Preserve existing hard links by rewriting the corrupted CAS inode in-place.
	target := cas.New(r.Config.CAS.Root, 0).BlobPath(p.Digest)
	if err := overwriteVerified(tmp, target, p.Digest); err != nil {
		return err
	}
	if r.Emit != nil {
		r.Emit("distributed repair completed " + p.Digest)
	}
	return nil
}

func copyRegularFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}
func overwriteVerified(src, dst, digest string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	got, _, err := cas.HashFile(dst)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, digest) {
		return fmt.Errorf("post-repair digest mismatch")
	}
	return nil
}

// ScheduleDistributedRepair transfers a minimal set of distinct shard indices
// to target, then queues a retryable repair job on that same storage node.
func ScheduleDistributedRepair(ctx context.Context, st Store, cfg config.Config, target, digest string) (int, error) {
	set, ok, err := st.GetErasureSet(ctx, digest)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("no erasure set for %s", digest)
	}
	shards, err := st.ListErasureShards(ctx, digest)
	if err != nil {
		return 0, err
	}
	agents, err := st.ListAgents(ctx)
	if err != nil {
		return 0, err
	}
	timeout, _ := time.ParseDuration(cfg.ControlPlane.Replication.AgentTimeout)
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	now := time.Now().UTC()
	online := map[string]Agent{}
	for _, a := range agents {
		if now.Sub(a.LastSeenAt) <= timeout && agentReadable(a, cfg.ControlPlane.Storage) && strings.TrimSpace(a.ReplicationPublicKey) != "" {
			online[a.ID] = a
		}
	}
	ta, ok := online[target]
	if !ok {
		return 0, fmt.Errorf("repair target %s is offline/unreadable", target)
	}
	byIndex := map[int][]ErasureShard{}
	for _, sh := range shards {
		if sh.State == ShardReady {
			byIndex[sh.ShardIndex] = append(byIndex[sh.ShardIndex], sh)
		}
	}
	indices := make([]int, 0, len(byIndex))
	for i := range byIndex {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	chosen := make([]repairShard, 0, set.DataShards)
	queued := 0
	for _, idx := range indices {
		if len(chosen) >= set.DataShards {
			break
		}
		xs := byIndex[idx]
		var candidate *ErasureShard
		for i := range xs {
			if _, ok := online[xs[i].AgentID]; ok {
				candidate = &xs[i]
				break
			}
		}
		if candidate == nil {
			continue
		}
		chosen = append(chosen, repairShard{Index: idx, Digest: candidate.ShardSHA256, Bytes: candidate.Bytes})
		if candidate.AgentID == target {
			continue
		}
		transferID := newID("repairshard")
		leaseID := "repair:" + transferID
		ttl, _ := time.ParseDuration(cfg.ControlPlane.Replication.TransferTTL)
		if ttl <= 0 {
			ttl = 2 * time.Hour
		}
		if err := st.AcquireObjectLease(ctx, ObjectLease{ID: leaseID, Digest: candidate.ShardSHA256, Owner: "repair:" + digest + "@" + target, ExpiresAt: now.Add(ttl)}); err != nil {
			return queued, err
		}
		p := replicateCASPayload{TransferID: transferID, SourceAgent: candidate.AgentID, TargetAgent: target, TargetReplicationPubKey: ta.ReplicationPublicKey, PoolAgents: []string{target}, Factor: 1, Digests: []string{candidate.ShardSHA256}, Erasure: &erasureShardTransfer{ObjectSHA256: digest, ShardIndex: idx, ShardSHA256: candidate.ShardSHA256, ShardBytes: candidate.Bytes, LeaseID: leaseID}}
		pb, _ := json.Marshal(p)
		_, created, e := st.Enqueue(ctx, Job{Kind: "replicate-cas", Target: fmt.Sprintf("repairshard:%s:%d@%s", digest, idx, target), Payload: string(pb), Affinity: candidate.AgentID, Priority: 190, MaxAttempts: cfg.ControlPlane.Workers.MaxAttempts, NotBefore: now})
		if e != nil {
			return queued, e
		}
		if created {
			queued++
		}
	}
	if len(chosen) < set.DataShards {
		return queued, fmt.Errorf("only %d/%d distinct repair shards online", len(chosen), set.DataShards)
	}
	p := repairObjectPayload{Digest: digest, OriginalBytes: set.OriginalBytes, DataShards: set.DataShards, ParityShards: set.ParityShards, BlockBytes: set.BlockBytes, Shards: chosen}
	b, _ := json.Marshal(p)
	_, created, err := st.Enqueue(ctx, Job{Kind: "repair-object", Target: "repair:" + digest + "@" + target, Payload: string(b), Affinity: target, Priority: 180, MaxAttempts: maxInt(cfg.ControlPlane.Workers.MaxAttempts, 8), NotBefore: now.Add(5 * time.Second)})
	if err != nil {
		return queued, err
	}
	if created {
		queued++
	}
	return queued, nil
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
