# RepoArk Distributed Storage

## v0.8 durable object + distributed erasure layer

v0.8 adds durable CAS object ownership/refcounts and expiring object leases, distributed Reed–Solomon shard metadata, failure-domain-aware shard placement, SMART/NVMe risk telemetry, bounded scrub/repair, and copy-only cold tiering. Shard CAS objects move through the same end-to-end encrypted resumable relay as generation/CAS replication. Health verifies that losing a represented failure domain still leaves enough unique shard indices to reconstruct the original object.

Generation-level HA remains the authoritative restore contract; distributed erasure is an auxiliary durability/repair layer.

## Recovery contract

The authoritative recovery unit is still an immutable **generation**. Object-level CAS reconciliation and erasure parity are optimization/durability layers around that contract; they do not replace generation signatures, checksums or restore drills.

## Storage state model

Each agent heartbeat can report:

- total/free bytes and free percent;
- write/fsync/read probe duration;
- `healthy`, `degraded` or `unhealthy` state;
- storage error text when present;
- compact CAS inventory root/count/bytes/segments;
- operator labels such as `site`, `zone`, `cas_pool`, `bandwidth_mbps`.

RepoArk intentionally separates three predicates:

| State | Read/evacuate | Receive new placement | Counts as durable quorum |
| --- | --- | --- | --- |
| healthy | yes | yes, subject to capacity | yes |
| degraded | yes | no | no when `evacuate_degraded=true` |
| unhealthy | no | no | no |
| unknown / old agent | yes during rolling upgrade | no | no |

This prevents a slow/failing disk from becoming a new target while preserving the chance to recover data from it.

## Capacity-aware placement

For each generation, the reconciler:

1. filters to online, allowed, mTLS-authenticated storage nodes;
2. rejects unhealthy/degraded/unknown new targets;
3. ensures the estimated generation bytes fit while preserving `min_free_bytes`;
4. enforces `min_free_percent`;
5. prefers a new configured failure domain;
6. then prefers higher `bandwidth_mbps` label;
7. then higher free percent/bytes;
8. never routes restore to an agent without a verified ready replica.

An excluded or degraded old copy remains available as an evacuation source, but no longer satisfies desired durable placement.

## Resumable encrypted relay

v0.6 encrypted a whole generation to the destination X25519 key before relaying it. v0.7 makes the relay resumable while keeping end-to-end content encryption.

Upload protocol:

```text
HEAD transfer
  -> authoritative received offset/state
PATCH transfer + exact offset + chunk SHA-256
  -> append + verify + fsync + durable receiving state
... repeat ...
POST finalize + full ciphertext SHA-256 + bytes
  -> full verification + atomic spool promotion + install job
```

Properties:

- exact server-authoritative offset;
- independently authenticated SHA-256 for every ciphertext chunk;
- final ciphertext SHA-256 and byte count;
- deterministic retry after connection loss;
- idempotent finalize;
- transfer TTL and mTLS source/target lease authorization from v0.6 remain enforced.

Download protocol uses HTTP Range into a deterministic partial file. If the local partial already equals the expected byte count and SHA-256, RepoArk accepts it without another network request.

`bandwidth_limit_mbps` optionally paces both upload and download. `0` means unlimited.

## Compact CAS Merkle inventory

RepoArk scans `CAS_ROOT/sha256/<prefix>/<digest>` and groups objects by the first two hexadecimal digest characters. There can therefore be at most **256** segment summaries per heartbeat.

For each segment it hashes sorted lines:

```text
<digest>:<size>\n
```

The top root hashes sorted segment summaries. Comparison first checks the top root and then returns only divergent prefixes. The control plane does not need the full object leaf list in every heartbeat.

Commands:

```bash
repoark control inventory
repoark control inventory storage-a storage-b
```

## Object-level CAS reconciliation

Enable only after mTLS HA replication is configured:

```yaml
control_plane:
  replication:
    enabled: true
  agents:
    enabled: true
  storage:
    inventory_enabled: true
    object_replication_factor: 2
    object_pool_label: cas_pool
```

Configuration validation rejects `object_replication_factor > 0` when the HA replication/agent transport is disabled.

Agents are grouped by `object_pool_label`. For each digest, deterministic rendezvous hashing selects the desired `object_replication_factor` members. When two pool members have divergent Merkle prefixes, a `replicate-cas` job sends only source objects in those prefixes whose rendezvous placement includes the target.

The object stream is packed into a traversal-safe archive, encrypted to the target X25519 key and transferred through the same resumable relay. The target:

1. decrypts into staging;
2. rejects unsafe tar entry types/paths;
3. hashes every object and requires the filename digest to match;
4. creates CAS prefix directories;
5. atomically installs missing objects;
6. retains an already-existing object only when its digest verifies.

The current SQL state stores compact inventory and transfer/job state, not a giant per-object replica table. This keeps control-plane state bounded while rendezvous placement defines intended ownership.

## Erasure parity

Example:

```yaml
control_plane:
  storage:
    erasure:
      enabled: true
      min_object_bytes: 4294967296
      data_shards: 6
      parity_shards: 3
      block_bytes: 1048576
```

RepoArk only protects regular immutable files that are already represented by their SHA-256 CAS object. Encoding is block bounded: memory is approximately `(data_shards + parity_shards) * block_bytes`, not the whole object size.

Each erasure set contains:

- `manifest.json`;
- original object SHA-256 and byte count;
- shard topology and block size;
- each shard byte count and SHA-256;
- `shard-NNN.rs` files.

Reconstruction accepts any sufficient set of valid shards, reconstructs block by block, truncates to the original byte count and **must match the original object SHA-256** before the output is promoted.

Commands:

```bash
repoark cas erasure-protect
repoark cas erasure-verify <sha256>
repoark cas erasure-reconstruct <sha256> /recovery/object
```

### Important boundary

v0.7 erasure shard files are local parity sets. They are not independently scheduled across zones. Cross-node durability is still provided by generation/CAS replication and off-site replication. A future data-plane version can add independent shard placement without changing the v0.7 object manifest format.

## Failure/evacuation behavior

With `evacuate_degraded=true`, a ready replica on degraded media stops satisfying desired durable factor. The reconciler can read from it as a source and creates a healthy replacement before the old copy is manually removed. RepoArk does not auto-delete the degraded copy during evacuation.

This same rule applies to explicitly excluded/decommissioning agents: they no longer satisfy desired placement, so a replacement is created first.