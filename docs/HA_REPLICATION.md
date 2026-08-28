# RepoArk v0.6 HA Replication

## Purpose

RepoArk v0.6 separates **control-plane availability** from **data-plane availability**. A healthy SQL database or live worker is not sufficient to recover a repository: a restore is routable only to a storage node that reports a verified `ready` copy of the requested immutable generation.

## Data path

```text
verified generation on source agent
        |
        | ArchiveGeneration
        v
     tar.gz stream
        |
        | X25519(source ephemeral -> destination static public key)
        | HKDF-SHA256 -> AES-256 key
        | chunked AES-256-GCM
        v
 opaque encrypted relay blob
        |
        | mTLS upload
        v
 control-plane replication spool
 + durable ReplicationTransfer lease
        |
        | mTLS download only by target lease owner
        v
 destination agent
        |
        | AES-GCM decrypt
        | traversal-safe tar extraction
        | generation signature/checksum verification
        | atomic install
        | optional local CAS compaction
        v
 verified ready replica
```

The control plane never receives the target X25519 private key. mTLS protects transport identity and relay confidentiality in transit; X25519 + AES-GCM provides an independent end-to-end content-encryption layer for the relayed backup payload.

## Keys

Each remote agent uses a distinct replication key:

```yaml
control_plane:
  agents:
    replication_key_path: ~/.config/repoark/pki/replication-x25519.key
```

The private key is stored with mode `0600`. The raw-base64 public key is included in the mTLS-authenticated heartbeat. The optional control-plane-local storage node has its own key at `replication.local_key_path`.

Replicating a generation pins the target public key into the source job. The upload endpoint compares that pinned key with the target agent's current heartbeat state before accepting a transfer, preventing a stale or substituted target key from silently receiving a new encrypted copy.

## Durable transfer lease

Each accepted relay blob has a durable state-store record:

- transfer ID;
- generation/repository ID;
- source and target agent;
- ciphertext byte count;
- ciphertext SHA-256;
- expiry timestamp;
- state/update timestamp.

A destination download must match the leased `install-replica` job, target certificate identity and persisted transfer metadata. Expired transfers are not served. The reconciler deletes expired spool blobs and their SQL records; successful installation deletes them immediately.

`transfer_ttl` therefore limits both stale state and leaked spool storage after crashes.

## Placement policy

```yaml
control_plane:
  replication:
    enabled: true
    factor: 3
    min_healthy: 2
    agent_timeout: 2m
    transfer_ttl: 2h
    failure_domain_label: zone
    min_failure_domains: 2
```

Definitions:

- `factor`: desired number of durable `ready` copies.
- `min_healthy`: minimum online `ready` copies needed for a healthy generation.
- `failure_domain_label`: optional agent label used for topology separation.
- `min_failure_domains`: minimum distinct online label values.
- `allowed_agents` / `excluded_agents`: placement scope.

The reconciler prefers targets in failure domains not already represented by healthy copies. If `factor` is already met but healthy copies collapsed into one zone, it may create an additional copy rather than treating the generation as healthy.

## Agent labels

```yaml
control_plane:
  agents:
    labels:
      role: backup-worker
      site: dc-1
      zone: rack-a
      storage: nvme
```

Labels are reported over the authenticated mTLS connection and stored with the certificate-derived agent identity. `zone`, `site` and `rack` have no built-in semantics; the operator chooses one key as `failure_domain_label`.

## CAS behavior

RepoArk intentionally does **not** copy the entire source CAS namespace. Only files reachable from the retained generation are archived and transferred. Once the destination has verified and atomically installed the generation, `cas.auto_compact` can ingest the immutable files into the destination CAS.

This gives generation-level replication plus destination CAS deduplication without propagating source-side orphaned blobs.

## Restore failover and failback

Restore routing evaluates `generation_replicas` plus agent heartbeat age:

1. use the generation's preferred/original owner if that owner is online and its replica is `ready`;
2. otherwise choose a deterministic online `ready` replica;
3. otherwise reject the restore.

When the original node returns, it becomes preferred again. This is **routing failback**; RepoArk does not delete redundant replacement copies as part of failback.

A live agent without the generation is never selected merely because it can execute code.

## Monitoring

Prometheus exposes:

```text
repoark_control_replicas_ready
repoark_control_replication_transfers_active
repoark_control_replication_deficits
repoark_control_replication_healthy_generations
repoark_control_replication_failure_domain_deficits
```

`/healthz` becomes unhealthy when configured replication quorum is below policy.

## v0.7 distributed-storage extensions

v0.7 keeps generation replication as the authoritative recovery path and adds resumable encrypted relay chunks, storage-health/capacity-aware placement, compact CAS Merkle inventories, deterministic object-level CAS reconciliation and optional local Reed–Solomon parity sets for large immutable CAS objects.

A degraded storage node can remain a readable evacuation source, but it is not selected for new placement and, when `evacuate_degraded` is enabled, does not satisfy durable quorum. Unknown storage-health telemetry from a rolling-upgrade v0.6 agent is treated similarly: it remains readable for emergency recovery but cannot masquerade as a healthy placement target.

Object-level CAS replication is an overlay rather than a replacement for immutable generation replication. Healthy target ownership uses rendezvous hashing within the configured pool; degraded nodes may source missing objects but are excluded from the target set. Reed–Solomon shards in v0.7 are local parity sets and are not independently scheduled across zones.
