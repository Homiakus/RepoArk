# RepoArk v0.8.0

**RepoArk** is a Go disaster-recovery platform for GitHub/GitHub Enterprise repositories, GitHub platform data and a self-hosted GitLab recovery target. Interactive operation is provided by a self-contained browser console embedded in the RepoArk binary; the CLI remains the automation and break-glass interface.

The design assumes that a useful source-control backup must prove **recoverability**, not merely copy `.git` directories. v0.8 extends the distributed storage/recovery layer with durable object lifecycle, distributed erasure placement, media telemetry, background scrub/repair and safe storage tiering, so RepoArk now separates eight planes:

1. **Git data plane** — bare mirrors, portable bundles, LFS payloads, wikis, `git fsck`, checksums.
2. **GitHub platform plane** — issues/PRs/reviews, releases/assets, Discussions, Packages, Actions artifacts, Projects v2 and official migration exports.
3. **GitLab recovery plane** — deployment, namespace-preserving migration, application/config backup and disposable full restore drills.
4. **Durability plane** — content-addressed deduplication, restic/rclone, immutable S3 Object Lock.
5. **Control/trust plane** — browser console, fleet orchestration, RPO/RTO policies, metrics, notifications, signed manifests and tamper-evident audit.
6. **Orchestration plane** — SQLite/PostgreSQL state, leased jobs, per-repository schedules, immutable backup generations, local workers and mutually authenticated remote agents.
7. **HA data plane** — encrypted generation replication, topology-aware placement, durable transfer leases, replica quorum health, safe restore failover/failback and approval-gated recovery.
8. **Distributed storage/recovery plane** — resumable encrypted transfers, storage-health/capacity scheduling, compact Merkle CAS reconciliation, rendezvous object placement, bounded-memory erasure parity and OIDC recovery UI.

The Git path intentionally uses native Git (`git clone --mirror`, `git bundle`, `git fsck`) instead of reimplementing the Git object model in Go.

## v0.8 highlights

v0.8 keeps immutable generations as the authoritative recovery contract and hardens the distributed object layer around them:

- durable CAS object references, logical owners and transfer leases in SQLite/PostgreSQL;
- SQL schema marker v5 and config schema v8, with v0.7 configuration loading on safe defaults;
- distributed Reed-Solomon shard metadata and placement across mTLS storage agents;
- deterministic failure-domain-aware shard ownership and health checks that verify recoverability after loss of any one represented failure domain;
- exact-digest encrypted/resumable CAS transport, so a shard transfer moves only the required content-addressed object;
- idempotent `digest + logical owner` accounting so retries cannot inflate refcounts;
- CAS garbage collection consults durable refs/leases before deleting distributed erasure objects;
- SMART/NVMe JSON telemetry through `smartctl -j` / `nvme ... -o json` with an explainable risk score used for placement and evacuation;
- bounded deterministic CAS scrub sampling and SHA-256-gated local erasure repair;
- distributed repair orchestration that moves at least `data_shards` back to the corrupt node before scheduling `repair-object`;
- durable `scrub-cas` / `tier-cas` maintenance jobs with leases, retries and affinity;
- safe copy-tiering to HDD/cold storage and/or rclone/S3 without deleting the authoritative hot CAS object;
- distributed-erasure health in `/healthz`, API and Prometheus;
- CLI additions: `repoark storage disk-health`, `repoark storage scrub`, `repoark storage tier`, and `repoark control erasure`.

The v0.8 erasure layer does not replace generation-level DR. A generation remains the unit of verified restore; distributed CAS/erasure is an additional repair and durability mechanism.

## v0.7 highlights

v0.7 keeps generation-level HA as the primary recovery contract and adds an opt-in **distributed storage layer** around it:

