package controlplane

import "time"

type Job struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Target      string    `json:"target"`
	Payload     string    `json:"payload,omitempty"`
	Status      string    `json:"status"`
	Priority    int       `json:"priority"`
	Attempts    int       `json:"attempts"`
	MaxAttempts int       `json:"max_attempts"`
	NotBefore   time.Time `json:"not_before"`
	Affinity    string    `json:"affinity,omitempty"`
	LeaseOwner  string    `json:"lease_owner,omitempty"`
	LeaseUntil  time.Time `json:"lease_until,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	LastError   string    `json:"last_error,omitempty"`
}

type Repository struct {
	ID                   string    `json:"id"`
	Account              string    `json:"account"`
	FullName             string    `json:"full_name"`
	BackupRoot           string    `json:"backup_root"`
	IntervalSeconds      int64     `json:"interval_seconds"`
	Priority             int       `json:"priority"`
	MirrorGitLab         bool      `json:"mirror_gitlab"`
	Enabled              bool      `json:"enabled"`
	NextRunAt            time.Time `json:"next_run_at"`
	LastBackupAt         time.Time `json:"last_backup_at,omitempty"`
	LastGenerationID     string    `json:"last_generation_id,omitempty"`
	LastBackupSuccessful bool      `json:"last_backup_successful"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type Generation struct {
	ID           string    `json:"id"`
	RepositoryID string    `json:"repository_id"`
	Repository   string    `json:"repository"`
	MetaPath     string    `json:"meta_path"`
	CreatedAt    time.Time `json:"created_at"`
	Verified     bool      `json:"verified"`
	BundleSHA256 string    `json:"bundle_sha256,omitempty"`
	LFSSHA256    string    `json:"lfs_sha256,omitempty"`
}

type Agent struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	CertSubject          string    `json:"cert_subject"`
	LabelsJSON           string    `json:"labels_json,omitempty"`
	ReplicationPublicKey string    `json:"replication_public_key,omitempty"`
	Status               string    `json:"status"`
	StorageHealth        string    `json:"storage_health,omitempty"`
	StorageTotalBytes    int64     `json:"storage_total_bytes,omitempty"`
	StorageFreeBytes     int64     `json:"storage_free_bytes,omitempty"`
	StorageFreePercent   float64   `json:"storage_free_percent,omitempty"`
	StorageProbeMS       int64     `json:"storage_probe_ms,omitempty"`
	StorageError         string    `json:"storage_error,omitempty"`
	DiskRiskScore        int       `json:"disk_risk_score,omitempty"`
	DiskModel            string    `json:"disk_model,omitempty"`
	DiskSerial           string    `json:"disk_serial,omitempty"`
	DiskTemperatureC     float64   `json:"disk_temperature_c,omitempty"`
	DiskPercentageUsed   float64   `json:"disk_percentage_used,omitempty"`
	DiskMediaErrors      int64     `json:"disk_media_errors,omitempty"`
	DiskCriticalWarning  int64     `json:"disk_critical_warning,omitempty"`
	InventoryRoot        string    `json:"inventory_root,omitempty"`
	InventoryObjects     int       `json:"inventory_objects,omitempty"`
	InventoryBytes       int64     `json:"inventory_bytes,omitempty"`
	InventoryJSON        string    `json:"inventory_json,omitempty"`
	LastSeenAt           time.Time `json:"last_seen_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type GenerationReplica struct {
	GenerationID string    `json:"generation_id"`
	RepositoryID string    `json:"repository_id"`
	AgentID      string    `json:"agent_id"`
	MetaPath     string    `json:"meta_path"`
	State        string    `json:"state"`
	Bytes        int64     `json:"bytes"`
	SHA256       string    `json:"sha256,omitempty"`
	VerifiedAt   time.Time `json:"verified_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ReplicationTransfer struct {
	ID           string    `json:"id"`
	GenerationID string    `json:"generation_id"`
	RepositoryID string    `json:"repository_id"`
	SourceAgent  string    `json:"source_agent"`
	TargetAgent  string    `json:"target_agent"`
	SpoolPath    string    `json:"spool_path,omitempty"`
	State        string    `json:"state"`
	Bytes        int64     `json:"bytes"`
	SHA256       string    `json:"sha256,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ObjectRef struct {
	Digest    string    `json:"digest"`
	Kind      string    `json:"kind,omitempty"`
	Bytes     int64     `json:"bytes,omitempty"`
	RefCount  int64     `json:"ref_count"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ObjectLease struct {
	ID        string    `json:"id"`
	Digest    string    `json:"digest"`
	Owner     string    `json:"owner"`
	ExpiresAt time.Time `json:"expires_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ErasureSet struct {
	ObjectSHA256  string    `json:"object_sha256"`
	OriginalBytes int64     `json:"original_bytes"`
	DataShards    int       `json:"data_shards"`
	ParityShards  int       `json:"parity_shards"`
	BlockBytes    int       `json:"block_bytes"`
	State         string    `json:"state"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type ErasureShard struct {
	ObjectSHA256  string    `json:"object_sha256"`
	ShardIndex    int       `json:"shard_index"`
	ShardSHA256   string    `json:"shard_sha256"`
	AgentID       string    `json:"agent_id"`
	FailureDomain string    `json:"failure_domain,omitempty"`
	State         string    `json:"state"`
	Bytes         int64     `json:"bytes"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type RestoreApproval struct {
	ID           string    `json:"id"`
	Repository   string    `json:"repository"`
	GenerationID string    `json:"generation_id"`
	Target       string    `json:"target,omitempty"`
	RequestedBy  string    `json:"requested_by"`
	ApprovedBy   string    `json:"approved_by,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Stats struct {
	QueuedJobs             int `json:"queued_jobs"`
	RunningJobs            int `json:"running_jobs"`
	FailedJobs             int `json:"failed_jobs"`
	Repositories           int `json:"repositories"`
	DueRepositories        int `json:"due_repositories"`
	Generations            int `json:"generations"`
	ConnectedAgents        int `json:"connected_agents"`
	StrandedJobs           int `json:"stranded_jobs"`
	ReadyReplicas          int `json:"ready_replicas"`
	ReplicaDeficits        int `json:"replica_deficits"`
	PendingApprovals       int `json:"pending_approvals"`
	ActiveTransfers        int `json:"active_transfers"`
	DegradedStorageAgents  int `json:"degraded_storage_agents"`
	UnhealthyStorageAgents int `json:"unhealthy_storage_agents"`
	ObjectRefs             int `json:"object_refs"`
	ActiveObjectLeases     int `json:"active_object_leases"`
	ErasureSets            int `json:"erasure_sets"`
	ErasureShardCopies     int `json:"erasure_shard_copies"`
}

const (
	// LocalWorkerAffinity reserves path-bound work for the control-plane worker
	// group. Remote mTLS certificates may not use this identity.
	LocalWorkerAffinity = "__repoark_local__"

	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"

	ReplicaReady   = "ready"
	ReplicaStaging = "staging"
	ReplicaFailed  = "failed"

	TransferReceiving = "receiving"
	TransferReady     = "ready"
	TransferConsumed  = "consumed"

	ApprovalPending   = "pending"
	ApprovalApproved  = "approved"
	ApprovalScheduled = "scheduled"
	ApprovalExecuted  = "executed"
	ApprovalExpired   = "expired"

	ErasureReady    = "ready"
	ErasureDegraded = "degraded"
	ErasureFailed   = "failed"
	ShardReady      = "ready"
	ShardStaging    = "staging"
	ShardFailed     = "failed"
)
