package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemoryStore is used by deterministic scheduler/worker tests and small embedded
// harnesses. Production control-plane commands use SQLStore.
type MemoryStore struct {
	mu              sync.Mutex
	Jobs            map[string]Job
	Repos           map[string]Repository
	Generations     map[string][]Generation
	Replicas        map[string][]GenerationReplica
	Agents          map[string]Agent
	Approvals       map[string]RestoreApproval
	Transfers       map[string]ReplicationTransfer
	ObjectRefs      map[string]ObjectRef
	ObjectRefOwners map[string]map[string]struct{}
	ObjectLeases    map[string]ObjectLease
	ErasureSets     map[string]ErasureSet
	ErasureShards   map[string][]ErasureShard
	Meta            map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{Jobs: map[string]Job{}, Repos: map[string]Repository{}, Generations: map[string][]Generation{}, Replicas: map[string][]GenerationReplica{}, Agents: map[string]Agent{}, Approvals: map[string]RestoreApproval{}, Transfers: map[string]ReplicationTransfer{}, ObjectRefs: map[string]ObjectRef{}, ObjectRefOwners: map[string]map[string]struct{}{}, ObjectLeases: map[string]ObjectLease{}, ErasureSets: map[string]ErasureSet{}, ErasureShards: map[string][]ErasureShard{}, Meta: map[string]string{}}
}
func (m *MemoryStore) Close() error                  { return nil }
func (m *MemoryStore) Migrate(context.Context) error { return nil }
func (m *MemoryStore) Enqueue(_ context.Context, j Job) (Job, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.Jobs {
		if x.Kind == j.Kind && x.Target == j.Target && x.Affinity == j.Affinity && (x.Status == JobQueued || x.Status == JobRunning) {
			return x, false, nil
		}
	}
	if j.ID == "" {
		j.ID = newID("job")
	}
	if j.Status == "" {
		j.Status = JobQueued
	}
	if j.MaxAttempts == 0 {
		j.MaxAttempts = 5
	}
	now := time.Now().UTC()
	if j.NotBefore.IsZero() {
		j.NotBefore = now
	}
	j.CreatedAt = now
	j.UpdatedAt = now
	m.Jobs[j.ID] = j
	return j, true, nil
}
func (m *MemoryStore) Lease(_ context.Context, owner string, limit int, lease time.Duration) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var xs []Job
	for id, j := range m.Jobs {
		if j.Status == JobRunning && !j.LeaseUntil.IsZero() && !j.LeaseUntil.After(now) && j.Attempts >= j.MaxAttempts {
			j.Status = JobFailed
			j.LeaseOwner = ""
			j.LeaseUntil = time.Time{}
			j.UpdatedAt = now
			if j.LastError == "" {
				j.LastError = "lease expired on final attempt"
			}
			m.Jobs[id] = j
			continue
		}
		if j.Attempts < j.MaxAttempts && !j.NotBefore.After(now) && (j.Affinity == "" || j.Affinity == owner) && (j.Status == JobQueued || (j.Status == JobRunning && !j.LeaseUntil.IsZero() && !j.LeaseUntil.After(now))) {
			xs = append(xs, j)
		}
	}
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].Priority == xs[j].Priority {
			return xs[i].CreatedAt.Before(xs[j].CreatedAt)
		}
		return xs[i].Priority > xs[j].Priority
	})
	if limit <= 0 || limit > len(xs) {
		limit = len(xs)
	}
	xs = xs[:limit]
	for i := range xs {
		j := xs[i]
		j.Status = JobRunning
		j.Attempts++
		j.LeaseOwner = owner
		j.LeaseUntil = now.Add(lease)
		j.UpdatedAt = now
		m.Jobs[j.ID] = j
		xs[i] = j
	}
	return xs, nil
}
func (m *MemoryStore) Complete(_ context.Context, id, owner string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.Jobs[id]
	if !ok || j.Status != JobRunning || j.LeaseOwner != owner {
		return fmt.Errorf("job not leased")
	}
	if j.Kind == "restore-generation" {
		var p restoreGenerationPayload
		if json.Unmarshal([]byte(j.Payload), &p) == nil && p.ApprovalID != "" {
			a, ok := m.Approvals[p.ApprovalID]
			if !ok || a.Status != ApprovalScheduled {
				return fmt.Errorf("restore approval is not scheduled")
			}
			a.Status = ApprovalExecuted
			a.UpdatedAt = time.Now().UTC()
			m.Approvals[p.ApprovalID] = a
		}
	}
	j.Status = JobSucceeded
	j.LeaseOwner = ""
	j.LeaseUntil = time.Time{}
	j.UpdatedAt = time.Now().UTC()
	m.Jobs[id] = j
	return nil
}
func (m *MemoryStore) Fail(_ context.Context, id, owner, detail string, retry time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.Jobs[id]
	if !ok || j.LeaseOwner != owner {
		return fmt.Errorf("job not leased")
	}
	j.LastError = detail
	j.LeaseOwner = ""
	j.LeaseUntil = time.Time{}
	j.UpdatedAt = time.Now().UTC()
	if j.Attempts >= j.MaxAttempts {
		j.Status = JobFailed
	} else {
		j.Status = JobQueued
		j.NotBefore = j.UpdatedAt.Add(retry)
	}
	m.Jobs[id] = j
	return nil
}
func (m *MemoryStore) ListJobs(_ context.Context, limit int) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Job
	for _, j := range m.Jobs {
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (m *MemoryStore) GetJob(_ context.Context, id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.Jobs[id]
	if !ok {
		return Job{}, fmt.Errorf("job not found")
	}
	return j, nil
}
func (m *MemoryStore) RetryJob(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.Jobs[id]
	if !ok || j.Status != JobFailed {
		return fmt.Errorf("failed job not found")
	}
	j.Status = JobQueued
	j.Attempts = 0
	j.LastError = ""
	j.NotBefore = time.Now().UTC()
	m.Jobs[id] = j
	return nil
}
func (m *MemoryStore) UpsertRepository(_ context.Context, r Repository) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.ID == "" {
		r.ID = RepositoryID(r.Account, r.FullName)
	}
	if old, ok := m.Repos[r.ID]; ok {
		r.LastBackupAt = old.LastBackupAt
		r.LastGenerationID = old.LastGenerationID
		r.LastBackupSuccessful = old.LastBackupSuccessful
		if r.NextRunAt.IsZero() {
			r.NextRunAt = old.NextRunAt
		}
	}
	if r.NextRunAt.IsZero() {
		r.NextRunAt = time.Now().UTC()
	}
	r.UpdatedAt = time.Now().UTC()
	m.Repos[r.ID] = r
	return nil
}
func (m *MemoryStore) ListRepositories(context.Context) ([]Repository, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Repository
	for _, r := range m.Repos {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FullName < out[j].FullName })
	return out, nil
}
func (m *MemoryStore) DueRepositories(_ context.Context, now time.Time, limit int) ([]Repository, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Repository
	for _, r := range m.Repos {
		if r.Enabled && !r.NextRunAt.After(now) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NextRunAt.Before(out[j].NextRunAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (m *MemoryStore) MarkScheduled(_ context.Context, id string, next time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.Repos[id]
	r.NextRunAt = next
	r.UpdatedAt = time.Now().UTC()
	m.Repos[id] = r
	return nil
}
func (m *MemoryStore) MarkBackupResult(_ context.Context, id string, ok bool, gid string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.Repos[id]
	r.LastBackupAt = at
	r.LastBackupSuccessful = ok
	r.LastGenerationID = gid
	r.UpdatedAt = time.Now().UTC()
	m.Repos[id] = r
	return nil
}
func (m *MemoryStore) RecordGeneration(_ context.Context, g Generation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Generations[g.RepositoryID] = append(m.Generations[g.RepositoryID], g)
	return nil
}
func (m *MemoryStore) ListAllGenerations(_ context.Context, limit int) ([]Generation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Generation
	for _, xs := range m.Generations {
		out = append(out, xs...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (m *MemoryStore) RecordReplica(_ context.Context, r GenerationReplica) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r.UpdatedAt = time.Now().UTC()
	xs := m.Replicas[r.GenerationID]
	for i := range xs {
		if xs[i].AgentID == r.AgentID {
			xs[i] = r
			m.Replicas[r.GenerationID] = xs
			return nil
		}
	}
	m.Replicas[r.GenerationID] = append(xs, r)
	return nil
}
func (m *MemoryStore) ListReplicas(_ context.Context, generationID string) ([]GenerationReplica, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]GenerationReplica(nil), m.Replicas[generationID]...), nil
}
func (m *MemoryStore) RecordReplicationTransfer(_ context.Context, t ReplicationTransfer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.ID == "" {
		return fmt.Errorf("replication transfer id is required")
	}
	if t.State == "" {
		t.State = TransferReady
	}
	t.UpdatedAt = time.Now().UTC()
	m.Transfers[t.ID] = t
	return nil
}
func (m *MemoryStore) GetReplicationTransfer(_ context.Context, id string) (ReplicationTransfer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.Transfers[id]
	if !ok {
		return ReplicationTransfer{}, fmt.Errorf("replication transfer not found")
	}
	return t, nil
}
func (m *MemoryStore) ListExpiredReplicationTransfers(_ context.Context, now time.Time, limit int) ([]ReplicationTransfer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ReplicationTransfer
	for _, t := range m.Transfers {
		if !t.ExpiresAt.IsZero() && !t.ExpiresAt.After(now) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExpiresAt.Before(out[j].ExpiresAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (m *MemoryStore) DeleteReplicationTransfer(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Transfers, id)
	return nil
}
func (m *MemoryStore) ListAllReplicas(_ context.Context, limit int) ([]GenerationReplica, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []GenerationReplica
	for _, xs := range m.Replicas {
		out = append(out, xs...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (m *MemoryStore) ListGenerations(_ context.Context, rid string, limit int) ([]Generation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]Generation(nil), m.Generations[rid]...)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (m *MemoryStore) PruneGenerationRecords(_ context.Context, repoID string, keep int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	xs := m.Generations[repoID]
	sort.Slice(xs, func(i, j int) bool { return xs[i].CreatedAt.After(xs[j].CreatedAt) })
	if keep > 0 && len(xs) > keep {
		removed := append([]Generation(nil), xs[keep:]...)
		xs = xs[:keep]
		for _, g := range removed {
			delete(m.Replicas, g.ID)
		}
	}
	m.Generations[repoID] = xs
	return nil
}
func (m *MemoryStore) DisableMissingRepositories(_ context.Context, account string, seen map[string]struct{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.Repos {
		if r.Account != account {
			continue
		}
		if _, ok := seen[id]; !ok {
			r.Enabled = false
			r.UpdatedAt = time.Now().UTC()
			m.Repos[id] = r
		}
	}
	return nil
}
func (m *MemoryStore) HeartbeatAgent(_ context.Context, a Agent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a.ID == "" {
		a.ID = a.Name
	}
	a.LastSeenAt = time.Now().UTC()
	a.UpdatedAt = a.LastSeenAt
	a.Status = "online"
	m.Agents[a.ID] = a
	return nil
}
func (m *MemoryStore) ListAgents(context.Context) ([]Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Agent
	for _, a := range m.Agents {
		out = append(out, a)
	}
	return out, nil
}
func (m *MemoryStore) GetAgent(_ context.Context, id string) (Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.Agents[id]
	if !ok {
		return Agent{}, fmt.Errorf("agent not found")
	}
	return a, nil
}
func (m *MemoryStore) CreateRestoreApproval(_ context.Context, a RestoreApproval) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a.ID == "" {
		a.ID = newID("restore")
	}
	now := time.Now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	if a.Status == "" {
		a.Status = ApprovalPending
	}
	m.Approvals[a.ID] = a
	return nil
}
func (m *MemoryStore) GetRestoreApproval(_ context.Context, id string) (RestoreApproval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.Approvals[id]
	if !ok {
		return RestoreApproval{}, fmt.Errorf("restore approval not found")
	}
	return a, nil
}
func (m *MemoryStore) ListRestoreApprovals(_ context.Context, limit int) ([]RestoreApproval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RestoreApproval
	for _, a := range m.Approvals {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (m *MemoryStore) ApproveRestore(_ context.Context, id, actor string, requireDistinct bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.Approvals[id]
	if !ok {
		return fmt.Errorf("restore approval not found")
	}
	now := time.Now().UTC()
	if a.Status != ApprovalPending {
		return fmt.Errorf("restore approval is %s", a.Status)
	}
	if !a.ExpiresAt.IsZero() && !a.ExpiresAt.After(now) {
		a.Status = ApprovalExpired
		a.UpdatedAt = now
		m.Approvals[id] = a
		return fmt.Errorf("restore approval expired")
	}
	if requireDistinct && a.RequestedBy == actor {
		return fmt.Errorf("requester cannot approve own restore")
	}
	a.Status = ApprovalApproved
	a.ApprovedBy = actor
	a.UpdatedAt = now
	m.Approvals[id] = a
	return nil
}
func (m *MemoryStore) ScheduleRestore(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.Approvals[id]
	if !ok {
		return fmt.Errorf("restore approval not found")
	}
	if a.Status != ApprovalApproved {
		return fmt.Errorf("restore approval is %s", a.Status)
	}
	a.Status = ApprovalScheduled
	a.UpdatedAt = time.Now().UTC()
	m.Approvals[id] = a
	return nil
}
func (m *MemoryStore) ReleaseRestoreSchedule(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.Approvals[id]
	if !ok {
		return fmt.Errorf("restore approval not found")
	}
	if a.Status != ApprovalScheduled {
		return fmt.Errorf("restore approval is %s", a.Status)
	}
	a.Status = ApprovalApproved
	a.UpdatedAt = time.Now().UTC()
	m.Approvals[id] = a
	return nil
}
func (m *MemoryStore) MarkRestoreExecuted(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.Approvals[id]
	if !ok {
		return fmt.Errorf("restore approval not found")
	}
	if a.Status != ApprovalScheduled {
		return fmt.Errorf("restore approval is %s", a.Status)
	}
	a.Status = ApprovalExecuted
	a.UpdatedAt = time.Now().UTC()
	m.Approvals[id] = a
	return nil
}
func (m *MemoryStore) Stats(_ context.Context, now time.Time) (Stats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var s Stats
	s.Repositories = len(m.Repos)
	for _, j := range m.Jobs {
		switch j.Status {
		case JobQueued:
			s.QueuedJobs++
		case JobRunning:
			s.RunningJobs++
		case JobFailed:
			s.FailedJobs++
		}
	}
	for _, r := range m.Repos {
		if r.Enabled && !r.NextRunAt.After(now) {
			s.DueRepositories++
		}
	}
	for _, g := range m.Generations {
		s.Generations += len(g)
	}
	connected := map[string]struct{}{}
	for _, a := range m.Agents {
		if a.LastSeenAt.After(now.Add(-2 * time.Minute)) {
			s.ConnectedAgents++
			connected[a.ID] = struct{}{}
		}
		switch a.StorageHealth {
		case "degraded":
			s.DegradedStorageAgents++
		case "unhealthy":
			s.UnhealthyStorageAgents++
		}
	}
	for _, j := range m.Jobs {
		if j.Affinity == "" || j.Affinity == LocalWorkerAffinity || (j.Status != JobQueued && j.Status != JobRunning) {
			continue
		}
		if _, ok := connected[j.Affinity]; !ok {
			s.StrandedJobs++
		}
	}
	for _, xs := range m.Replicas {
		for _, r := range xs {
			if r.State == ReplicaReady {
				s.ReadyReplicas++
			}
		}
	}
	s.ActiveTransfers = len(m.Transfers)
	for _, a := range m.Approvals {
		if a.Status == ApprovalPending && (a.ExpiresAt.IsZero() || a.ExpiresAt.After(now)) {
			s.PendingApprovals++
		}
	}
	s.ObjectRefs = len(m.ObjectRefs)
	for _, l := range m.ObjectLeases {
		if l.ExpiresAt.After(now) {
			s.ActiveObjectLeases++
		}
	}
	s.ErasureSets = len(m.ErasureSets)
	for _, xs := range m.ErasureShards {
		for _, sh := range xs {
			if sh.State == ShardReady {
				s.ErasureShardCopies++
			}
		}
	}
	return s, nil
}
func (m *MemoryStore) SetMeta(_ context.Context, k, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Meta[k] = v
	return nil
}
func (m *MemoryStore) GetMeta(_ context.Context, k string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.Meta[k]
	return v, ok, nil
}

func (m *MemoryStore) AdjustObjectRef(_ context.Context, ref ObjectRef, delta int64) (ObjectRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ref.Digest == "" {
		return ObjectRef{}, fmt.Errorf("object digest is required")
	}
	cur := m.ObjectRefs[ref.Digest]
	if cur.Digest == "" {
		cur = ref
	}
	if ref.Kind != "" {
		cur.Kind = ref.Kind
	}
	if ref.Bytes > 0 {
		cur.Bytes = ref.Bytes
	}
	cur.RefCount += delta
	if cur.RefCount < 0 {
		return ObjectRef{}, fmt.Errorf("negative refcount for %s", ref.Digest)
	}
	cur.UpdatedAt = time.Now().UTC()
	m.ObjectRefs[ref.Digest] = cur
	return cur, nil
}

func (m *MemoryStore) EnsureObjectRef(_ context.Context, ref ObjectRef, owner string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ref.Digest == "" || owner == "" {
		return false, fmt.Errorf("digest and owner are required")
	}
	owners := m.ObjectRefOwners[ref.Digest]
	if owners == nil {
		owners = map[string]struct{}{}
		m.ObjectRefOwners[ref.Digest] = owners
	}
	if _, ok := owners[owner]; ok {
		return false, nil
	}
	cur := m.ObjectRefs[ref.Digest]
	if cur.Digest == "" {
		cur = ref
	}
	if ref.Kind != "" {
		cur.Kind = ref.Kind
	}
	if ref.Bytes > 0 {
		cur.Bytes = ref.Bytes
	}
	cur.RefCount++
	cur.UpdatedAt = time.Now().UTC()
	m.ObjectRefs[ref.Digest] = cur
	owners[owner] = struct{}{}
	return true, nil
}
func (m *MemoryStore) ReleaseObjectRef(_ context.Context, digest, owner string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owners := m.ObjectRefOwners[digest]
	if _, ok := owners[owner]; !ok {
		return false, nil
	}
	delete(owners, owner)
	cur := m.ObjectRefs[digest]
	if cur.RefCount > 0 {
		cur.RefCount--
	}
	cur.UpdatedAt = time.Now().UTC()
	m.ObjectRefs[digest] = cur
	return true, nil
}

func (m *MemoryStore) GetObjectRef(_ context.Context, digest string) (ObjectRef, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.ObjectRefs[digest]
	return r, ok, nil
}

func (m *MemoryStore) ListObjectRefs(_ context.Context, limit int) ([]ObjectRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ObjectRef, 0, len(m.ObjectRefs))
	for _, r := range m.ObjectRefs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Digest < out[j].Digest })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) AcquireObjectLease(_ context.Context, l ObjectLease) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l.ID == "" {
		l.ID = newID("olease")
	}
	if l.Digest == "" || l.Owner == "" || l.ExpiresAt.IsZero() {
		return fmt.Errorf("invalid object lease")
	}
	l.UpdatedAt = time.Now().UTC()
	m.ObjectLeases[l.ID] = l
	return nil
}

func (m *MemoryStore) ReleaseObjectLease(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.ObjectLeases, id)
	return nil
}

func (m *MemoryStore) ListActiveObjectLeases(_ context.Context, now time.Time, limit int) ([]ObjectLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ObjectLease
	for _, l := range m.ObjectLeases {
		if l.ExpiresAt.After(now) {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExpiresAt.Before(out[j].ExpiresAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) ProtectedObjectDigests(_ context.Context, now time.Time) (map[string]struct{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]struct{}{}
	for d, r := range m.ObjectRefs {
		if r.RefCount > 0 {
			out[d] = struct{}{}
		}
	}
	for _, l := range m.ObjectLeases {
		if l.ExpiresAt.After(now) {
			out[l.Digest] = struct{}{}
		}
	}
	return out, nil
}

func (m *MemoryStore) RecordErasureSet(_ context.Context, e ErasureSet) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.ObjectSHA256 == "" {
		return fmt.Errorf("erasure object digest is required")
	}
	now := time.Now().UTC()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	e.UpdatedAt = now
	m.ErasureSets[e.ObjectSHA256] = e
	return nil
}

func (m *MemoryStore) GetErasureSet(_ context.Context, digest string) (ErasureSet, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.ErasureSets[digest]
	return e, ok, nil
}

func (m *MemoryStore) ListErasureSets(_ context.Context, limit int) ([]ErasureSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ErasureSet, 0, len(m.ErasureSets))
	for _, e := range m.ErasureSets {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ObjectSHA256 < out[j].ObjectSHA256 })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) RecordErasureShard(_ context.Context, sh ErasureShard) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sh.ObjectSHA256 == "" || sh.ShardSHA256 == "" || sh.AgentID == "" {
		return fmt.Errorf("invalid erasure shard")
	}
	sh.UpdatedAt = time.Now().UTC()
	xs := m.ErasureShards[sh.ObjectSHA256]
	for i := range xs {
		if xs[i].ShardIndex == sh.ShardIndex && xs[i].AgentID == sh.AgentID {
			xs[i] = sh
			m.ErasureShards[sh.ObjectSHA256] = xs
			return nil
		}
	}
	m.ErasureShards[sh.ObjectSHA256] = append(xs, sh)
	return nil
}

func (m *MemoryStore) DeleteErasureShard(_ context.Context, digest string, index int, agent string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	xs := m.ErasureShards[digest]
	out := xs[:0]
	for _, sh := range xs {
		if sh.ShardIndex == index && sh.AgentID == agent {
			continue
		}
		out = append(out, sh)
	}
	m.ErasureShards[digest] = out
	return nil
}

func (m *MemoryStore) ListErasureShards(_ context.Context, digest string) ([]ErasureShard, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ErasureShard(nil), m.ErasureShards[digest]...), nil
}