- resumable encrypted generation/CAS relay uploads with exact server-authoritative offsets, per-chunk SHA-256, idempotent finalize and Range-resumable downloads;
- storage health telemetry based on a write/fsync/read probe plus total/free capacity;
- separate **readability**, **new-placement eligibility** and **durable-quorum eligibility** decisions so degraded media can be evacuated without being treated as healthy;
- capacity-aware placement that reserves `min_free_bytes`, checks `min_free_percent`, prefers new failure domains, then higher declared `bandwidth_mbps` and free capacity;
- automatic replica evacuation semantics: degraded/excluded nodes stop satisfying the desired durable factor before the old copy is removed;
- compact per-agent SHA-256 CAS inventory with at most 256 prefix segment roots and a top Merkle root;
- optional object-level CAS reconciliation across labelled storage pools using deterministic rendezvous placement;
- target-side CAS object verification and atomic idempotent installation;
- bounded-memory Reed-Solomon parity sets for large immutable CAS objects, with per-shard hashes and final original-object SHA-256 verification after reconstruction;
- `repoark control inventory [A B]` for CAS Merkle inventory inspection/comparison;
- `repoark cas erasure-protect`, `erasure-verify` and `erasure-reconstruct`;
- browser point-in-time recovery wizard protected by OIDC Authorization Code + PKCE, encrypted HttpOnly sessions, CSRF tokens and group-based viewer/operator/admin roles;
- optional OIDC `amr` step-up requirements such as `webauthn`/`mfa` for approve/schedule operations;
- browser restores are deliberately staged only under `control_plane.generations.restore_root`; arbitrary filesystem targets remain an explicit CLI-admin capability;
- storage health/inventory state in SQL, API, Prometheus and the then-current operator UI;
- config schema v7 and SQL schema marker v4, with rolling-upgrade-safe behavior for v0.6 agents that have not yet reported storage health.

A live heartbeat is therefore no longer enough to make a node a placement target. During a mixed-version rollout an agent with unknown storage health remains readable for emergency recovery, but is not allowed to receive new replicas or satisfy durable quorum until it reports v0.7 storage telemetry.

## v0.6 highlights

v0.6 keeps the v0.5 control-plane API and adds an opt-in **HA data plane**:

- generation replication between mTLS storage agents with **X25519 destination keys** and chunked **AES-256-GCM** payload encryption;
- the control plane acts only as an opaque encrypted relay and does not receive destination decryption keys;
- durable encrypted-transfer leases in SQLite/PostgreSQL with source/target pinning, ciphertext SHA-256, byte count, expiry and automatic spool reaping;
- replication factor and minimum-online-copy policy (`factor`, `min_healthy`);
- optional topology-aware placement using arbitrary agent labels such as `zone`, `rack` or `site`;
- `min_failure_domains` health gating so multiple replicas in one failure domain do not masquerade as HA;
- destination-side CAS hydration: only reachable generation content crosses the network, then the target deduplicates immutable payloads into its local CAS;
- safe restore failover: recovery is routed only to an online agent that has a `ready` verified replica;
- safe failback: when the original storage owner returns, restore routing prefers it again without deleting redundant copies;
- two-person restore approval workflow with expiration, requester/approver allowlists and atomic `approved → scheduled → executed` transitions;
- configurable agent heartbeat labels for placement decisions;
- replication/quorum/transfer/approval state in the then-current operator UI, API and Prometheus;
- deterministic chaos tests for storage-node loss, replica replacement, failover/failback, topology deficits and archive traversal;
- config schema v6, backward compatible with v5 defaults.

The HA layer deliberately does **not** claim that a live worker equals available data. A restore job is never failed over to a node that does not physically hold the requested generation.

## v0.5 highlights

v0.5 keeps direct v0.4 commands available and adds an opt-in **durable control plane**:

- SQLite zero-config state store and PostgreSQL backend for external/HA deployments;
- durable `queued → running → succeeded/failed` jobs with leases, abandoned-job recovery, bounded attempts and exponential retry;
- GitHub repository discovery into the state store;
- per-repository schedule rules with glob patterns, independent intervals, priority and GitLab-mirroring policy;
- single-repository backup jobs instead of forcing every schedule tick to back up the whole account;
- immutable **point-in-time generations** preserving bundle/LFS or an archived bare mirror for empty repositories;
- generation-level SHA-256 checks plus optional detached Ed25519 signatures anchored in the external RepoArk signing key;
- `repoark restore OWNER/REPO DIR --generation ID`;
- continuous `backup-repo → mirror-gitlab` follow-up jobs with storage-node affinity for agent-local paths;
- separate read-only control-plane dashboard/API and Prometheus-visible queue state, including stranded affinitized jobs;
- mTLS-only agent API with TLS 1.3, certificate-derived identity and no bearer-token enrollment path;
- local Ed25519 CA/bootstrap commands for small deployments; external enterprise PKI can replace the local CA;
- remote agent reporting of backup/generation state is accepted only while the authenticated certificate owns the corresponding running lease;
- orchestrated `restore-generation` jobs route point-in-time recovery back to the worker/agent that physically owns an `agent://` generation;
- SQLite + PostgreSQL integration-test gates in GitHub Actions.

The old `repoark daemon` remains for compatibility. New deployments that need durable scheduling should prefer `repoark control serve`.

## v0.4 highlights

v0.4 keeps v0.3 configuration backward-compatible and adds:

- **SHA-256 content-addressed storage (CAS)** for immutable payloads;
- hard-link deduplication when the filesystem supports it, with ordinary restore paths preserved;
- `repoark cas gc --dry-run` / `repoark cas gc` reachability-based garbage collection;
- real package payload archival for **npm, NuGet, Maven and RubyGems** in addition to Packages metadata;
- package size limits and SHA-256 sidecars;
- GitHub Enterprise Server custom REST/GraphQL and package-registry endpoints;
- **RPO + observed-RTO policy gates** for backup age, restore-drill age and restore duration;
- policy enforcement in `/healthz`, `/api/v1/policy`, Prometheus metrics and dashboard;
- optional **AWS KMS Ed25519 attestation** layered over local offline-verifiable Ed25519 signatures;
- S3 Object Lock mode/minimum-retention verification;
- explicit `offsite configure-lock` command for applying default retention, with a second explicit flag required for COMPLIANCE mode;
- SHA-256 metadata/sidecars for full GitLab application archives and pre-restore verification;
- automatic CAS ingestion of GitLab application archives;
- a self-contained **fleet web dashboard** at `/`;
- a separate slow GitHub Actions integration workflow that starts a real GitLab, creates an application backup and performs a disposable full restore drill.

## Existing DR coverage

RepoArk also provides:

- incremental mirrors of every selected repository;
- portable Git bundles + SHA-256;
- Git LFS archive + restore;
- GitHub Wiki mirrors;
- release assets;
- Issues, issue comments, PRs, reviews and review comments;
- labels, milestones, branches, tags, workflows, deployments, environments and hooks;
- Discussions + replies with explicit truncation warnings;
- Packages metadata/version snapshots;
- optional GHCR/container OCI export through `skopeo`;
- live GitHub Actions artifact capture before retention expiry;
- Projects v2 export through GraphQL;
- official GitHub migration archives as a second recovery channel;
- multi-account/fleet backup and verification;
- Ed25519 manifest signatures;
- SHA-256 chained audit ledger + signed checkpoint;
- repository restore drills;
- local/remote GitLab deployment;
- namespace-preserving GitHub → GitLab mirror/LFS migration;
- GitLab application + `/etc/gitlab` config/secrets backup;
- disposable full GitLab restore drill + `gitlab:check`;
- restic/rclone replication and optional WORM copy;
- health/readiness/status APIs, Prometheus metrics, Telegram and generic webhooks.

## Requirements

Required:

- Go 1.25+;
- `git`;
- no local database daemon is needed for the default SQLite control plane;
- GitHub auth via token environment variable or authenticated `gh`.

The RepoArk runtime has no Node.js or browser-framework dependency. Browser assets are embedded in the Go binary.

Feature-specific:

- `git-lfs` — LFS backup/restore;
- `docker` + Docker Compose — GitLab deployment/full restore drill;
- `ssh` + `scp` — remote GitLab;
- `restic` / `rclone` — off-site backends;
- AWS CLI v2 — S3 Object Lock and KMS attestation;
- `skopeo` — OCI export;
- `mvn` — Maven package payloads;
- `gem` — RubyGems package payloads.

npm and NuGet payloads are downloaded directly through registry HTTP; `npm`/`dotnet` are useful for independent restore validation but are not required for the downloader itself.

## Build

```bash
git clone <repo>
cd repoark
go mod tidy
go test ./...
go vet ./...
go build -trimpath -o repoark ./cmd/repoark
```

Windows:

```powershell
go build -trimpath -o repoark.exe ./cmd/repoark
```

## First run

```bash
repoark init
export GITHUB_TOKEN='...'
repoark doctor
repoark
```

`repoark` starts the browser console on the configured loopback listener. Use `repoark web` when an explicit command is preferred.

Never put PATs, AWS secrets, webhook URLs containing credentials or GitLab tokens in `config.yml`. RepoArk configuration stores only environment-variable names/profiles/key IDs.

## Main CLI

