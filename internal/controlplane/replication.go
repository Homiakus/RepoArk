package controlplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/replication"
	"github.com/Homiakus/repoark/internal/storagehealth"
)

type replicateGenerationPayload struct {
	TransferID              string `json:"transfer_id"`
	RepositoryID            string `json:"repository_id"`
	Repository              string `json:"repository"`
	GenerationID            string `json:"generation_id"`
	SourceAgent             string `json:"source_agent"`
	TargetAgent             string `json:"target_agent"`
	TargetReplicationPubKey string `json:"target_replication_public_key"`
}

type installReplicaPayload struct {
	TransferID   string `json:"transfer_id"`
	RepositoryID string `json:"repository_id"`
	Repository   string `json:"repository"`
	GenerationID string `json:"generation_id"`
	SourceAgent  string `json:"source_agent"`
	TargetAgent  string `json:"target_agent"`
	CipherSHA256 string `json:"cipher_sha256,omitempty"`
	CipherBytes  int64  `json:"cipher_bytes,omitempty"`
}

type ReplicationReconciler struct {
	Store  Store
	Config config.Config
	Emit   func(string)
}

func (r ReplicationReconciler) Run(ctx context.Context) error {
	interval, _ := time.ParseDuration(r.Config.ControlPlane.Replication.ReconcileInterval)
	if interval <= 0 {
		interval = time.Minute
	}
	if _, err := r.Reconcile(ctx); err != nil && r.Emit != nil {
		r.Emit("initial replication reconcile: " + err.Error())
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if _, err := r.Reconcile(ctx); err != nil && r.Emit != nil {
				r.Emit("replication reconcile: " + err.Error())
			}
		}
	}
}

