# Security model

## Secrets

RepoArk configuration stores **environment-variable names**, not PATs/passwords/webhook secrets.

Environment-backed secrets include:

- GitHub token;
- GitLab token;
- restic repository/password material;
- generic webhook URL;
- Telegram bot token/chat ID.

HTTPS Git authentication uses `GIT_ASKPASS`, so the GitHub token is not embedded into remote URLs or process command-line arguments. OCI/GHCR export uses a temporary registry auth file with mode `0600` and passes only `REGISTRY_AUTH_FILE` to `skopeo`.

Use minimum required token permissions and a service/process secret manager for unattended hosts.

## Signing key

By default v0.4 creates an Ed25519 private key outside the backup root:

```text
~/.config/repoark/manifest-ed25519.key
```

The private key is written with restrictive permissions. Its authoritative verification key is `<signing_key_path>.pub`, also outside the backup root. RepoArk additionally copies the public key into the backup tree under `manifests/manifest-ed25519.pub`, but that file is a **transport copy, not a trust anchor**. Production verification always uses the externally pinned public key.

On a recovery host, restore/authenticate the public key independently and place it at `<signing_key_path>.pub`; the private key is not required for verification. Back up the private key **independently** only if you need to continue signing after loss of the RepoArk host. Do not place it next to the only copy of the backup: doing so collapses the integrity trust boundary.

This distinction prevents a full-tree attacker from replacing a manifest, its detached signature, and `manifest-ed25519.pub` with an attacker-generated key pair. AWS KMS attestation follows the same rule: verification uses the configured `key_id`, not the `KeyId` stored in the envelope.

## Integrity levels

RepoArk uses different mechanisms for different threats:

- `git fsck --full` — Git object graph integrity;
- `git bundle verify` — bundle structural/reference validity;
- SHA-256 — corruption/change detection for bundles, LFS archives, release assets and official migration archives;
- Ed25519 — manifest authenticity/integrity and signed audit-head checkpoints when the private key remains trusted;
- recovery drills — proof that selected backups can actually be materialized and checked.

Checksums alone are not attacker-resistant if the attacker can rewrite both an artifact and its expected hash. Detached signatures protect manifests and audit checkpoints. S3 Object Lock can add a storage-enforced WORM boundary. v0.4 can additionally require AWS KMS Ed25519 attestation while retaining local Ed25519 for offline verification. Hardware PKCS#11/HSM/TPM providers remain future extensions rather than being emulated by a weak software abstraction.

## Backup sensitivity

A full Git history can contain proprietary code or credentials removed from the default branch long ago. Treat the entire backup root as sensitive.

The local backup root is **not automatically encrypted** by RepoArk. Use encrypted filesystem/storage where required. The restic backend can provide encrypted off-site storage; rclone security depends on how the configured remote/crypt layer is set up externally.

## GitHub platform limitations

RepoArk intentionally does not attempt to extract secrets that GitHub does not expose, including Actions secrets. Package metadata alone is not treated as payload recovery. v0.4 can archive npm, NuGet, Maven and RubyGems payloads through their registry protocols; unsupported or ambiguous package cases remain explicit warnings.

Platform APIs can return permission-specific warnings. Those warnings must be reviewed if platform metadata is part of your RPO/RTO objective.

## GitLab secrets

A GitLab application backup alone is not sufficient for complete service recovery. Protect GitLab configuration and secret material, including `gitlab-secrets.json`, TLS keys/certificates and SSH host keys. Keep recovery exports outside the failed GitLab host's storage failure domain.

## Network surfaces

The observability server defaults to `127.0.0.1`. Do not bind it to an untrusted network without an authenticated/TLS reverse proxy.

Remote GitLab deployment executes Docker-related commands over SSH and should therefore be limited to trusted administrative hosts/accounts.

## Operational recommendations

- pin GitLab versions and upgrade deliberately;
- keep at least one independently stored copy;
- perform automated repository restore drills and periodic full GitLab DR drills;
- protect the signing private key separately from backup media;
- review manifest warnings, not only failed repository counts;
- use object lock/WORM for compliance-sensitive immutable retention where appropriate.

## v0.6 encrypted generation replication

Remote generation replication uses a separate X25519 key pair on each destination storage agent. A source creates an ephemeral X25519 key, derives a 256-bit content key with HKDF-SHA256, and encrypts the archive in authenticated AES-256-GCM chunks. The control plane relays only ciphertext.

Security checks before a relay is accepted or consumed include:

- source mTLS identity must own the running `replicate-generation` lease;
- target public key pinned in the job must equal the target agent's current heartbeat key;
- transfer ID/source/target/generation are persisted in the state store;
- ciphertext byte count and SHA-256 are persisted and matched by the target install job;
- target mTLS identity must own the running `install-replica` lease;
- transfer must be unexpired;
- extraction rejects traversal, absolute paths and unsupported archive entry types;
- generation signatures/checksums are verified before a replica becomes `ready`.

mTLS and generation encryption solve different problems. TLS authenticates workers and protects each network hop; X25519/AES-GCM prevents the relay/control-plane spool from becoming plaintext backup storage.

The CLI restore-approval path can still use local OS user names. v0.7 additionally provides an opt-in OIDC browser recovery path with group RBAC and AMR step-up; the two identity paths are explicit and are not silently interchangeable.

## v0.7 storage and browser recovery security

Storage health is part of the trust decision. With the v0.7 storage layer enabled, a node with unknown/degraded/unhealthy telemetry cannot silently become a new placement target. `degraded` remains readable for evacuation; `unhealthy` is excluded from restore routing. New CAS object archives are digest-verified against their SHA-256 object names and reject traversal, symlink/device and unsupported entry types before installation.

Resumable relay upload does not weaken the v0.6 ciphertext boundary: chunks are pieces of an already destination-encrypted blob. The server requires the exact durable offset, verifies each chunk SHA-256 before sync, and still requires the final full ciphertext hash/byte count plus source/target mTLS lease ownership.

Browser recovery is disabled by default. When enabled it uses OIDC Authorization Code + PKCE/state/nonce, ID-token verification, AES-GCM sealed HttpOnly sessions, SameSite cookies, CSRF tokens and group-based RBAC. Session sealing requires at least 32 bytes of external high-entropy secret material and sessions cannot outlive the verified ID token. RepoArk delegates WebAuthn/MFA enrollment and ceremony to the IdP and can require resulting `amr` claims for approve/schedule step-up. The browser UI cannot supply an arbitrary filesystem restore target; managed restore staging is rooted under `control_plane.generations.restore_root`.