```text
repoark                               Open the embedded browser console
repoark web                           Open the embedded browser console explicitly
repoark tui                           Deprecated compatibility alias for `repoark web`
repoark init [--force]                Create configuration
repoark doctor                        Check prerequisites
repoark backup                        Backup primary GitHub account
repoark verify                        Verify Git + payload + CAS integrity
repoark restore OWNER/REPO [DIR]      Restore latest repository backup
repoark restore OWNER/REPO DIR --generation ID
                                      Restore one immutable generation
repoark drill [N]                     Real repository restore drills
repoark fleet backup                  Backup all configured accounts
repoark fleet verify                  Verify all account backups
repoark policy check                  Evaluate RPO/RTO/recovery policy
repoark audit verify                  Verify hash-chain + signed checkpoint
repoark keys generate                 Ensure local Ed25519 signing key
repoark keys verify                   Verify local + optional KMS attestation
repoark cas stats                     CAS statistics
repoark cas verify                    Hash-verify all CAS blobs
repoark cas compact                   Deduplicate immutable payloads
repoark cas gc --dry-run              Plan unreachable-object collection
repoark cas gc                        Remove unreachable CAS objects
repoark cas erasure-protect           Protect large immutable CAS objects
repoark cas erasure-verify SHA256     Verify an erasure set
repoark cas erasure-reconstruct SHA256 OUTPUT
                                      Reconstruct and hash-verify an object
repoark storage disk-health           Show SMART/NVMe + filesystem health
repoark storage scrub                 Run bounded CAS scrub/optional repair
repoark storage tier                  Copy eligible CAS objects to cold tier
repoark serve                         Compatibility alias for the browser console
repoark daemon                        Legacy scheduled control loop
repoark control serve                 Durable scheduler + local workers + optional mTLS agent API
repoark control sync                  Discover repositories into SQL state
repoark control jobs                  List durable jobs
repoark control retry JOB_ID          Retry one terminal failed job
repoark control restore OWNER/REPO GENERATION_ID [--target PATH]
                                      Restore a generation on its owning storage node
repoark control enqueue OWNER/REPO    Queue one repository backup
repoark control repos                 List scheduled repositories
repoark control stats                 Queue/repository/generation/agent counters
repoark control replicas              Replica/quorum/placement health
repoark control inventory [A B]       List/compare compact CAS Merkle inventories
repoark control replicate             Run one HA placement reconciliation
repoark control erasure               Show distributed erasure recoverability
repoark control restore-request OWNER/REPO GENERATION_ID [--target PATH]
                                      Create an approval-gated restore request
repoark control approvals             List restore approval requests
repoark control approve REQUEST_ID    Approve a restore as the current OS actor
repoark control restore-approved REQUEST_ID
                                      Schedule an approved HA restore
repoark generations list OWNER/REPO   List immutable point-in-time generations
repoark agents pki-init               Create local CA + control-plane server certificate
repoark agents issue NAME             Issue an mTLS worker certificate
repoark agent run                     Run a remote worker agent
repoark offsite                       Replicate configured off-site copies
repoark offsite verify-lock           Verify Object Lock/retention/versioning
repoark offsite configure-lock        Apply configured default retention
repoark offsite configure-lock --allow-compliance
repoark gitlab compose                Generate pinned GitLab Compose
repoark gitlab deploy [--remote user@host]
repoark gitlab password [--remote user@host]
repoark gitlab backup [--remote user@host]
repoark gitlab migrate                Preserve GitHub owner namespaces
repoark gitlab drill [ARCHIVE]        Full disposable application restore
repoark github export user            Official GitHub migration export
repoark github export org ORG         Official organization migration export
```

Global: `--config PATH`.

## Browser console

The primary interactive UI is a responsive, dependency-free browser console embedded in the Go binary. It exposes the former terminal actions as explicit operation cards, streams active job/log snapshots over Server-Sent Events, falls back to low-frequency polling when SSE is unavailable and supports cooperative cancellation.

Important execution rules:

- only one interactive mutation can run at a time;
- refresh/reconnect converges on the same active job through revisioned SSE snapshots;
- cancellation propagates through external subprocess trees rather than killing only a wrapper process;
- without OIDC the interactive listener must be loopback-only and browser mutations are protected against cross-origin/DNS-rebinding requests;
- remote access uses OIDC Authorization Code + PKCE, encrypted sessions, RBAC, CSRF and optional AMR/ACR step-up;
- recognized browser mutations are written to the tamper-evident audit ledger, and `audit.required` fails closed.

The old Bubble Tea/Lip Gloss implementation has been removed. `repoark tui` remains only as a compatibility command that redirects to the browser console.

## Durable control plane

Enable it explicitly:

```yaml
control_plane:
  enabled: true
  store:
    driver: sqlite
    sqlite_path: ~/.repoark/control/repoark.db
  workers:
    concurrency: 2
    poll_interval: 2s
    lease: 2m
    max_attempts: 5
  scheduler:
    enabled: true
    tick: 30s
    discovery_interval: 30m
    default_interval: 24h
    policies:
      - pattern: critical/*
        interval: 1h
        priority: 100
        mirror_gitlab: true
      - pattern: "*"
        interval: 24h
        priority: 50
        mirror_gitlab: true
  generations:
    enabled: true
    root: ~/.repoark/generations
    restore_root: ~/.repoark/recovery
    keep_per_repo: 14
```

Initialize discovery and run the service:

