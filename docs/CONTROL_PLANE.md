# RepoArk v0.5 Control Plane

## Goals

The v0.5 control plane replaces best-effort periodic execution with durable orchestration while keeping all v0.4 direct commands usable.

Core invariants:

1. A job is either durable in SQL or completed; process memory is never the system of record.
2. A worker owns a job only until `lease_until`.
3. A dead worker cannot permanently strand a job: an expired running lease is reclaimable.
4. A repeated scheduler tick cannot create duplicate active `(kind,target,affinity)` work.
5. Repository backup generations are immutable recovery points, not aliases to mutable `latest` files.
6. Remote result reports require the same mTLS identity that owns the current job lease.
7. A path-bound follow-up is never executed on a different storage node merely to keep the queue moving.
8. An expired lease on the final permitted attempt becomes terminal instead of creating attempt `max+1`.

## State backends

### SQLite

Default for one control-plane process. RepoArk opens SQLite in WAL mode, enables foreign keys and a busy timeout, and keeps a single DB connection to avoid accidental write-contention patterns in the embedded mode.

### PostgreSQL

Use PostgreSQL when the database must live outside the RepoArk host or when the service topology needs database HA. RepoArk uses pgx through `database/sql` and `FOR UPDATE SKIP LOCKED` for concurrent job leasing.

Credentials are read from `control_plane.store.dsn_env`.

## Schema

The first control-plane schema contains:

- `repoark_meta` — schema/control metadata;
- `jobs` — durable queue, attempts, lease owner/deadline, retry state;
- `repositories` — discovered repos, schedule, priority and last recovery state;
- `generations` — immutable recovery-point index;
- `agents` — last-seen mTLS worker inventory.

IDs are generated application-side so SQL remains portable between SQLite and PostgreSQL.

## Scheduler

A repository has an independent interval and priority. The first matching `scheduler.policies` glob wins. `*` is a RepoArk catch-all policy.

The scheduler:

1. refreshes GitHub inventory on `discovery_interval`;
2. finds enabled repositories with `next_run_at <= now`;
3. enqueues one `backup-repo` job when no active job for the same target exists;
4. advances `next_run_at` immediately to prevent repeated ticks from re-enqueuing it.

## Workers and retries

A local worker or mTLS agent leases work for `workers.lease`. Leasing increments the attempt counter. On failure the job is requeued with exponential backoff until `max_attempts`; after that it becomes terminal `failed`. If a worker crashes on the last permitted attempt, the expired-lease reaper marks that row failed instead of issuing another lease. `repoark control retry JOB_ID` resets a terminal failed job explicitly.

## Backup job pipeline

`backup-repo`:

1. resolves exactly one GitHub repository;
2. updates its bare mirror and platform payloads;
3. creates/verifies the portable bundle and LFS archive;
4. creates an immutable generation;
5. records the generation in SQL;
6. records repository backup success;
7. optionally enqueues `mirror-gitlab`.

`mirror-gitlab` uses the same namespace-preserving GitLab logic as the v0.4 full migration path.

## Failure semantics

A GitHub/API/storage failure fails that repository job, not the entire scheduler. A process crash after lease acquisition leaves a `running` row, but once `lease_until` expires another worker may reclaim it.

Generation creation is completed before SQL marks that backup successful. Therefore SQL never intentionally points `last_generation_id` at a generation that was not fully written.


## Node affinity and stranded work

Normal repository backup jobs have no affinity and may be leased by any eligible worker. Once a local control-plane worker creates a path-bound follow-up, the job inherits the reserved `__repoark_local__` lease identity. A remote agent's `mirror-gitlab` follow-up contains mirror/LFS paths local to that agent, so the mTLS server overrides the child job affinity with the authenticated certificate identity. SQL leasing only returns an affinitized job to that identity.

`Stats.StrandedJobs` counts queued/running affinitized jobs whose owning agent has not heartbeated in the recent window. This is exported through `/api/v1/control/stats`, the dashboard/TUI and Prometheus (`repoark_control_jobs_stranded`). A stranded job is deliberately visible instead of being silently reassigned to a node that cannot see its storage.

## Orchestrated point-in-time restore

```bash
repoark control restore owner/repo 20260820T010203.123456789Z
repoark control restore owner/repo 20260820T010203.123456789Z --target /srv/recovery/case-123
```

The control plane finds the generation in SQL. A local/shared `meta_path` produces the reserved `__repoark_local__` affinity, so remote agents cannot consume a restore that depends on control-plane-local paths. An `agent://worker-a/...` path produces `affinity=worker-a`, so only that mTLS identity can lease the restore. Without `--target`, the worker uses its own `control_plane.generations.restore_root` and stages into `<restore_root>/<owner>/<repo>/<generation-id>`.

The normal observability/dashboard listener remains read-only. RepoArk intentionally does not expose a browser mutation endpoint for restore in v0.5; write operations stay behind the local CLI or the separate mTLS worker protocol.

## Inventory reconciliation

A successful complete GitHub discovery marks repositories that disappeared from that account as disabled while retaining their history/generation index. An incomplete listing that hits the configured page limit is an error, so reconciliation does not disable repositories from a truncated inventory.

Filesystem generation retention and SQL generation retention are pruned together. Removing an old immutable generation therefore cannot leave a stale SQL entry that appears restorable.


For multi-instance PostgreSQL control planes, `__repoark_local__` assumes the local worker group sees the same backup/generation filesystem. If control-plane instances use node-local disks, run them as explicit mTLS storage agents or provide shared/replicated storage; SQL HA alone does not make local files highly available.

## v0.6 HA data plane

The v0.6 control plane adds three durable resource types on top of the v0.5 queue:

- `generation_replicas` — verified physical generation placement;
- `replication_transfers` — leased encrypted relay blobs with source/target/hash/size/expiry;
- `restore_approvals` — explicit recovery authorization state.

Replication reconciliation runs independently of repository scheduling. It compares retained generations with configured placement policy, current heartbeats and ready replicas. A missing physical copy, online-quorum deficit or configured failure-domain deficit can enqueue a `replicate-generation` source job followed by a target-affined `install-replica` job.

Encrypted transfer state survives process restarts. The relay spool is bounded by `max_transfer_bytes`; stale transfers expire by `transfer_ttl` and are reaped. The destination never reports a replica `ready` until decryption, safe extraction and generation verification succeed.

### Data-aware restore routing

When replication is enabled, point-in-time recovery ignores worker availability unless the worker also has a `ready` replica record. The original owner is preferred while online; otherwise a deterministic online ready copy is selected. This changes v0.5's fail-safe `stranded` behavior into safe HA failover **only when data replication has actually completed**.

### Approval-gated recovery

If `restore_approval.enabled` is true, direct generation restore commands refuse to schedule recovery. Operators must request, approve and schedule through the durable state machine. Successful restore-job completion atomically changes `scheduled` to `executed`; approval alone never implies a completed restore.