func (r ReplicationReconciler) Reconcile(ctx context.Context) (int, error) {
	cfg := r.Config.ControlPlane.Replication
	if !cfg.Enabled {
		return 0, nil
	}
	if _, err := CleanupExpiredReplicationTransfers(ctx, r.Store, cfg, time.Now().UTC()); err != nil {
		return 0, err
	}
	timeout, _ := time.ParseDuration(cfg.AgentTimeout)
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	now := time.Now().UTC()
	agents, err := r.Store.ListAgents(ctx)
	if err != nil {
		return 0, err
	}
	placement := map[string]Agent{}
	onlineSources := map[string]Agent{}
	targetEligible := map[string]Agent{}
	storageCfg := r.Config.ControlPlane.Storage
	for _, a := range agents {
		if !agentAllowed(a.ID, cfg.AllowedAgents, cfg.ExcludedAgents) {
			continue
		}
		placement[a.ID] = a
		if now.Sub(a.LastSeenAt) > timeout || strings.TrimSpace(a.ReplicationPublicKey) == "" {
			continue
		}
		if _, err := base64.RawStdEncoding.DecodeString(a.ReplicationPublicKey); err != nil {
			continue
		}
		onlineSources[a.ID] = a
		if agentAcceptsPlacement(a, storageCfg) {
			targetEligible[a.ID] = a
		}
	}
	if cfg.IncludeLocal {
		pub, err := replication.EnsureKey(cfg.LocalKeyPath)
		if err != nil {
			return 0, err
		}
		maxProbe, _ := time.ParseDuration(storageCfg.MaxProbe)
		hr := storagehealth.Probe(r.Config.ControlPlane.Generations.Root, storagehealth.Thresholds{MinFreeBytes: uint64(maxInt64(storageCfg.MinFreeBytes, 0)), MinFreePercent: storageCfg.MinFreePercent, MaxProbe: maxProbe})
		local := Agent{ID: LocalWorkerAffinity, Name: LocalWorkerAffinity, LabelsJSON: `{"__repoark_local_domain":"local"}`, ReplicationPublicKey: pub, LastSeenAt: now, Status: "online", StorageHealth: hr.Health, StorageTotalBytes: int64(hr.TotalBytes), StorageFreeBytes: int64(hr.FreeBytes), StorageFreePercent: hr.FreePercent, StorageProbeMS: hr.ProbeMS, StorageError: hr.Error}
		placement[LocalWorkerAffinity] = local
		onlineSources[LocalWorkerAffinity] = local
		if agentAcceptsPlacement(local, storageCfg) {
			targetEligible[LocalWorkerAffinity] = local
		}
	}
	gens, err := r.Store.ListAllGenerations(ctx, 20000)
	if err != nil {
		return 0, err
	}
	activePlacement := map[string]struct{}{}
	if jobs, e := r.Store.ListJobs(ctx, 10000); e == nil {
		for _, j := range jobs {
			if (j.Kind == "replicate-generation" || j.Kind == "install-replica") && (j.Status == JobQueued || j.Status == JobRunning) {
				activePlacement[j.Target] = struct{}{}
			}
		}
	}
	n := 0
	for _, g := range gens {
		replicas, err := r.Store.ListReplicas(ctx, g.ID)
		if err != nil {
			return n, err
		}
		ready := map[string]GenerationReplica{}
		healthyReady := map[string]GenerationReplica{}
		healthyDomains := map[string]struct{}{}
		for _, rp := range replicas {
			if rp.State != ReplicaReady {
				continue
			}
			a, ok := placement[rp.AgentID]
			if !ok {
				continue
			}
			if !storageCfg.EvacuateDegraded || agentCountsDurable(a, storageCfg) {
				ready[rp.AgentID] = rp
			}
			if a, ok := targetEligible[rp.AgentID]; ok {
				healthyReady[rp.AgentID] = rp
				if d := agentFailureDomain(a, cfg.FailureDomainLabel); d != "" {
					healthyDomains[d] = struct{}{}
				}
			}
		}
		domainsOK := true
		if strings.TrimSpace(cfg.FailureDomainLabel) != "" && cfg.MinFailureDomains > 0 {
			domainsOK = len(healthyDomains) >= cfg.MinFailureDomains
		}
		if len(ready) >= cfg.Factor && len(healthyReady) >= cfg.MinHealthy && domainsOK {
			continue
		}
		sources := make([]string, 0, len(replicas))
		for _, rp := range replicas {
			if rp.State == ReplicaReady {
				if _, ok := onlineSources[rp.AgentID]; ok {
					sources = append(sources, rp.AgentID)
				}
			}
		}
		if len(sources) == 0 {
			continue
		}
		sort.Slice(sources, func(i, j int) bool {
			ai, aj := onlineSources[sources[i]], onlineSources[sources[j]]
			if agentCountsDurable(ai, storageCfg) != agentCountsDurable(aj, storageCfg) {
				return agentCountsDurable(ai, storageCfg)
			}
			bi, bj := agentBandwidthMbps(ai), agentBandwidthMbps(aj)
			if bi != bj {
				return bi > bj
			}
			return sources[i] < sources[j]
		})
		var generationBytes int64
		for _, rp := range replicas {
			if rp.Bytes > generationBytes {
				generationBytes = rp.Bytes
			}
		}
		targets := make([]string, 0)
		for id, a := range targetEligible {
			if _, ok := ready[id]; !ok && agentHasCapacityFor(a, storageCfg, generationBytes) {
				targets = append(targets, id)
			}
		}
		sort.Slice(targets, func(i, j int) bool {
			di := agentFailureDomain(targetEligible[targets[i]], cfg.FailureDomainLabel)
			dj := agentFailureDomain(targetEligible[targets[j]], cfg.FailureDomainLabel)
			_, iSeen := healthyDomains[di]
			_, jSeen := healthyDomains[dj]
			if di != "" && !iSeen && (dj == "" || jSeen) {
				return true
			}
			if dj != "" && !jSeen && (di == "" || iSeen) {
				return false
			}
			ai, aj := targetEligible[targets[i]], targetEligible[targets[j]]
			bi, bj := agentBandwidthMbps(ai), agentBandwidthMbps(aj)
			if bi != bj {
				return bi > bj
			}
			if ai.StorageFreePercent != aj.StorageFreePercent {
				return ai.StorageFreePercent > aj.StorageFreePercent
			}
			if ai.StorageFreeBytes != aj.StorageFreeBytes {
				return ai.StorageFreeBytes > aj.StorageFreeBytes
			}
			return targets[i] < targets[j]
		})
		needCopies := cfg.Factor - len(ready)
		if needCopies < 0 {
			needCopies = 0
		}
		needHealthy := cfg.MinHealthy - len(healthyReady)
		if needHealthy < 0 {
			needHealthy = 0
		}
		needDomains := 0
		if strings.TrimSpace(cfg.FailureDomainLabel) != "" && cfg.MinFailureDomains > 0 {
			needDomains = cfg.MinFailureDomains - len(healthyDomains)
			if needDomains < 0 {
				needDomains = 0
			}
		}
		need := needCopies
		if needHealthy > need {
			need = needHealthy
		}
		if needDomains > need {
			need = needDomains
		}
		if need > len(targets) {
			need = len(targets)
		}
		for i := 0; i < need; i++ {
			source := sources[i%len(sources)]
			target := targets[i]
			jobTarget := fmt.Sprintf("%s@%s", g.ID, target)
			if _, busy := activePlacement[jobTarget]; busy {
				continue
			}
			transferID := newID("repl")
			payload := replicateGenerationPayload{TransferID: transferID, RepositoryID: g.RepositoryID, Repository: g.Repository, GenerationID: g.ID, SourceAgent: source, TargetAgent: target, TargetReplicationPubKey: targetEligible[target].ReplicationPublicKey}
			b, _ := json.Marshal(payload)
			_, created, err := r.Store.Enqueue(ctx, Job{Kind: "replicate-generation", Target: jobTarget, Payload: string(b), Affinity: source, Priority: 150, MaxAttempts: r.Config.ControlPlane.Workers.MaxAttempts, NotBefore: now})
			if err != nil {
				return n, err
			}
			if created {
				n++
				if r.Emit != nil {
					r.Emit(fmt.Sprintf("replication queued %s %s -> %s", g.Repository+"@"+g.ID, source, target))
				}
			}
		}
	}
	if r.Config.ControlPlane.Storage.ObjectReplicationFactor > 0 {
		cn, err := (CASReconciler{Store: r.Store, Config: r.Config, Emit: r.Emit}).Reconcile(ctx)
		if err != nil {
			return n, err
		}
		n += cn
	}
	if r.Config.ControlPlane.Storage.Erasure.Enabled && r.Config.ControlPlane.Storage.Erasure.Distributed {
		en, err := (ErasureReconciler{Store: r.Store, Config: r.Config, Emit: r.Emit}).Reconcile(ctx)
		if err != nil {
			return n, err
		}
		n += en
	}
	if r.Config.ControlPlane.Storage.Enabled {
		mn, err := (StorageMaintenanceReconciler{Store: r.Store, Config: r.Config, Emit: r.Emit}).Reconcile(ctx)
		if err != nil {
			return n, err
		}
		n += mn
	}
	return n, nil
}

