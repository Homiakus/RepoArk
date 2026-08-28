# Changelog

## 0.8.0 — 2026-08-20

### Added
- Durable `object_refs`, unique logical owners, object leases, erasure sets and erasure shard-copy state in the control-plane store.
- SQL schema v5 and config schema v8.
- Exact-digest encrypted/resumable CAS transfer for distributed erasure shards.
- Failure-domain-aware distributed Reed-Solomon shard placement and recoverability health evaluation.
- SMART/NVMe JSON telemetry with explainable risk scoring for placement/evacuation.
- Bounded deterministic CAS scrub with SHA-256-gated erasure repair.
- Distributed corruption repair orchestration pinned to the storage node that reported corruption.
- Durable scrub/tiering maintenance jobs.
- Verified SSD/HDD/rclone copy-tiering without deleting authoritative hot CAS objects.
- Distributed erasure/object-ref/lease health metrics and API visibility.
- v0.7 → v0.8 SQLite migration coverage and new lifecycle/repair regression tests.

### Changed
- CAS GC treats positive durable refcounts and active leases as reachability roots.
- Erasure shard ownership is idempotent by `(digest, logical owner)`.
- Degraded/unhealthy storage cannot satisfy healthy distributed shard placement.
- `install-cas` records shard metadata before a job may complete successfully.
- Background storage maintenance uses the durable job scheduler rather than detached timers.

### Security / Reliability
- A distributed shard transfer is bound to an exact SHA-256 digest instead of a broad CAS prefix.
- Repair success requires the reconstructed object to match the original CAS digest.
- Failure-domain health verifies that loss of a represented domain still leaves at least `data_shards` unique shards.
- Object leases prevent GC from deleting data while a transfer/repair is in flight.


## 0.7.0 — 2026-08-20

### Added
- Resumable encrypted relay upload/download with exact offsets, chunk SHA-256, idempotent finalize and HTTP Range resume.
- Agent storage write/fsync/read health probe plus free/total-capacity heartbeat telemetry.
- Storage-aware placement, durable-quorum and restore-routing predicates with degraded-media evacuation semantics.
- Capacity/bandwidth/failure-domain-aware target preference.
- Compact 256-segment SHA-256 CAS Merkle inventories and control-plane inventory comparison API/CLI.
- Optional object-level CAS reconciliation across labelled pools using rendezvous hashing and the encrypted relay.
- Traversal-safe, digest-verified, atomic CAS object installation.
- Optional bounded-memory Reed-Solomon parity/reconstruction for large immutable CAS objects.
- OIDC Authorization Code + PKCE browser recovery with encrypted sessions, CSRF protection, group RBAC and optional `amr` step-up.
- Browser restore wizard constrained to the configured managed `restore_root`.
- Distributed-storage race/chaos workflow and expanded TUI/metrics storage visibility.

### Changed
- Config schema advances to 7; SQL schema marker advances to 4.
- A mixed-version agent with unknown storage health is readable but cannot receive or satisfy new durable placement.
- Generation and CAS encrypted relay jobs share the resumable transfer protocol.
- Object replication must be paired with enabled HA replication + mTLS agents; invalid partial configurations are rejected.
- OIDC session lifetime is capped by both eight hours and the verified ID-token expiry.

### Security / Reliability
- Degraded/unhealthy storage can no longer masquerade as a healthy new placement target.
- A fully received local relay is hash-verified before any retry network request.
- Browser recovery no longer accepts arbitrary filesystem destination paths.
- OIDC web auth refuses configurations without at least one RBAC group mapping.
- CAS object install verifies content against the SHA-256 object name and rejects unsafe archive entries.
- Erasure reconstruction verifies the final original object SHA-256 before promotion.

## 0.6.0 — 2026-08-20