```bash
repoark control sync
repoark control serve
```

`control serve` periodically refreshes GitHub inventory, enqueues repositories whose `next_run_at` is due, leases jobs to local workers or authenticated remote agents, and records generations/results in the SQL store. A running job whose worker disappears becomes eligible again after its lease expires unless it has exhausted `max_attempts`, in which case the lease reaper makes it terminal `failed`. Active jobs are deduplicated by `(kind,target,affinity)` so repeated scheduler ticks do not create a queue storm while node-local follow-ups remain distinct.

When a remote worker creates a follow-up whose payload references its local mirror/LFS paths, RepoArk pins that job to the worker certificate identity. If that storage node disappears, the control-plane stats expose the job as `stranded`; RepoArk does **not** pretend that another node can recover paths it does not possess. Point-in-time restore uses the same ownership rule: `repoark control restore OWNER/REPO GENERATION_ID` reads generation ownership from SQL and sends an `agent://` recovery to the owning agent; local generations are reserved to the `__repoark_local__` control-worker group. With no explicit `--target`, the executing node restores under its local `control_plane.generations.restore_root`.

For PostgreSQL:

```yaml
control_plane:
  store:
    driver: postgres
    dsn_env: REPOARK_DATABASE_URL
```

```bash
export REPOARK_DATABASE_URL='postgres://repoark:...@db/repoark?sslmode=verify-full'
```

RepoArk never writes that DSN into generated state or manifests.

## Point-in-time generations

A generation is an immutable recovery unit created after a successful scheduled repository backup. For non-empty repositories it preserves the portable Git bundle; for an empty repository it stores a compressed bare mirror. LFS payloads are preserved separately. Generation artifacts have SHA-256 digests, and when manifest signing is enabled the generation metadata gets its own detached Ed25519 signature verified against `security.signing_key_path.pub` outside the backup root.

```bash
repoark generations list Homiakus/project
repoark restore Homiakus/project ./project-old --generation 20260820T050000.000000000Z
```

Because the current bundle is replaced atomically, RepoArk can hard-link it into a generation on the same filesystem; the next bundle replacement creates a new inode and does not mutate the historical generation. Cross-device capture falls back to a durable copy.

## mTLS worker agents

The agent listener is separate from the browser/observability listener. It requires a valid client certificate and TLS 1.3. The server derives worker identity from the verified certificate and ignores user-supplied identity fields. Generation/result reports are accepted only for a job currently leased to that certificate identity.

Small deployment bootstrap:

```bash
repoark agents pki-init
repoark agents issue workshop-node-1
```

Copy **only** the CA certificate plus that agent's client certificate/private key to the worker. Keep the CA private key on the control-plane host or replace the built-in bootstrap with your existing CA. Configure the worker's `server_url`, `ca_path`, `client_cert_path`, `client_key_path`, then run:

```bash
repoark agent run
```

The protocol leases only predefined RepoArk job types; it is not a generic remote shell.

## HA generation replication

Enable after at least two storage locations are available:

```yaml
control_plane:
  agents:
    labels:
      role: backup-worker
      zone: rack-a
      site: dc-1
  replication:
    enabled: true
    factor: 2
    min_healthy: 2
    reconcile_interval: 1m
    agent_timeout: 2m
    transfer_ttl: 2h
    include_local: true
    failure_domain_label: zone
    min_failure_domains: 2
```

Each destination agent publishes a separate X25519 replication public key in its mTLS-authenticated heartbeat. The source archives a verified immutable generation and encrypts the stream for that destination. The relay blob is bounded by `max_transfer_bytes`, hash-checked, durable in the control-plane store and automatically expired after `transfer_ttl`. The destination decrypts, extracts only into a traversal-safe staging tree, verifies generation signatures/checksums, atomically installs it, and optionally compacts it into local CAS.

The reconciler differentiates **stored copies** from **online ready copies**. `factor` controls durable placement, while `min_healthy` controls restore availability. When `failure_domain_label` is configured, a generation is healthy only when its online copies also span `min_failure_domains` distinct label values.

```bash
repoark control replicas
repoark control replicate
```

Restore selection is data-aware. If the original storage node is offline but another verified replica is online, RepoArk routes the restore to that node. If no online `ready` replica exists, the operation is rejected instead of pretending to fail over.

## Distributed storage layer

v0.7 deliberately keeps immutable **generations** as the recovery unit while using CAS objects to reduce transfer/storage amplification. Enable storage telemetry first:

