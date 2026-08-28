# Upgrade RepoArk v0.6 -> v0.7

## Compatibility

v0.7 uses config schema `7` and SQL schema marker `4`. Existing v0.6 configuration loads with new distributed-storage and web-auth functionality safe by default: object replication, erasure and browser auth are opt-in.

## Recommended rolling order

1. Back up the RepoArk SQL database and configuration.
2. Upgrade the control-plane binary.
3. Start it and verify SQL migration to schema marker `4`.
4. Upgrade storage agents one at a time.
5. Wait for each upgraded agent to report `storage_health`, capacity and CAS inventory.
6. Confirm `repoark control stats`, `repoark control replicas` and `repoark control inventory`.
7. Only then enable `object_replication_factor` or erasure parity.
8. Configure OIDC/web recovery last, after HTTPS and the IdP redirect URI are ready.

## Mixed v0.6/v0.7 agents

An old agent heartbeat has no storage health telemetry. v0.7 deliberately treats that state as:

- readable for emergency recovery;
- not eligible for new placement;
- not sufficient for durable quorum.

This avoids both data unavailability during a rolling upgrade and a false healthy result from an unprobed disk.

## SQL migration

v0.7 adds these additive `agents` columns:

```text
storage_health
storage_total_bytes
storage_free_bytes
storage_free_percent
storage_probe_ms
storage_error
inventory_root
inventory_objects
inventory_bytes
inventory_json
```

No generation, replica, transfer or approval table is replaced.

## New configuration

Review `control_plane.storage` and `control_plane.web_auth` in `configs/config.example.yml`.

Important: if `control_plane.storage.object_replication_factor > 0`, both `control_plane.replication.enabled` and `control_plane.agents.enabled` must be true. Validation rejects an incomplete topology.

## First storage validation

```bash
repoark control stats
repoark control replicas
repoark control inventory
repoark control replicate
```

Start with `object_replication_factor: 0`, observe health/capacity values, then choose a factor and pool label.

## Erasure rollout

Start with a high `min_object_bytes`, run:

```bash
repoark cas erasure-protect
repoark cas erasure-verify <digest>
```

and perform a reconstruction drill before considering the parity layer operational. Erasure sets are local in v0.7; keep normal replica/off-site policies.

## Web recovery rollout

Use HTTPS. Create an OIDC client with the exact callback URL. Configure group mappings and a random high-entropy `REPOARK_SESSION_KEY`. If `required_amr` contains `webauthn` or `mfa`, verify your IdP actually emits that `amr` value after the desired step-up flow before enabling it in production.