func agentReadable(a Agent, cfg config.StorageDataConfig) bool {
	if !cfg.Enabled {
		return true
	}
	return a.StorageHealth != storagehealth.Unhealthy
}

func agentAcceptsPlacement(a Agent, cfg config.StorageDataConfig) bool {
	if !cfg.Enabled {
		return true
	}
	if a.StorageHealth == storagehealth.Unhealthy || a.StorageHealth == storagehealth.Degraded || strings.TrimSpace(a.StorageHealth) == "" {
		return false
	}
	if cfg.MinFreeBytes > 0 && a.StorageFreeBytes > 0 && a.StorageFreeBytes < cfg.MinFreeBytes {
		return false
	}
	if cfg.MinFreePercent > 0 && a.StorageTotalBytes > 0 && a.StorageFreePercent < cfg.MinFreePercent {
		return false
	}
	return true
}

func agentCountsDurable(a Agent, cfg config.StorageDataConfig) bool {
	if !cfg.Enabled {
		return true
	}
	if a.StorageHealth == storagehealth.Unhealthy || strings.TrimSpace(a.StorageHealth) == "" {
		return false
	}
	if cfg.EvacuateDegraded && a.StorageHealth == storagehealth.Degraded {
		return false
	}
	return true
}

func agentHasCapacityFor(a Agent, cfg config.StorageDataConfig, objectBytes int64) bool {
	if !agentAcceptsPlacement(a, cfg) {
		return false
	}
	if objectBytes <= 0 || a.StorageFreeBytes <= 0 {
		return true
	}
	reserve := cfg.MinFreeBytes
	if reserve < 0 {
		reserve = 0
	}
	return a.StorageFreeBytes-objectBytes >= reserve
}

