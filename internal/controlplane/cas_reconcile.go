package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/objectinventory"
)

type erasureShardTransfer struct {
	ObjectSHA256 string `json:"object_sha256"`
	ShardIndex   int    `json:"shard_index"`
	ShardSHA256  string `json:"shard_sha256"`
	ShardBytes   int64  `json:"shard_bytes"`
	LeaseID      string `json:"lease_id,omitempty"`
}

type replicateCASPayload struct {
	TransferID              string                `json:"transfer_id"`
	SourceAgent             string                `json:"source_agent"`
	TargetAgent             string                `json:"target_agent"`
	TargetReplicationPubKey string                `json:"target_replication_public_key"`
	PoolAgents              []string              `json:"pool_agents"`
	Factor                  int                   `json:"factor"`
	Prefixes                []string              `json:"prefixes,omitempty"`
	Digests                 []string              `json:"digests,omitempty"`
	Erasure                 *erasureShardTransfer `json:"erasure,omitempty"`
	SourceInventoryRoot     string                `json:"source_inventory_root"`
	TargetInventoryRoot     string                `json:"target_inventory_root"`
}

type installCASPayload struct {
	TransferID   string                `json:"transfer_id"`
	SourceAgent  string                `json:"source_agent"`
	TargetAgent  string                `json:"target_agent"`
	CipherSHA256 string                `json:"cipher_sha256"`
	CipherBytes  int64                 `json:"cipher_bytes"`
	Erasure      *erasureShardTransfer `json:"erasure,omitempty"`
}

// CASReconciler schedules encrypted object-level reconciliation inside an
// optional storage pool. Rendezvous hashing gives every digest a stable target
// set, while Merkle segment roots keep comparisons bounded to divergent
// prefixes. factor=0 disables object replication entirely.
type CASReconciler struct {
	Store  Store
	Config config.Config
	Emit   func(string)
}

func (r CASReconciler) Reconcile(ctx context.Context) (int, error) {
	scfg := r.Config.ControlPlane.Storage
	if !scfg.Enabled || !scfg.InventoryEnabled || scfg.ObjectReplicationFactor <= 0 {
		return 0, nil
	}
	rcfg := r.Config.ControlPlane.Replication
	timeout, _ := time.ParseDuration(rcfg.AgentTimeout)
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	now := time.Now().UTC()
	agents, err := r.Store.ListAgents(ctx)
	if err != nil {
		return 0, err
	}
	eligible := map[string]Agent{}
	inv := map[string]objectinventory.Inventory{}
	sourcePools := map[string][]string{}
	targetPools := map[string][]string{}
	for _, a := range agents {
		if !agentAllowed(a.ID, rcfg.AllowedAgents, rcfg.ExcludedAgents) || now.Sub(a.LastSeenAt) > timeout || strings.TrimSpace(a.ReplicationPublicKey) == "" || a.InventoryJSON == "" {
			continue
		}
		if !agentReadable(a, scfg) {
			continue
		}
		x, e := objectinventory.DecodeCompact(a.InventoryJSON)
		if e != nil {
			continue
		}
		eligible[a.ID] = a
		inv[a.ID] = x
		pool := agentPool(a, scfg.ObjectPoolLabel)
		sourcePools[pool] = append(sourcePools[pool], a.ID)
		if agentAcceptsPlacement(a, scfg) {
			targetPools[pool] = append(targetPools[pool], a.ID)
		}
	}
	jobs, _ := r.Store.ListJobs(ctx, 20000)
	known := map[string]Job{}
	for _, j := range jobs {
		if j.Kind == "replicate-cas" {
			known[j.Target] = j
		}
	}
	retryCooldown, _ := time.ParseDuration(scfg.InventoryInterval)
	if retryCooldown <= 0 {
		retryCooldown = 10 * time.Minute
	}
	queued := 0
	for pool, sources := range sourcePools {
		targets := append([]string(nil), targetPools[pool]...)
		sort.Strings(sources)
		sort.Strings(targets)
		factor := scfg.ObjectReplicationFactor
		if factor > len(targets) {
			factor = len(targets)
		}
		if factor < 1 {
			continue
		}
		for _, source := range sources {
			for _, target := range targets {
				if source == target {
					continue
				}
				ta := eligible[target]
				diff := objectinventory.DivergentPrefixes(inv[source], inv[target])
				if len(diff) == 0 {
					continue
				}
				if !agentHasCapacityFor(ta, scfg, divergentSourceBytes(inv[source], diff)) {
					continue
				}
				targetKey := fmt.Sprintf("cas:%s>%s:%s:%s", source, target, shortRoot(inv[source].MerkleRoot), shortRoot(inv[target].MerkleRoot))
				if prev, ok := known[targetKey]; ok {
					if prev.Status == JobQueued || prev.Status == JobRunning || prev.Status == JobSucceeded {
						continue
					}
					if prev.Status == JobFailed && now.Sub(prev.UpdatedAt) < retryCooldown {
						continue
					}
				}
				p := replicateCASPayload{TransferID: newID("casrepl"), SourceAgent: source, TargetAgent: target, TargetReplicationPubKey: ta.ReplicationPublicKey, PoolAgents: append([]string(nil), targets...), Factor: factor, Prefixes: diff, SourceInventoryRoot: inv[source].MerkleRoot, TargetInventoryRoot: inv[target].MerkleRoot}
				b, _ := json.Marshal(p)
				_, created, e := r.Store.Enqueue(ctx, Job{Kind: "replicate-cas", Target: targetKey, Payload: string(b), Affinity: source, Priority: 120, MaxAttempts: r.Config.ControlPlane.Workers.MaxAttempts, NotBefore: now})
				if e != nil {
					return queued, e
				}
				if created {
					queued++
					known[targetKey] = Job{Target: targetKey, Status: JobQueued, UpdatedAt: now}
					if r.Emit != nil {
						r.Emit(fmt.Sprintf("CAS reconciliation queued %s -> %s prefixes=%d", source, target, len(diff)))
					}
				}
			}
		}
	}
	return queued, nil
}

func agentPool(a Agent, label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return "default"
	}
	var m map[string]string
	if json.Unmarshal([]byte(a.LabelsJSON), &m) == nil {
		if v := strings.TrimSpace(m[label]); v != "" {
			return v
		}
	}
	return "default"
}
func shortRoot(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func divergentSourceBytes(inv objectinventory.Inventory, prefixes []string) int64 {
	want := map[string]bool{}
	for _, p := range prefixes {
		want[strings.ToLower(strings.TrimSpace(p))] = true
	}
	var n int64
	for _, seg := range inv.Segments {
		if want[strings.ToLower(seg.Prefix)] {
			n += seg.Bytes
		}
	}
	return n
}
