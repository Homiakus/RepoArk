package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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
	"github.com/Homiakus/repoark/internal/replication"
	"github.com/Homiakus/repoark/internal/storagehealth"
)

type protectErasurePayload struct {
	Limit int `json:"limit,omitempty"`
}

type DistributedErasureHealth struct {
	Sets                  int `json:"sets"`
	Healthy               int `json:"healthy"`
	Degraded              int `json:"degraded"`
	Unrecoverable         int `json:"unrecoverable"`
	ShardCopies           int `json:"shard_copies"`
	FailureDomainDeficits int `json:"failure_domain_deficits"`
}

// protectErasure encodes large immutable CAS objects locally and registers the
// resulting shard identities in the durable control-plane state. Shards are
// themselves CAS objects. EnsureObjectRef makes retries idempotent.
func (r Runner) protectErasure(ctx context.Context, job Job) error {
	cfg := r.Config.ControlPlane.Storage.Erasure
	if !cfg.Enabled {
		return nil
	}
	var p protectErasurePayload
	_ = json.Unmarshal([]byte(job.Payload), &p)
	store := cas.New(r.Config.CAS.Root, 0)
	objects, err := store.ListObjects()
	if err != nil {
		return err
	}
	agentID := strings.TrimSpace(job.LeaseOwner)
	if agentID == "" {
		agentID = LocalWorkerAffinity
	}
	domain := localFailureDomain(r.Config, agentID, cfg.FailureDomainLabel)
	protected := 0
	for _, obj := range objects {
		if obj.Bytes < cfg.MinObjectBytes {
			continue
		}
		// Never recursively erasure-protect an erasure shard.
		if ref, ok, _ := r.Store.GetObjectRef(ctx, obj.Digest); ok && ref.Kind == "erasure-shard" {
			continue
		}
		dir := filepath.Join(r.Config.CAS.Root, "erasure", obj.Digest[:2], obj.Digest)
		m, readErr := erasure.ReadManifest(dir)
		if readErr != nil {
			m, err = erasure.EncodeFile(obj.Path, dir, erasure.Config{DataShards: cfg.DataShards, ParityShards: cfg.ParityShards, BlockBytes: cfg.BlockBytes})
			if err != nil {
				return fmt.Errorf("protect %s: %w", obj.Digest, err)
			}
		}
		if !strings.EqualFold(m.ObjectSHA256, obj.Digest) {
			return fmt.Errorf("erasure manifest/object mismatch for %s", obj.Digest)
		}
		set := ErasureSet{ObjectSHA256: obj.Digest, OriginalBytes: obj.Bytes, DataShards: m.DataShards, ParityShards: m.ParityShards, BlockBytes: m.BlockBytes, State: ErasureReady, CreatedAt: m.CreatedAt}
		if err := r.Store.RecordErasureSet(ctx, set); err != nil {
			return err
		}
		for _, sh := range m.Shards {
			path := filepath.Join(dir, filepath.Base(sh.Name))
			in, err := store.Ingest(path)
			if err != nil {
				return fmt.Errorf("ingest erasure shard %s/%d: %w", obj.Digest, sh.Index, err)
			}
			if !strings.EqualFold(in.SHA256, sh.SHA256) {
				return fmt.Errorf("erasure shard digest mismatch %s/%d", obj.Digest, sh.Index)
			}
			owner := fmt.Sprintf("erasure:%s:%d", obj.Digest, sh.Index)
			if _, err := r.Store.EnsureObjectRef(ctx, ObjectRef{Digest: sh.SHA256, Kind: "erasure-shard", Bytes: sh.Bytes}, owner); err != nil {
				return err
			}
			if err := r.Store.RecordErasureShard(ctx, ErasureShard{ObjectSHA256: obj.Digest, ShardIndex: sh.Index, ShardSHA256: sh.SHA256, AgentID: agentID, FailureDomain: domain, State: ShardReady, Bytes: sh.Bytes}); err != nil {
				return err
			}
		}
		protected++
		if p.Limit > 0 && protected >= p.Limit {
			break
		}
	}
	if r.Emit != nil && protected > 0 {
		r.Emit(fmt.Sprintf("erasure protected objects=%d on %s", protected, agentID))
	}
	return nil
}

