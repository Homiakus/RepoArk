# Upgrade RepoArk v0.5 -> v0.6

v0.6 is backward compatible at the configuration level. Existing v5 files load on top of v6 defaults; HA replication and restore approval remain disabled until explicitly enabled.

## 1. Replace the binary

Build with Go 1.25+ and run the ordinary checks:

```bash
go mod tidy
go test ./...
go vet ./...
go build -trimpath -o repoark ./cmd/repoark
```

## 2. Back up the control-plane database

Stop `repoark control serve` and copy the SQLite database, or take a PostgreSQL-native backup before the first v0.6 start.

## 3. Database migration

The idempotent migration adds:

- `agents.replication_public_key` when absent;
- `generation_replicas`;
- `replication_transfers`;
- `restore_approvals`;
- related indexes.

`repoark_meta.schema_version` becomes `3` for the internal SQL schema. This number is independent of the user config schema (`version: 6`).

## 4. Add agent replication keys

On every remote storage agent:

```yaml
control_plane:
  agents:
    replication_key_path: ~/.config/repoark/pki/replication-x25519.key
    labels:
      zone: rack-a
```

The key is created automatically on first agent run with restrictive permissions.

## 5. Enable replication gradually

Start with two storage locations and observe `control replicas` before enforcing topology:

```yaml
control_plane:
  replication:
    enabled: true
    factor: 2
    min_healthy: 2
    transfer_ttl: 2h
    include_local: true
```

Then, after every agent reports the intended labels, optionally add:

```yaml
    failure_domain_label: zone
    min_failure_domains: 2
```

Do not enable `min_failure_domains` until enough distinct labeled agents exist, or health will correctly remain degraded.

## 6. Optional restore approval

Enable only after operational roles are chosen:

```yaml
control_plane:
  restore_approval:
    enabled: true
    approval_ttl: 30m
    require_distinct_approver: true
```

Once enabled, direct generation restore is intentionally blocked in favor of request/approve/schedule commands.

## 7. Verify

```bash
repoark control sync
repoark control replicate
repoark control replicas
repoark control stats
```

Then intentionally stop one storage agent and verify that quorum/health and restore routing match the expected policy.