```yaml
control_plane:
  storage:
    enabled: true
    min_free_bytes: 10737418240
    min_free_percent: 10
    max_probe: 3s
    evacuate_degraded: true
    inventory_enabled: true
    inventory_interval: 10m
    chunk_bytes: 8388608
    chunk_retries: 5
    bandwidth_limit_mbps: 0
    object_replication_factor: 0
    object_pool_label: cas_pool
```

Agents can additionally advertise scheduling hints through authenticated heartbeat labels:

```yaml
control_plane:
  agents:
    labels:
      site: dc-1
      zone: rack-a
      cas_pool: primary
      bandwidth_mbps: "1000"
```

The storage probe distinguishes three states. `healthy` can read and receive new placement. `degraded` remains readable so it can be evacuated, but with `evacuate_degraded=true` it no longer satisfies durable quorum or receives new replicas. `unhealthy` is excluded from restore routing as well. Unknown v0.6-era health is treated as readable but not placement-safe during rolling upgrades.

Encrypted relay transfers are resumable. The control plane persists the confirmed ciphertext offset, verifies each uploaded chunk before `fsync`, verifies the final full SHA-256/size, and keeps finalize idempotent. Downloads use HTTP Range into a deterministic partial file and accept an already-complete locally verified file without another network request.

### CAS Merkle reconciliation

When `object_replication_factor > 0`, RepoArk groups agents by `object_pool_label`, compares compact CAS inventories, identifies only divergent two-hex prefixes and uses rendezvous hashing to select the desired target agents for each digest. The heartbeat never sends the full CAS leaf list: it sends at most 256 segment summaries plus one top Merkle root. Generation replication remains independent and is still the authoritative point-in-time restore path. Object replication requires HA replication + mTLS agents to be enabled; configuration validation rejects a silent partial setup.

```bash
repoark control inventory
repoark control inventory storage-a storage-b
repoark control replicate
```

### Erasure parity for large immutable objects

Erasure protection is optional and local to each CAS storage node in v0.7. It does **not** pretend that parity shards are independently placed across failure domains. Large immutable CAS objects can be encoded into `data_shards + parity_shards` block-bounded shard files, each with its own SHA-256. Reconstruction requires at least `data_shards` valid shards and then verifies the reconstructed object's original SHA-256 before success. Cross-node durability still comes from generation/CAS replication and off-site copies.

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

```bash
repoark cas erasure-protect
repoark cas erasure-verify <sha256>
repoark cas erasure-reconstruct <sha256> /tmp/recovered-object
```

## Browser recovery with OIDC step-up

The recovery UI is disabled by default. RepoArk delegates credentials, MFA and WebAuthn ceremony to an OIDC identity provider instead of storing browser authenticators itself. It uses Authorization Code + PKCE/state/nonce, verifies the ID token, maps a configurable group claim into `viewer`, `operator` and `admin`, seals the local session with AES-GCM, uses HttpOnly/SameSite cookies and validates a CSRF token for every mutation. Session lifetime never exceeds the verified ID-token expiry.

```yaml
control_plane:
  web_auth:
    enabled: true
    mode: oidc
    issuer: https://id.example.com/realms/repoark
    client_id: repoark
    client_secret_env: REPOARK_OIDC_CLIENT_SECRET
    redirect_url: https://repoark.example.com/auth/callback
    session_key_env: REPOARK_SESSION_KEY
    group_claim: groups
    scopes: [profile, email, groups]
    step_up_acr_values: []
    viewer_groups: [repoark-viewers]
    operator_groups: [repoark-operators]
    admin_groups: [repoark-admins]
    required_amr: [webauthn]
    secure_cookies: true
```

When restore requester/approver allowlists are non-empty, the browser OIDC `sub` or email must also match the relevant allowlist.

With observability/control plane enabled, open `/restore`. A viewer can inspect repositories/generations, an operator can request recovery, and an admin with the configured step-up `amr` can approve/schedule it. Browser recovery never accepts an arbitrary destination path: it restores under the executing node's configured `restore_root`.

## Restore approvals

For environments where restore can overwrite or expose sensitive source history, enable a second-person gate:

```yaml
control_plane:
  restore_approval:
    enabled: true
    approval_ttl: 30m
    require_distinct_approver: true
    requesters: [backup-operator]
    approvers: [dr-lead]
```

```bash
repoark control restore-request acme/service 20260820T010203Z
repoark control approvals
repoark control approve restore-...
repoark control restore-approved restore-...
```

When the gate is enabled, direct generation restore paths refuse to bypass it. Scheduling is atomic in the state store and successful job completion moves the approval to `executed`. The CLI actor model uses the local OS account; remote browser operations can additionally use the OIDC/RBAC/step-up boundary described above.

## Content-addressed storage

```yaml
cas:
  enabled: true
  root: ~/.repoark/cas
  min_file_size: 1048576
  auto_compact: true
```