func agentBandwidthMbps(a Agent) int {
	var labels map[string]any
	if json.Unmarshal([]byte(a.LabelsJSON), &labels) != nil {
		return 0
	}
	v, ok := labels["bandwidth_mbps"]
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		var n int
		_, _ = fmt.Sscanf(strings.TrimSpace(x), "%d", &n)
		return n
	}
	return 0
}

func maxInt64(v, min int64) int64 {
	if v < min {
		return min
	}
	return v
}

func agentFailureDomain(a Agent, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if a.ID == LocalWorkerAffinity {
		return "__local__"
	}
	var labels map[string]string
	if json.Unmarshal([]byte(a.LabelsJSON), &labels) != nil {
		return ""
	}
	return strings.TrimSpace(labels[key])
}

func agentAllowed(id string, allowed, excluded []string) bool {
	for _, x := range excluded {
		if strings.EqualFold(strings.TrimSpace(x), id) {
			return false
		}
	}
	if len(allowed) == 0 {
		return true
	}
	for _, x := range allowed {
		if strings.EqualFold(strings.TrimSpace(x), id) {
			return true
		}
	}
	return false
}

func SelectRestoreAffinity(ctx context.Context, store Store, generationID, preferredMetaPath string, cfg config.ReplicationConfig) (string, error) {
	return SelectRestoreAffinityWithStorage(ctx, store, generationID, preferredMetaPath, cfg, config.StorageDataConfig{})
}

