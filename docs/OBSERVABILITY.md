# Observability, browser console and notifications

RepoArk exposes operational telemetry and the embedded browser console from the Go HTTP service. The default listener is loopback-only: `127.0.0.1:9787`.

Read-oriented endpoints include:

- `GET /healthz` — 200 only when the latest manifest is readable/valid and all enabled recovery policy gates pass; otherwise 503.
- `GET /readyz` — same conservative readiness contract.
- `GET /metrics` — Prometheus text exposition for repository/platform counts, CAS, policy, audit, control-plane state, last-backup time and process uptime.
- `GET /api/v1/status` — JSON view of the latest manifest plus policy state.
- `GET /api/v1/fleet` — per-account fleet state.
- `GET /api/v1/policy` — RPO/RTO policy evaluation.
- `GET /api/v1/control/*` — control-plane status, inventories and recovery state where enabled.
- `GET /` — self-contained responsive RepoArk browser console.

Interactive browser operations live under `/api/v1/console/*`. Job/log updates use Server-Sent Events, with reconnect-safe complete snapshots and low-frequency polling fallback. Only one interactive mutation runs at a time and cancellation propagates through the operation context into external subprocess trees.

Run the primary browser console explicitly with:

```bash
repoark web
```

Running `repoark` without a command starts the same console. `repoark tui` is a deprecated compatibility alias and `repoark serve` is a compatibility alias; both are routed by the top-level entrypoint to the browser console. There is no separate terminal UI implementation.

When `observability.enabled: true`, `repoark daemon` also starts the observability server used by the scheduler process.

## Exposure and authentication

Without `control_plane.web_auth`, the interactive console is restricted to an explicit loopback listen address and browser mutations are protected by loopback Host and same-origin checks. RepoArk rejects attempts to expose the unauthenticated interactive console on a wildcard/non-loopback address.

For remote browser access, enable OIDC web authentication and terminate HTTPS at the intended reverse proxy. RepoArk reuses its encrypted OIDC session, group-to-role mapping, CSRF validation and optional AMR/ACR step-up evidence:

- viewer — read-only;
- operator — normal/elevated console operations;
- admin + configured step-up — dangerous operations.

The point-in-time recovery wizard is mounted at `/restore` with `/auth/*` when web authentication is enabled. Browser recovery remains constrained to the configured managed restore root.

Every recognized browser mutation is correlated with actor/request IDs in the tamper-evident audit ledger. With `audit.required: true`, mutation fails closed when the required audit record cannot be persisted.

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

## HA metrics

When the control-plane HA data plane is enabled, additional gauges are exposed:

```text
repoark_control_replicas_ready
repoark_control_replication_transfers_active
repoark_control_replication_deficits
repoark_control_replication_healthy_generations
repoark_control_replication_failure_domain_deficits
repoark_control_restore_approvals_pending
```

A raw replica count is not a health signal by itself. `replication_deficits` evaluates online ready copies against `min_healthy` and, when configured, distinct failure-domain labels. The browser console and control-plane APIs expose ready replicas, active encrypted transfers, pending approvals and degraded storage state.

## Distributed-storage visibility

Agent state exposes storage health/capacity and compact CAS inventory information. The control API includes:

```text
GET /api/v1/control/inventories
GET /api/v1/control/inventories?left=storage-a&right=storage-b
```

The comparison reports whether compact Merkle roots are equal and, when not equal, the divergent two-hex digest prefixes. Storage-health, queue, replica, transfer and approval state are surfaced through the browser console, status APIs and Prometheus rather than a terminal UI.
