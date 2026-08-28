# mTLS Agents

RepoArk v0.5 agents are execution workers, not remote shells.

## Trust model

- TLS minimum: 1.3.
- Server certificate is validated by the agent against `ca_path`.
- Client certificate is mandatory and validated by the server against the same configured CA trust bundle.
- Agent identity comes from the verified certificate DNS SAN, falling back to CN.
- JSON cannot override the authenticated identity.
- A generation, backup result or follow-up job may be reported only while that certificate identity owns the corresponding running lease.

## Bootstrap CA

`repoark agents pki-init` is intentionally a small-deployment convenience. It creates an Ed25519 CA and a server certificate. `repoark agents issue NAME` issues a client-auth-only certificate.

For production PKI, pre-provision the same file paths with certificates from Vault PKI, step-ca, AD CS, an enterprise intermediate CA, or another managed PKI. The network protocol does not depend on RepoArk being the issuer.

The CA private key must never be copied to agents.

## Network exposure

The agent API is a separate listener from `observability.listen`. Keep it on a private management network, firewall it to known workers, and use a routable DNS name in `server_url` when agents are remote.

The ordinary dashboard APIs are read-only; mutating worker APIs exist only on the mTLS listener.


## Storage-node affinity

The initial `backup-repo` job is portable and has no affinity. Once an agent creates a follow-up containing local filesystem paths, the server pins it to the authenticated certificate identity. The request body cannot choose or override that affinity.

Generation metadata reported by an agent is recorded as an `agent://<identity>/...` logical location. `repoark control restore` uses that ownership marker to route a selective restore back to the same agent. If the agent is unavailable, the job is surfaced as stranded rather than being executed against a different node's filesystem.

This is intentional fail-safe behavior. True storage failover requires shared or replicated generation data; job reassignment alone is not data failover.

## v0.6 replication identity

When HA replication is enabled, every agent also owns an X25519 replication private key distinct from the TLS client key. Its public key is reported in the heartbeat over the authenticated mTLS channel and persisted against the certificate-derived agent ID.

The TLS certificate answers **who is the worker**; the replication key answers **who can decrypt this generation copy**. These concerns are intentionally separated so certificate rotation does not require re-encrypting stored generations and a TLS server/control-plane compromise does not automatically expose relay payload plaintext.

Agents may advertise placement labels:

```yaml
control_plane:
  agents:
    labels:
      zone: rack-a
      site: dc-1
      storage: nvme
```

The request body cannot change the authenticated agent identity, but labels are mutable operational metadata from that authenticated node. Do not use labels as an authorization principal; use them for placement/topology only.

With v0.6 replication enabled, a generation previously marked stranded can become restorable from another online `ready` replica. RepoArk still refuses to route a restore to an agent that merely exists in the inventory but does not own the requested generation.