// SelectRestoreAffinityWithStorage routes restores only to readable storage.
// Degraded storage remains readable (so it can be evacuated/recovered from),
// while unhealthy storage is never selected merely because its heartbeat is live.
func SelectRestoreAffinityWithStorage(ctx context.Context, store Store, generationID, preferredMetaPath string, cfg config.ReplicationConfig, storageCfg config.StorageDataConfig) (string, error) {
	preferred := AffinityFromMetaPath(preferredMetaPath)
	timeout, _ := time.ParseDuration(cfg.AgentTimeout)
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	now := time.Now().UTC()
	online := map[string]bool{LocalWorkerAffinity: true}
	agents, err := store.ListAgents(ctx)
	if err != nil {
		return "", err
	}
	for _, a := range agents {
		if now.Sub(a.LastSeenAt) <= timeout && agentReadable(a, storageCfg) {
			online[a.ID] = true
		}
	}
	reps, err := store.ListReplicas(ctx, generationID)
	if err != nil {
		return "", err
	}
	available := map[string]bool{}
	for _, rp := range reps {
		if rp.State == ReplicaReady && online[rp.AgentID] {
			available[rp.AgentID] = true
		}
	}
	if available[preferred] {
		return preferred, nil
	}
	var ids []string
	for id := range available {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > 0 {
		return ids[0], nil
	}
	return "", fmt.Errorf("generation %s has no online ready replica", generationID)
}

type ReplicationHealthReport struct {
	Enabled               bool `json:"enabled"`
	Generations           int  `json:"generations"`
	HealthyGenerations    int  `json:"healthy_generations"`
	Deficits              int  `json:"deficits"`
	ReadyReplicas         int  `json:"ready_replicas"`
	OnlineReadyReplicas   int  `json:"online_ready_replicas"`
	MinHealthy            int  `json:"min_healthy"`
	MinFailureDomains     int  `json:"min_failure_domains,omitempty"`
	FailureDomainDeficits int  `json:"failure_domain_deficits,omitempty"`
}

func ReplicationHealth(ctx context.Context, store Store, cfg config.ReplicationConfig) (ReplicationHealthReport, error) {
	return ReplicationHealthWithStorage(ctx, store, cfg, config.StorageDataConfig{})
}

func ReplicationHealthWithStorage(ctx context.Context, store Store, cfg config.ReplicationConfig, storageCfg config.StorageDataConfig) (ReplicationHealthReport, error) {
	minHealthy := cfg.MinHealthy
	if minHealthy < 1 {
		minHealthy = 1
	}
	report := ReplicationHealthReport{Enabled: cfg.Enabled, MinHealthy: minHealthy, MinFailureDomains: cfg.MinFailureDomains}
	timeout, _ := time.ParseDuration(cfg.AgentTimeout)
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	now := time.Now().UTC()
	online := map[string]bool{}
	onlineAgents := map[string]Agent{}
	if cfg.IncludeLocal {
		online[LocalWorkerAffinity] = true
		onlineAgents[LocalWorkerAffinity] = Agent{ID: LocalWorkerAffinity, LabelsJSON: `{"__repoark_local_domain":"local"}`}
	}
	agents, err := store.ListAgents(ctx)
	if err != nil {
		return report, err
	}
	for _, a := range agents {
		if !agentAllowed(a.ID, cfg.AllowedAgents, cfg.ExcludedAgents) {
			continue
		}
		if now.Sub(a.LastSeenAt) <= timeout && agentCountsDurable(a, storageCfg) {
			online[a.ID] = true
			onlineAgents[a.ID] = a
		}
	}
	gens, err := store.ListAllGenerations(ctx, 20000)
	if err != nil {
		return report, err
	}
	report.Generations = len(gens)
	for _, g := range gens {
		reps, err := store.ListReplicas(ctx, g.ID)
		if err != nil {
			return report, err
		}
		healthy := 0
		domains := map[string]struct{}{}
		for _, rp := range reps {
			if rp.State != ReplicaReady {
				continue
			}
			report.ReadyReplicas++
			if online[rp.AgentID] {
				healthy++
				report.OnlineReadyReplicas++
				if d := agentFailureDomain(onlineAgents[rp.AgentID], cfg.FailureDomainLabel); d != "" {
					domains[d] = struct{}{}
				}
			}
		}
		domainsOK := true
		if strings.TrimSpace(cfg.FailureDomainLabel) != "" && cfg.MinFailureDomains > 0 {
			domainsOK = len(domains) >= cfg.MinFailureDomains
			if !domainsOK {
				report.FailureDomainDeficits++
			}
		}
		if healthy >= minHealthy && domainsOK {
			report.HealthyGenerations++
		} else {
			report.Deficits++
		}
	}
	return report, nil
}

func replicationTransferExpiry(cfg config.ReplicationConfig, now time.Time) time.Time {
	ttl, _ := time.ParseDuration(cfg.TransferTTL)
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	return now.Add(ttl)
}

// CleanupExpiredReplicationTransfers reaps durable encrypted relay leases. It
// derives spool paths from transfer IDs instead of trusting a persisted path.
func CleanupExpiredReplicationTransfers(ctx context.Context, store Store, cfg config.ReplicationConfig, now time.Time) (int, error) {
	xs, err := store.ListExpiredReplicationTransfers(ctx, now, 1000)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range xs {
		_ = os.Remove(replicationSpoolPath(cfg.SpoolRoot, t.ID))
		_ = os.Remove(replicationSpoolPath(cfg.SpoolRoot, t.ID) + ".part")
		if err := store.DeleteReplicationTransfer(ctx, t.ID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