Large immutable payloads remain visible under their normal backup path but are stored in a SHA-256 CAS. RepoArk uses a hard link when possible and safely falls back to the normal file when cross-device/filesystem policy prevents linking.

Typical compacted trees include bundles, LFS archives, release assets, Actions artifacts, package payloads, OCI archives, official migration exports and GitLab application archives.

Garbage collection is reachability-based rather than link-count-based:

```bash
repoark cas gc --dry-run
repoark cas gc
repoark cas verify
```

The GC hashes eligible files under primary/fleet/GitLab logical roots, builds a reachable digest set, then deletes only CAS blobs not present in that set.

## GitHub package payloads

Enable separately from package metadata:

```yaml
package_payloads:
  enabled: true
  npm: true
  nuget: true
  maven: true
  rubygems: true
  max_bytes: 2147483648
```

Payload path:

```text
backups/packages/<owner>/<repo>/<registry>/...
```

Each downloaded payload gets a `.sha256` sidecar and `repoark verify` checks the package tree. Package metadata remains available even when a registry payload cannot be downloaded.

GitHub package registries have ecosystem-specific behavior. npm and NuGet are retrieved directly over HTTPS. Maven uses an isolated temporary Maven repository/settings file; RubyGems uses an isolated temporary HOME. PATs are never placed in process arguments. Registry failures are warnings and do not invalidate an otherwise recoverable Git repository.

## GitHub Enterprise Server

Use the GHES API roots explicitly:

```yaml
github:
  api_url: https://github.example.com/api/v3
  graphql_url: https://github.example.com/api/graphql
```

Fleet accounts can each have independent REST/GraphQL URLs. Package registry URLs are separately overridable under `package_payloads`, because an enterprise package hostname may differ from the API hostname.

## RPO/RTO policy engine

```yaml
policy:
  enabled: true
  enforce_in_health: true
  max_backup_age: 30h
  max_failed_repositories: 0
  require_signed_manifest: true
  max_recovery_drill_age: 7d
  max_recovery_drill_duration: 15m
  max_gitlab_drill_age: 30d
  max_gitlab_drill_duration: 45m
  max_offsite_age: 30h
```

Age gates express **RPO/recovery evidence freshness**. Duration gates use the timestamps of the latest successful drill and express the **observed RTO ceiling**.

```bash
repoark policy check
```

When `enforce_in_health` is true, any critical policy violation makes `/healthz` return HTTP 503.

## Signatures and KMS attestation

Local Ed25519 signing remains the primary offline trust mechanism:

```yaml
security:
  sign_manifests: true
  signing_key_path: ~/.config/repoark/manifest-ed25519.key
```

Optional AWS KMS attestation adds an independent trust anchor:

```yaml
security:
  kms_attestation:
    enabled: true
    key_id: alias/repoark-manifest
    signing_algorithm: ED25519_SHA_512
    region: us-east-1
    profile: repoark-kms
    require_valid: true
```

RepoArk sends the manifest as a RAW message to `aws kms sign`, stores a detached KMS envelope and calls KMS Verify during verification/policy checks. Verification pins the configured KMS `key_id`; the `KeyId` stored in the envelope is audit metadata, not a trust decision. The local Ed25519 signature is intentionally retained so a recovery environment can still validate the backup when AWS/network access is unavailable.

The Ed25519 verification trust anchor is **`signing_key_path.pub` outside the backup root**. RepoArk also copies a public key into `manifests/manifest-ed25519.pub` for transport/recovery convenience, but verification never trusts that backup-local copy automatically. On a new recovery host, place the independently verified public key at `<signing_key_path>.pub` before running `verify`, `drill`, `policy check`, or health checks.

## Immutable S3 Object Lock

```yaml
offsite:
  enabled: true
  backend: restic
  object_lock:
    enabled: true
    bucket: company-repoark-worm
    prefix: repoark
    region: us-east-1
    profile: repoark-backup
    require_enabled: true
    expected_mode: GOVERNANCE
    min_retention_days: 30
```

Validate without mutation:

```bash
repoark offsite verify-lock
```

Explicitly apply the configured default retention:

```bash
repoark offsite configure-lock
```

For `COMPLIANCE`, RepoArk refuses to continue unless you explicitly add `--allow-compliance`. This is deliberate: compliance-retained versions cannot have their retention shortened or be deleted before expiry.

Immutable replication does not send delete propagation.

## GitLab DR

`gitlab.image` is pinned. RepoArk application backup includes:

- `gitlab-backup create` application archive;
- GitLab configuration/secrets directory;
- backup metadata containing exact image/edition;
- SHA-256 metadata + sidecar.

