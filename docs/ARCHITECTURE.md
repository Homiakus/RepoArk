# RepoArk architecture

RepoArk is split into five recovery planes so that no single service is also the only copy of its own backup.

```text
                         GitHub
        ┌──────────────────┼─────────────────────────┐
        │                  │                         │
   Git / LFS          REST / GraphQL          migration export
        │                  │                         │
        v                  v                         v
┌─────────────────────────────────────────────────────────────┐
│                     RepoArk backup root                     │
│ mirrors  bundles  lfs  metadata  assets  OCI  official exp │
│                        │                                    │
│                 signed manifests                            │
└───────────────┬────────┴───────────────────┬─────────────────┘
                │                            │
                v                            v
          restore drills                GitLab DR target
                                             │
                                   application/config export
                                             │
                ┌────────────────────────────┘
                v
          restic / rclone
       independent off-site copy
```

## Package boundaries

- `internal/githubapi` — authenticated GitHub REST/GraphQL inventory, metadata, release assets, Actions artifacts, Projects v2, package discovery and official migration archives.
- `internal/backup` — mirror synchronization, LFS capture, bundle creation, platform artifact capture, verification, restore and recovery drills.
- `internal/cas` — SHA-256 content-addressed immutable object store, compaction, verification and reachability GC.
- `internal/policy` — RPO/RTO and recovery-readiness policy evaluation.
- `internal/kmsattest` — optional AWS KMS detached manifest attestation.
- `internal/signing` — local Ed25519 key creation/loading and detached signatures.
- `internal/manifest` — machine-readable recovery inventory, signed latest/history manifests and retention.
- `internal/gitlab` — Compose generation, local/SSH deployment, namespace-preserving project migration, GitLab recovery exports and isolated full-service restore drills.
- `internal/offsite` — restic/rclone orchestration plus guarded S3 Object Lock replication; object-store encryption remains delegated to mature external tools.
- `internal/observability` — health/readiness/Prometheus/status HTTP surface plus the embedded browser console, SSE job stream, OIDC/RBAC/CSRF enforcement and audited interactive-operation adapter.
- `internal/notify` — generic JSON webhook and Telegram delivery using environment-backed secrets.
- `internal/fleet` — bounded-concurrency execution across multiple GitHub identities and independent backup roots.
- `internal/audit` — append-only hash-chain operation ledger with signed head checkpoints.
- `internal/app` — CLI routing, doctor, foreground scheduler and command composition.

## Critical vs best-effort data

RepoArk explicitly distinguishes two failure classes.

### Critical repository layer

A failure to synchronize or validate Git data invalidates the repository backup. Core checks include:

- bare mirror synchronization;
- `git fsck --full`;
- portable bundle generation when refs exist;
- `git bundle verify`;
- SHA-256 bundle validation;
- LFS archive checksum when LFS payloads were captured.

### Platform layer

GitHub-only state can fail because of permissions, feature availability, token type, API limits or unsupported package formats. These failures are stored as manifest warnings while preserving a successful Git recovery point.

This makes operational degradation visible without incorrectly treating a complete Git graph as lost merely because, for example, Discussions permission is missing.

## Manifest trust model

A v0.4 manifest can be protected by a detached local Ed25519 signature and optionally by an independent AWS KMS attestation. The same independent signing key can also anchor the audit-ledger head checkpoint.

```text
private key (outside backup root)
          │
          ├── public trust anchor: <signing_key_path>.pub (outside backup root)
          │                                  │
          └── sign ──> latest.json + latest.json.sig ── verify
                                │
                                └── manifest-ed25519.pub inside backup root
                                    (transport copy; never auto-trusted)
```

The verifier pins the public key outside the backup root. The public key copied into the backup is only a recovery/transport copy that must be independently authenticated before it is installed as the trust anchor on another host. Therefore replacing `latest.json`, its signature, and the backup-local public key with an attacker-generated key pair still fails verification. Optional KMS verification similarly pins `security.kms_attestation.key_id` from configuration rather than trusting the `KeyId` carried in the detached envelope.

## Invariants

1. GitHub/GitLab/notification credentials are not serialized to RepoArk YAML or manifests.
2. HTTPS Git credentials are supplied through `GIT_ASKPASS`, not credential-bearing remote URLs or CLI arguments.
3. Mirrors are updated in place; portable archives are written via temporary files before replacement.
4. Artifact paths in manifests are relative to the backup root so the recovery tree is relocatable.
5. A verified repository has passed Git object-graph validation; portable archives additionally pass format/hash validation.
6. The LFS payload is backed up separately from the Git bundle.
7. Manifest signatures are checked before normal verification when signing is required/present.
8. Platform capture failures are warnings unless they compromise the Git recovery object.
9. GitLab is a recovery target, not the sole backup copy.
10. GitLab configuration/secrets are exported separately from ordinary Git repository data.
11. Recovery drills perform a real restore rather than checking only file existence.
12. Off-site storage is a separate execution stage so it can live on a different failure domain.
13. Fleet accounts use independent token environments and backup roots; one account failure does not stop other accounts.
14. Audit records form a hash chain, and a signed checkpoint detects wholesale ledger rewrites after the checkpoint.
15. Immutable S3 replication never requests remote deletion and can require Object Lock plus versioning before upload.
16. Full GitLab drills restore into an isolated same-image instance rather than modifying the recovery target.
17. CAS GC deletes only blobs that are unreachable from all configured logical backup roots.
18. RPO/RTO policy can make health fail even when a copy exists but recovery evidence is stale or too slow.
19. GitLab application archives are SHA-256 verified before restore extraction.

