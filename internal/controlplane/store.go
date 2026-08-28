package controlplane

import (
	"context"
	"time"
)

type Store interface {
	Close() error
	Migrate(context.Context) error
	Enqueue(context.Context, Job) (Job, bool, error)
	Lease(context.Context, string, int, time.Duration) ([]Job, error)
	Complete(context.Context, string, string) error
	Fail(context.Context, string, string, string, time.Duration) error
	ListJobs(context.Context, int) ([]Job, error)
	GetJob(context.Context, string) (Job, error)
	RetryJob(context.Context, string) error
	UpsertRepository(context.Context, Repository) error
	ListRepositories(context.Context) ([]Repository, error)
	DueRepositories(context.Context, time.Time, int) ([]Repository, error)
	MarkScheduled(context.Context, string, time.Time) error
	MarkBackupResult(context.Context, string, bool, string, time.Time) error
	RecordGeneration(context.Context, Generation) error
	ListAllGenerations(context.Context, int) ([]Generation, error)
	RecordReplica(context.Context, GenerationReplica) error
	ListReplicas(context.Context, string) ([]GenerationReplica, error)
	ListAllReplicas(context.Context, int) ([]GenerationReplica, error)
	RecordReplicationTransfer(context.Context, ReplicationTransfer) error
	GetReplicationTransfer(context.Context, string) (ReplicationTransfer, error)
	ListExpiredReplicationTransfers(context.Context, time.Time, int) ([]ReplicationTransfer, error)
	DeleteReplicationTransfer(context.Context, string) error
	PruneGenerationRecords(context.Context, string, int) error
	DisableMissingRepositories(context.Context, string, map[string]struct{}) error
	ListGenerations(context.Context, string, int) ([]Generation, error)
	HeartbeatAgent(context.Context, Agent) error
	ListAgents(context.Context) ([]Agent, error)
	GetAgent(context.Context, string) (Agent, error)
	CreateRestoreApproval(context.Context, RestoreApproval) error
	GetRestoreApproval(context.Context, string) (RestoreApproval, error)
	ListRestoreApprovals(context.Context, int) ([]RestoreApproval, error)
	ApproveRestore(context.Context, string, string, bool) error
	ScheduleRestore(context.Context, string) error
	ReleaseRestoreSchedule(context.Context, string) error
	MarkRestoreExecuted(context.Context, string) error
	Stats(context.Context, time.Time) (Stats, error)
	SetMeta(context.Context, string, string) error
	GetMeta(context.Context, string) (string, bool, error)
	AdjustObjectRef(context.Context, ObjectRef, int64) (ObjectRef, error)
	EnsureObjectRef(context.Context, ObjectRef, string) (bool, error)
	ReleaseObjectRef(context.Context, string, string) (bool, error)
	GetObjectRef(context.Context, string) (ObjectRef, bool, error)
	ListObjectRefs(context.Context, int) ([]ObjectRef, error)
	AcquireObjectLease(context.Context, ObjectLease) error
	ReleaseObjectLease(context.Context, string) error
	ListActiveObjectLeases(context.Context, time.Time, int) ([]ObjectLease, error)
	ProtectedObjectDigests(context.Context, time.Time) (map[string]struct{}, error)
	RecordErasureSet(context.Context, ErasureSet) error
	GetErasureSet(context.Context, string) (ErasureSet, bool, error)
	ListErasureSets(context.Context, int) ([]ErasureSet, error)
	RecordErasureShard(context.Context, ErasureShard) error
	DeleteErasureShard(context.Context, string, int, string) error
	ListErasureShards(context.Context, string) ([]ErasureShard, error)
}