The restore drill verifies the outer archive checksum **before extraction**, starts a new isolated GitLab using the configured exact image, restores the application backup, restarts the container and executes:

```text
gitlab-rake gitlab:check SANITIZE=true
```

The production container and production bind mounts are never used as the drill target.

## Real GitLab integration gate

Fast CI remains in `.github/workflows/ci.yml`.

`.github/workflows/gitlab-restore-drill.yml` is a separate weekly/manual slow test. It:

1. builds the actual RepoArk Go binary with the production module graph;
2. starts a real pinned GitLab container;
3. waits for health;
4. creates a real GitLab application/config archive;
5. runs `repoark gitlab drill` against it;
6. retains diagnostics when the drill fails.

The underlying workflow can also be executed locally:

```bash
./scripts/integration-gitlab-drill.sh
```

## Browser console, dashboard and metrics

```yaml
observability:
  enabled: true
  listen: 127.0.0.1:9787
```

Primary interactive launch:

```bash
repoark
# or
repoark web
```

The console preserves the operational/read endpoints on the same service boundary, including:

- `/` — embedded responsive browser console;
- `/healthz`, `/readyz`;
- `/api/v1/status`;
- `/api/v1/fleet`;
- `/api/v1/policy`;
- `/api/v1/control/*` when enabled;
- `/api/v1/console/*` for interactive operation state/actions/SSE;
- `/restore` and `/auth/*` when OIDC recovery UI is enabled;
- `/metrics`.

The UI has no external JavaScript/CSS/CDN dependency. A real Chromium CI gate launches the compiled binary and verifies desktop/mobile rendering, SSE reconnect/cancellation, local security boundaries and the authenticated OIDC/PKCE/RBAC/CSRF/step-up path through a disposable HTTPS reverse proxy.

`repoark serve` is retained as a compatibility alias for the same browser console.

## Backup tree (abridged)

```text
~/.repoark/backups/
├── mirrors/owner/repo.git/
├── bundles/owner/repo.bundle
├── lfs/owner/repo.lfs.tar.gz
├── wikis/owner/repo.wiki.git/
├── release-assets/...
├── actions-artifacts/...
├── packages/...
├── oci/...
├── metadata/...
├── official-exports/...
├── state/
└── manifests/

~/.repoark/cas/
└── sha256/ab/abcdef...
```

Manifest artifact paths stay relative to the logical backup root, so a complete recovery tree remains portable.

## Security model

- no PAT in Git remote URLs;
- Git uses `GIT_ASKPASS`;
- no secrets in normal YAML config;
- temporary Maven/Ruby registry credentials use mode-0600 isolated files and are deleted;
- local signing private key is outside the backup root;
- optional KMS trust anchor has no exportable private key;
- manifests and audit checkpoints are signed;
- immutable copy never propagates deletion;
- restore drills always target isolated paths/containers;
- COMPLIANCE retention needs an explicit high-risk flag;
- all portable payloads use SHA-256 integrity metadata;
- unauthenticated interactive browser access is loopback-only;
- remote browser mutations require OIDC session/RBAC/CSRF and configured step-up for dangerous actions.

## Known boundaries

- v0.6 HA replication is generation-oriented: only content reachable from retained generations is transferred; it intentionally does not mirror orphaned CAS blobs. The destination rehydrates/deduplicates its CAS from the verified generation.
- CLI restore approval uses the local OS actor model; browser recovery can add OIDC group RBAC and AMR/ACR step-up, but RepoArk is not an LDAP directory or identity provider.
- GitHub does not expose repository/action secrets for backup; preserve secret sources separately.
- Packages differ by ecosystem; metadata backup is broader than payload backup. Unsupported/ambiguous package versions are reported as warnings rather than silently marked complete.
- Maven payload download currently requires GitHub package naming from which `groupId:artifactId` can be inferred.
- GitHub Actions artifacts that already expired before RepoArk runs cannot be recovered from GitHub.
- A local unit/contract test is not equivalent to the slow real GitLab integration workflow; both are intentionally kept.

See `docs/V0.4.0.md`, `docs/PACKAGE_DR.md`, `docs/CAS_AND_POLICY.md` and `docs/UPGRADE_V0.3_TO_V0.4.md` for implementation details.

### PostgreSQL HA and storage

PostgreSQL makes queue/state sharing safe across control-plane instances, but it does not replicate repository files. If multiple local `control serve` instances share the `__repoark_local__` worker group, their backup/generation roots must be shared or independently replicated. Node-local storage should be modeled as mTLS agents so RepoArk can preserve storage affinity explicitly.