### Added
- High-availability generation replication across mTLS storage agents.
- Destination-specific X25519 replication keys and chunked AES-256-GCM end-to-end encrypted relay payloads.
- Durable `replication_transfers` state with ciphertext SHA-256/size, source/target pinning, TTL and spool cleanup.
- Replication factor, minimum-online-copy health and optional failure-domain-aware placement using agent labels.
- Configurable mTLS agent labels (`zone`, `site`, `rack`, etc.) for topology policies.
- Safe data-aware restore failover/failback based only on online `ready` replicas.
- Destination CAS hydration after verified replica installation.
- Two-person restore request/approval/schedule workflow with expiration and optional requester/approver allowlists.
- Prometheus/TUI/API visibility for replicas, active transfers, approval state and failure-domain deficits.
- HA chaos CI workflow plus storage-loss/failover/failback, relay authorization, transfer expiry and malicious-archive regression tests.
- v0.5 -> v0.6 SQL migration coverage for new replica/transfer/approval schema.

### Changed
- Config schema is now version 6; v5 configuration continues to load with HA features disabled by default.
- Internal SQL schema marker advances to 3.
- Replica placement may create an additional copy when the raw replication factor is satisfied but configured failure-domain health is not.
- Agent heartbeat labels are configurable instead of being limited to a hard-coded worker role.
- Replicated generation content is compacted into destination CAS when CAS auto-compaction is enabled.

### Security
- Replication upload validates the target agent's current X25519 public key before accepting ciphertext.
- Encrypted relay download requires the destination certificate identity to own the exact running `install-replica` lease.
- Persisted transfer metadata must match destination, generation, ciphertext hash and byte count before download/install.
- Replication tar extraction rejects absolute paths, traversal entries and unsupported link/device entry types.
- Expired relay blobs are never served and are reaped from state/storage.
- Restore approval cannot be bypassed through direct generation restore while the gate is enabled.
- Approval execution is atomically coupled to successful restore-job completion in the state store.

## 0.5.0 — 2026-08-20

### Added
- SQLite control-plane state store using pure-Go modernc SQLite.
- PostgreSQL backend through pgx/v5.
- Portable SQL schema for jobs, repositories, generations and agents.
- Durable lease-based worker queue with abandoned-lease recovery and terminal final-attempt lease reaping.
- Concurrency-safe active-job deduplication keyed by `(kind,target,affinity)` and bounded exponential retries.
- Per-repository scheduler policies, priority and GitLab mirror policy.
- Single-repository backup execution path.
- Immutable point-in-time generations for bundles, LFS and empty bare repositories.
- Generation SHA-256 validation and detached Ed25519 metadata signatures.
- Generation-selective restore CLI plus control-plane routed `restore-generation` jobs for remote storage nodes.
- Automatic `mirror-gitlab` follow-up jobs with mTLS storage-node affinity.
- Read-only control-plane dashboard/API/Prometheus counters, including stranded jobs.
- mTLS/TLS 1.3 agent protocol with certificate-derived identity.
- Ed25519 bootstrap CA/server/agent certificate commands.
- Lease-bound authorization for remote generation/result/follow-up reporting.
- SQLite integration test and PostgreSQL service integration workflow.
- Inventory reconciliation disables removed/inaccessible repositories only after a complete successful listing.
- Filesystem generation retention is synchronized with the SQL generation index.

### Changed
- Config schema is now version 5; older config is loaded on top of safe v5 defaults.
- `repoark control serve` is the recommended durable scheduler; legacy `daemon` remains available.
- Charm TUI shows control-plane queue/agent state when enabled.

### Security
- Agent identity cannot be supplied by request JSON.
- Mutating remote-worker endpoints are available only on the mutual-TLS listener.
- Generation metadata trust is anchored outside the generation/backup root when signing is enabled.
- Agent-reported generation and backup state must match a job currently leased to the authenticated certificate identity.
- Agent-local path follow-ups cannot be leased by another certificate identity.
- The mTLS reporting routes are regression-tested through the actual HTTP ServeMux, not only direct handler calls.
