# Observability and notifications

RepoArk v0.4 can expose a small read-only HTTP endpoint from the standard library.
The default listener is loopback-only: `127.0.0.1:9787`.

Endpoints:

- `GET /healthz` — 200 only when the latest manifest is readable/valid and all enabled recovery policy gates pass; otherwise 503.
- `GET /readyz` — same conservative readiness contract.
- `GET /metrics` — Prometheus text exposition for repository/platform counts, CAS, policy, audit, last-backup time and process uptime.
- `GET /api/v1/status` — JSON view of the latest manifest plus policy state.
- `GET /api/v1/fleet` — per-account fleet state.
- `GET /api/v1/policy` — RPO/RTO policy evaluation.
- `GET /` — self-contained read-only fleet dashboard.

Run standalone:

```bash
repoark serve
```

When `observability.enabled: true`, `repoark daemon` starts the same endpoint automatically.
Do not expose this listener directly to an untrusted network. Put authentication/TLS in a reverse proxy if remote access is required.

## Notifications

RepoArk can send cycle summaries to both a generic JSON webhook and Telegram. Secrets stay in environment variables.

```bash
export REPOARK_WEBHOOK_URL='https://example.invalid/hooks/repoark'
export REPOARK_TELEGRAM_TOKEN='...'
export REPOARK_TELEGRAM_CHAT_ID='...'
```

Generic webhook JSON:

```json
{
  "source": "repoark",
  "success": true,
  "message": "RepoArk cycle: ...",
  "timestamp": "2026-08-20T03:00:00Z"
}
```

## Fleet and audit

`/api/v1/fleet` reports the latest state for configured fleet accounts. When audit verification is configured as required, health checks also validate the ledger chain and its signed checkpoint. Prometheus output includes audit-chain validity, audit record count, Actions artifact totals and Projects v2 owner totals.

## v0.6 HA metrics

When the control-plane HA data plane is enabled, additional gauges are exposed:

```text
repoark_control_replicas_ready
repoark_control_replication_transfers_active
repoark_control_replication_deficits
repoark_control_replication_healthy_generations
repoark_control_replication_failure_domain_deficits
repoark_control_restore_approvals_pending
```

A raw replica count is not a health signal by itself. `replication_deficits` evaluates online ready copies against `min_healthy` and, when configured, distinct failure-domain labels. The TUI control line also shows ready replicas, active encrypted transfers and pending approvals.

## v0.7 storage visibility

Agent state now also exposes storage health/capacity and compact CAS inventory information. The control API adds:

```text
GET /api/v1/control/inventories
GET /api/v1/control/inventories?left=storage-a&right=storage-b
```

The comparison reports whether the compact Merkle roots are equal and, when not equal, the divergent two-hex digest prefixes. TUI control status includes degraded/unhealthy storage-agent counts in addition to queue/replica/transfer/approval state.

When `control_plane.web_auth.enabled=true`, authenticated browser recovery routes are mounted at `/restore` plus `/auth/*`. These are mutation-capable recovery routes, unlike the ordinary read-only status dashboard, and should be served only over HTTPS with a properly configured OIDC provider.