## Scheduler pipeline

Daemon mode composes independent operations:

```text
backup
  │
  ├─ core verification
  │
  ├─ optional recovery drill
  │
  ├─ optional off-site replication
  │
  └─ success/failure notification
```

A failed cycle does not terminate the foreground scheduler permanently unless its context is cancelled.

## Recovery drill selection

Successful repositories are sorted and a daily deterministic offset chooses the configured sample. This produces reproducible daily selection while rotating through the repository fleet over time. Each selected repository is restored into an isolated path, checked with `git fsck`, optionally compared against mirror branch/tag refs, and checked with `git lfs fsck` when applicable.

## HTTP and browser-console boundary

The Go HTTP server owns both read-only operational telemetry and the interactive browser adapter. `/healthz`, `/readyz`, `/metrics`, status/fleet/policy/control-plane reads and the recovery wizard remain on the same service boundary; `/api/v1/console/*` adds explicit interactive operations without moving backup/recovery algorithms into frontend code.

Without OIDC web authentication, the interactive console may bind only to an explicit loopback address and browser mutations must pass loopback Host plus same-origin checks. With `control_plane.web_auth` enabled, remote browser reads and mutations reuse encrypted OIDC sessions, role mapping, CSRF validation and step-up evidence. Viewer is read-only, operator may run normal/elevated actions, and dangerous actions require admin plus configured step-up AMR/ACR.

Interactive work is single-flight. `consoleJobManager` keeps the active/last job, bounded log tail and cooperative cancellation context. Server-Sent Events publish complete immutable job snapshots by monotonically increasing revision, so browser refresh/reconnect converges without putting backpressure on backup work. Recognized browser mutations are correlated to actor/request IDs in the tamper-evident audit ledger; `audit.required` fails closed if required audit persistence is unavailable.

## Failure model

Repository jobs use bounded concurrency. A repository-level core failure is recorded in the manifest and does not discard successful backups of other repositories. The backup command exits non-zero if any repository failed.

Cancellation propagates through Git/API subprocess work. External subprocess trees are cancelled as a unit on supported platforms so wrappers cannot leave descendants holding inherited stdout/stderr handles. Best-effort GitHub platform errors are retained in `warnings` and surfaced through the browser console/status/metrics instead of disappearing into logs.

## v0.6 HA data-plane extension

```text
GitHub -> backup worker -> signed immutable generation
                           |        |
                           |        +-> local CAS
                           |
                           +-> encrypted generation replication
                                  source X25519/AES-GCM
                                           |
                                           v
                              opaque control-plane relay
                              + ReplicationTransfer lease
                                           |
                                           v
                                  target mTLS agent
                                  decrypt + verify
                                           |
                           +---------------+---------------+
                           |                               |
                      ready replica                    target CAS
                           |
                    restore selector
                    quorum/topology policy
```

Control-plane SQL is coordination state, not evidence that payload bytes exist. `GenerationReplica{state=ready}` is recorded only after the destination has verified the immutable generation. Restore selection consults that physical placement state plus heartbeat freshness.

## v0.7 distributed storage extension

v0.7 adds these package boundaries on top of the existing control/HA planes:

- `internal/storagehealth` — storage capacity and write/fsync/read probe classification;
- `internal/objectinventory` — bounded compact Merkle summary of the SHA-256 CAS namespace;
- `internal/cassync` — rendezvous-selected, digest-verified CAS object transfer archive/install;
- `internal/erasure` — bounded-memory Reed-Solomon parity/reconstruction for large immutable CAS objects;
- `internal/webauth` — OIDC/PKCE browser identity, encrypted sessions, RBAC and step-up evidence;
- `internal/observability/restore_wizard.go` — browser point-in-time recovery workflow using the existing generation/approval state machine.

The object plane is deliberately subordinate to the generation plane:

```text
immutable generation ───────────────> authoritative restore contract
       │
       ├─ reachable immutable files ─> local CAS
       │                                  │
       │                                  ├─ compact Merkle inventory
       │                                  ├─ rendezvous CAS reconciliation
       │                                  └─ optional local erasure parity
       │
       └─ generation replication ─────> cross-node point-in-time HA
```

This avoids creating a second recovery truth that could diverge from signed generation metadata.