func localFailureDomain(cfg config.Config, agentID, key string) string {
	if agentID == LocalWorkerAffinity {
		if strings.TrimSpace(key) == "" {
			return "__local__"
		}
		if v := strings.TrimSpace(cfg.ControlPlane.Agents.Labels[key]); v != "" {
			return v
		}
		return "__local__"
	}
	if strings.TrimSpace(key) == "" {
		return ""
	}
	return strings.TrimSpace(cfg.ControlPlane.Agents.Labels[key])
}

type ErasureReconciler struct {
	Store  Store
	Config config.Config
	Emit   func(string)
}

func (r ErasureReconciler) Reconcile(ctx context.Context) (int, error) {
	cfg := r.Config.ControlPlane.Storage.Erasure
	if !cfg.Enabled || !cfg.Distributed {
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
	readable := map[string]Agent{}
	for _, a := range agents {
		if !agentAllowed(a.ID, rcfg.AllowedAgents, rcfg.ExcludedAgents) || now.Sub(a.LastSeenAt) > timeout || strings.TrimSpace(a.ReplicationPublicKey) == "" {
			continue
		}
		if !agentInShardPool(a, cfg.ShardPoolLabel) {
			continue
		}
		if agentReadable(a, r.Config.ControlPlane.Storage) {
			readable[a.ID] = a
		}
		if agentAcceptsPlacement(a, r.Config.ControlPlane.Storage) {
			eligible[a.ID] = a
		}
	}
	if rcfg.IncludeLocal {
		// Local can participate only when storage itself is placement-healthy.
		if a, err := r.localAgent(now); err == nil {
			if agentReadable(a, r.Config.ControlPlane.Storage) {
				readable[a.ID] = a
			}
			if agentAcceptsPlacement(a, r.Config.ControlPlane.Storage) {
				eligible[a.ID] = a
			}
		}
	}
	queued := 0
	// Periodically ask every readable storage node to discover/protect new large
	// CAS objects. Job history provides durable cooldown without another timer table.
	interval, _ := time.ParseDuration(cfg.ReconcileInterval)
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	jobs, _ := r.Store.ListJobs(ctx, 50000)
	for id := range readable {
		if recentlyRanJob(jobs, "protect-erasure", id, interval, now) {
			continue
		}
		b, _ := json.Marshal(protectErasurePayload{})
		_, created, err := r.Store.Enqueue(ctx, Job{Kind: "protect-erasure", Target: id, Payload: string(b), Affinity: id, Priority: 90, MaxAttempts: r.Config.ControlPlane.Workers.MaxAttempts, NotBefore: now})
		if err != nil {
			return queued, err
		}
		if created {
			queued++
		}
	}
	sets, err := r.Store.ListErasureSets(ctx, 20000)
	if err != nil {
		return queued, err
	}
	for _, set := range sets {
		shards, err := r.Store.ListErasureShards(ctx, set.ObjectSHA256)
		if err != nil {
			return queued, err
		}
		byIndex := map[int][]ErasureShard{}
		for _, sh := range shards {
			if sh.State == ShardReady {
				byIndex[sh.ShardIndex] = append(byIndex[sh.ShardIndex], sh)
			}
		}
		total := set.DataShards + set.ParityShards
		for idx := 0; idx < total; idx++ {
			existing := byIndex[idx]
			if len(existing) == 0 {
				continue // no source yet; protect job will create it first
			}
			shardDigest := existing[0].ShardSHA256
			shardBytes := existing[0].Bytes
			desired := desiredShardAgents(set.ObjectSHA256, idx, cfg.ShardReplication, eligible, cfg.FailureDomainLabel)
			for _, target := range desired {
				if hasReadyShardOn(existing, target) {
					continue
				}
				source := selectShardSource(existing, readable, shardDigest)
				if source == "" || source == target {
					continue
				}
				ta := eligible[target]
				if !agentHasCapacityFor(ta, r.Config.ControlPlane.Storage, shardBytes) {
					continue
				}
				jobTarget := fmt.Sprintf("ers:%s:%03d@%s", set.ObjectSHA256, idx, target)
				if activeJobExists(jobs, "replicate-cas", jobTarget) {
					continue
				}
				transferID := newID("ersrepl")
				leaseID := "xfer:" + transferID
				ttl, _ := time.ParseDuration(rcfg.TransferTTL)
				if ttl <= 0 {
					ttl = 2 * time.Hour
				}
				if err := r.Store.AcquireObjectLease(ctx, ObjectLease{ID: leaseID, Digest: shardDigest, Owner: jobTarget, ExpiresAt: now.Add(ttl)}); err != nil {
					return queued, err
				}
				p := replicateCASPayload{TransferID: transferID, SourceAgent: source, TargetAgent: target, TargetReplicationPubKey: ta.ReplicationPublicKey, PoolAgents: []string{target}, Factor: 1, Digests: []string{shardDigest}, Erasure: &erasureShardTransfer{ObjectSHA256: set.ObjectSHA256, ShardIndex: idx, ShardSHA256: shardDigest, ShardBytes: shardBytes, LeaseID: leaseID}}
				b, _ := json.Marshal(p)
				_, created, err := r.Store.Enqueue(ctx, Job{Kind: "replicate-cas", Target: jobTarget, Payload: string(b), Affinity: source, Priority: 135, MaxAttempts: r.Config.ControlPlane.Workers.MaxAttempts, NotBefore: now})
				if err != nil {
					return queued, err
				}
				if created {
					queued++
				}
			}
		}
	}
	return queued, nil
}

func (r ErasureReconciler) localAgent(now time.Time) (Agent, error) {
	pub, err := replication.EnsureKey(r.Config.ControlPlane.Replication.LocalKeyPath)
	if err != nil {
		return Agent{}, err
	}
	maxProbe, _ := time.ParseDuration(r.Config.ControlPlane.Storage.MaxProbe)
	hr := storagehealth.Probe(r.Config.CAS.Root, storagehealth.Thresholds{MinFreeBytes: uint64(maxInt64(r.Config.ControlPlane.Storage.MinFreeBytes, 0)), MinFreePercent: r.Config.ControlPlane.Storage.MinFreePercent, MaxProbe: maxProbe})
	labels, _ := json.Marshal(r.Config.ControlPlane.Agents.Labels)
	return Agent{ID: LocalWorkerAffinity, Name: LocalWorkerAffinity, LabelsJSON: string(labels), ReplicationPublicKey: pub, Status: "online", StorageHealth: hr.Health, StorageTotalBytes: int64(hr.TotalBytes), StorageFreeBytes: int64(hr.FreeBytes), StorageFreePercent: hr.FreePercent, StorageProbeMS: hr.ProbeMS, StorageError: hr.Error, LastSeenAt: now}, nil
}

func recentlyRanJob(jobs []Job, kind, affinity string, interval time.Duration, now time.Time) bool {
	for _, j := range jobs {
		if j.Kind != kind || j.Affinity != affinity {
			continue
		}
		if (j.Status == JobQueued || j.Status == JobRunning) || (j.Status == JobSucceeded && now.Sub(j.UpdatedAt) < interval) {
			return true
		}
	}
	return false
}
func activeJobExists(jobs []Job, kind, target string) bool {
	for _, j := range jobs {
		if j.Kind == kind && j.Target == target && (j.Status == JobQueued || j.Status == JobRunning) {
			return true
		}
	}
	return false
}
func agentInShardPool(a Agent, label string) bool {
	label = strings.TrimSpace(label)
	if label == "" {
		return true
	}
	var m map[string]any
	if json.Unmarshal([]byte(a.LabelsJSON), &m) != nil {
		return false
	}
	v, ok := m[label]
	if !ok {
		return false
	}
	return strings.TrimSpace(fmt.Sprint(v)) != ""
}

func desiredShardAgents(object string, index, copies int, agents map[string]Agent, domainKey string) []string {
	if copies < 1 || len(agents) == 0 {
		return nil
	}
	byDomain := map[string][]string{}
	for id, a := range agents {
		d := agentFailureDomain(a, domainKey)
		if d == "" {
			d = "__agent__:" + id
		}
		byDomain[d] = append(byDomain[d], id)
	}
	domains := make([]string, 0, len(byDomain))
	for d := range byDomain {
		domains = append(domains, d)
		sort.Strings(byDomain[d])
	}
	sort.Strings(domains)
	if copies > len(agents) {
		copies = len(agents)
	}
	out := make([]string, 0, copies)
	used := map[string]bool{}
	for copyIndex := 0; len(out) < copies; copyIndex++ {
		d := domains[(index+copyIndex)%len(domains)]
		best, bestScore := "", uint64(0)
		for _, id := range byDomain[d] {
			if used[id] {
				continue
			}
			score := rendezvousScore(object, index, copyIndex, id)
			if best == "" || score > bestScore {
				best, bestScore = id, score
			}
		}
		if best == "" {
			// Domain exhausted; choose the best unused agent globally.
			for id := range agents {
				if used[id] {
					continue
				}
				score := rendezvousScore(object, index, copyIndex, id)
				if best == "" || score > bestScore {
					best, bestScore = id, score
				}
			}
		}
		if best == "" {
			break
		}
		used[best] = true
		out = append(out, best)
	}
	return out
}

func rendezvousScore(object string, index, copyIndex int, agent string) uint64 {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%s", object, index, copyIndex, agent)))
	return binary.BigEndian.Uint64(h[:8])
}
func hasReadyShardOn(xs []ErasureShard, agent string) bool {
	for _, sh := range xs {
		if sh.AgentID == agent && sh.State == ShardReady {
			return true
		}
	}
	return false
}
func selectShardSource(xs []ErasureShard, agents map[string]Agent, digest string) string {
	ids := make([]string, 0)
	for _, sh := range xs {
		if sh.State != ShardReady || !strings.EqualFold(sh.ShardSHA256, digest) {
			continue
		}
		if _, ok := agents[sh.AgentID]; ok {
			ids = append(ids, sh.AgentID)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func EvaluateDistributedErasureHealth(ctx context.Context, store Store, cfg config.Config, now time.Time) (DistributedErasureHealth, error) {
	var out DistributedErasureHealth
	ecfg := cfg.ControlPlane.Storage.Erasure
	if !ecfg.Enabled || !ecfg.Distributed {
		return out, nil
	}
	rcfg := cfg.ControlPlane.Replication
	timeout, _ := time.ParseDuration(rcfg.AgentTimeout)
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	agents, err := store.ListAgents(ctx)
	if err != nil {
		return out, err
	}
	online := map[string]Agent{}
	for _, a := range agents {
		if now.Sub(a.LastSeenAt) <= timeout && agentCountsDurable(a, cfg.ControlPlane.Storage) && agentInShardPool(a, ecfg.ShardPoolLabel) {
			online[a.ID] = a
		}
	}
	sets, err := store.ListErasureSets(ctx, 20000)
	if err != nil {
		return out, err
	}
	out.Sets = len(sets)
	for _, set := range sets {
		shards, err := store.ListErasureShards(ctx, set.ObjectSHA256)
		if err != nil {
			return out, err
		}
		indexDomains := map[int]map[string]struct{}{}
		for _, sh := range shards {
			if sh.State != ShardReady {
				continue
			}
			a, ok := online[sh.AgentID]
			if !ok {
				continue
			}
			d := agentFailureDomain(a, ecfg.FailureDomainLabel)
			if d == "" {
				d = "__agent__:" + sh.AgentID
			}
			if indexDomains[sh.ShardIndex] == nil {
				indexDomains[sh.ShardIndex] = map[string]struct{}{}
			}
			indexDomains[sh.ShardIndex][d] = struct{}{}
			out.ShardCopies++
		}
		if len(indexDomains) < set.DataShards {
			out.Unrecoverable++
			continue
		}
		domains := map[string]struct{}{}
		for _, ds := range indexDomains {
			for d := range ds {
				domains[d] = struct{}{}
			}
		}
		resilient := true
		for lost := range domains {
			remaining := 0
			for _, ds := range indexDomains {
				for d := range ds {
					if d != lost {
						remaining++
						break
					}
				}
			}
			if remaining < set.DataShards {
				resilient = false
				break
			}
		}
		if len(domains) < ecfg.MinFailureDomains || !resilient {
			out.Degraded++
			out.FailureDomainDeficits++
			continue
		}
		out.Healthy++
	}
	return out, nil
}

func erasureManifestDir(casRoot, digest string) string {
	return filepath.Join(casRoot, "erasure", digest[:2], digest)
}

func verifyCASDigest(root, digest string) error {
	path := cas.New(root, 0).BlobPath(digest)
	got, _, err := cas.HashFile(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, digest) {
		return fmt.Errorf("CAS digest mismatch: got %s want %s", got, digest)
	}
	return nil
}

func atomicReplaceFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.Rename(src, dst)
}
