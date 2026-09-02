# RepoArk TUI → Web UI migration

Status: **implementation complete**. The browser console is the primary interactive UI, the legacy Charm terminal implementation has been removed, and the CLI remains the automation/break-glass interface. `repoark tui` is intentionally retained only as a deprecated compatibility alias that starts the web console; removing that alias is deferred to a future breaking window.

## Goals

1. Replace terminal-only interaction with a responsive browser console without moving backup/recovery business logic into the frontend.
2. Reuse the existing observability/control-plane HTTP boundary instead of introducing a second application service.
3. Preserve CLI behavior for scripts, systemd, CI and disaster-recovery procedures.
4. Make mutating browser operations safe by default: single-flight execution, cancellation, OIDC/RBAC/CSRF for remote use, and loopback-only operation when authentication is not configured.
5. Keep the frontend dependency-free and embedded in the Go binary so deployment remains a single executable.

## Architecture

```text
Browser
  │
  ├── GET /                         responsive RepoArk Console
  ├── GET /api/v1/status            existing manifest/policy API
  ├── GET /api/v1/fleet             existing fleet API
  ├── GET /api/v1/control/*         existing control-plane API
  ├── GET /healthz /metrics         existing operations endpoints
  │
  └── /api/v1/console/*             interactive-operations API
       ├── state                    capabilities/tools/actions
       ├── session                  local/OIDC session state + CSRF
       ├── job                      active/last operation + log tail
       ├── events                   SSE job/log snapshots + reconnect
       ├── jobs/{name}              start operation
       └── job/cancel               cooperative cancellation
                │
                ▼
        consoleJobManager
        single active operation
        revision broadcast
                │
                ▼
 backup / fleet / gitlab / offsite / CAS / policy packages
```

The web layer is an adapter only. It does not duplicate repository backup, verification, GitLab DR, CAS or policy algorithms.

## Interaction model

The console exposes the former TUI actions as explicit cards:

| Former TUI action | Web operation | Availability |
|---|---|---|
| backup | `backup` | always |
| fleet | `fleet-backup` | `fleet.enabled` |
| verify | `verify` | always |
| policy | `policy` | `policy.enabled` |
| CAS compact | `cas-compact` | `cas.enabled` |
| repository drill | `repo-drill` | `recovery_drill.enabled` |
| GitLab restore drill | `gitlab-drill` | GitLab + drill enabled |
| GitLab deploy | `gitlab-deploy` | `gitlab.enabled` |
| GitLab migrate | `gitlab-migrate` | `gitlab.enabled` |
| GitLab backup | `gitlab-backup` | `gitlab.enabled` |
| offsite sync | `offsite` | `offsite.enabled` |

Only one operation can run at a time. This preserves the former terminal UI execution invariant and avoids concurrent mutation of the same backup/GitLab state. Each job keeps a bounded in-memory log tail and supports cooperative cancellation through `context.Context`.

Live activity uses Server-Sent Events. The server sends complete immutable job snapshots keyed by a monotonically increasing revision. A reconnect can therefore resume with `Last-Event-ID`, and a slow browser never applies pressure to the backup pipeline. The browser falls back to low-frequency job polling if EventSource is unavailable or temporarily disconnected.

## Security model

### Local mode

Without `control_plane.web_auth`, `RunConsole` accepts only an explicit loopback listen address (`127.0.0.1`, `::1`, or `localhost`). A configuration such as `0.0.0.0:9787` is rejected rather than silently exposing privileged buttons.

Local browser mutations additionally require a loopback Host and same-origin `Origin`/`Referer` when those headers are present. This closes browser CSRF and DNS-rebinding paths while preserving direct local CLI/HTTP diagnostics that do not send an Origin header.

### Authenticated remote mode

When OIDC web auth is enabled, the existing RepoArk encrypted session, role mapping and CSRF validation are reused:

- viewer: read-only console;
- operator: normal/elevated operations;
- admin + step-up: dangerous GitLab deploy/migrate operations;
- every browser mutation validates the session CSRF token.

The existing recovery wizard remains under `/restore` and retains its stronger restore approval and step-up rules.

The browser CI includes a disposable standards-shaped OIDC provider and HTTPS reverse proxy. It verifies the real Authorization Code + PKCE redirect flow, RS256 ID-token verification through JWKS discovery, viewer/operator/admin role mapping, secure encrypted session cookies, CSRF enforcement, and a fresh admin step-up that must carry the configured WebAuthn AMR/ACR before a privileged recovery approval endpoint is allowed past the security boundary.

### Auditability

Every recognized browser operation is correlated with an actor and request ID in the tamper-evident audit ledger. The ledger records request, rejection, completion/error/cancellation, and explicit cancel actions. When signed audit checkpoints are configured, the checkpoint is advanced with the web audit record. With `audit.required: true`, mutations fail closed before execution if the audit ledger cannot be written.

### HTTP hardening

The console adds `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, `Permissions-Policy`, and a restrictive Content Security Policy. The UI has no CDN/runtime frontend dependencies. SSE responses explicitly disable intermediary buffering and clear only the per-response write deadline required for a long-lived stream; the normal API server timeouts remain intact.

## Migration phases

### Phase 1 — Primary web console — implemented

- `repoark` with no command starts the web console.
- `repoark web` starts the web console explicitly.
- `repoark tui` starts the web console with a deprecation notice.
- Existing CLI subcommands remain unchanged.
- Existing observability, metrics and control-plane endpoints are mounted into the same console server.
- Former TUI operations are available from the browser with job state, log tail and cancellation.
- Responsive layout supports desktop, tablet and narrow mobile screens.

### Phase 2 — Soak period and parity validation — completed

Parity and resilience were proven before deleting the old terminal implementation:

- a regression test locks all 11 former TUI actions to the web operation adapter;
- browser mutation audit records include actor/request correlation and fail closed when required audit storage is unavailable;
- HTTP regression tests cover operation completion, rejection and cancellation;
- local-origin/DNS-rebinding regression coverage is in place;
- SSE live job/log delivery uses reconnect-safe full snapshots and polling fallback;
- race coverage exercises the job/audit/event paths;
- Chromium CI launches the compiled `repoark web` binary and verifies startup, routing, security headers, local session rendering, operation cards, SSE transport, polling suppression and a narrow mobile viewport;
- authenticated Chromium runs behind a disposable HTTPS reverse proxy and OIDC provider, covering PKCE, signed ID tokens, viewer/operator/admin RBAC, CSRF, Secure/SameSite session cookies and WebAuthn-style step-up AMR/ACR;
- a production-shaped long-running `offsite` operation is exercised with a safe fake `restic`: Chromium refreshes while the job is running, a new EventSource converges to the same job ID/state, and cancellation reaches the terminal `cancelled` state;
- external-command cancellation kills subprocess trees rather than only wrapper processes, preventing orphan descendants from keeping inherited stdout/stderr handles open;
- Linux/Windows cross-builds validate the platform-specific process-tree cancellation implementation;
- failure diagnostics preserve RepoArk/auth-harness server logs and browser screenshots without adding Node.js to the runtime distribution.

### Phase 3 — Remove terminal UI dependencies — implemented

- `internal/tui` has been deleted;
- Bubble Tea, Lip Gloss and their orphan terminal-rendering dependency graph have been removed from `go.mod`/`go.sum`;
- `internal/app` no longer contains a terminal-UI route or imports Charm packages;
- the public `repoark tui` command remains only as a deprecated alias handled by the top-level entrypoint and routed to the browser console;
- CLI backup, restore, control-plane, agent and disaster-recovery commands remain available unchanged.

Removing the `tui` compatibility alias itself is deferred to a future major/minor breaking window so existing launch scripts do not break merely because the implementation changed.

### Phase 4 — Progressive operations UX

Recommended next increments:

- persisted job history instead of only the active/last in-memory job;
- per-operation structured result summaries;
- repository-level backup/verify filters;
- config validation/editor with secrets never returned to the browser;
- audit trail view and immutable drill evidence links;
- control-plane scheduler editor with dry-run/preview.

## Acceptance criteria

- `repoark` starts successfully with the default config and binds only to `127.0.0.1:9787`.
- the dashboard loads without Node.js or external static assets;
- backup/verify can be started from the browser and produce live job state/logs;
- live job/log updates use SSE when available and converge after reconnect;
- an active external-process operation survives browser refresh and remains the same server-side job;
- a second operation receives HTTP 409 while one is running;
- cancel propagates through the operation context and terminates descendant subprocesses rather than leaving orphan children;
- disabled features cannot be started even by direct API calls;
- no-auth web console refuses non-loopback listen addresses;
- authenticated mutations enforce RepoArk roles and CSRF;
- dangerous remote GitLab operations require admin step-up;
- OIDC browser CI proves Authorization Code + PKCE, signed ID-token verification, Secure/SameSite session cookies and viewer/operator/admin role mapping through an HTTPS reverse proxy;
- an admin session without the configured step-up AMR is rejected, while a fresh step-up session carrying the required WebAuthn AMR passes the authorization boundary;
- recognized web mutations are represented in the tamper-evident audit ledger;
- `audit.required` blocks mutation when the audit ledger is unavailable;
- CI browser smoke tests cover desktop/mobile rendering, SSE reconnect and cancellation;
- existing `/healthz`, `/metrics`, `/api/v1/*`, and `/restore` behavior remains available in console mode;
- CLI commands and daemon/systemd workflows remain unchanged;
- the production module graph contains no Bubble Tea/Lip Gloss dependency.

## Rollback

The migration does not change backup formats, manifests, CAS layout, control-plane schema, or GitLab recovery data. If the browser UI must be rolled back, use the unchanged CLI subcommands while reverting the web-console entrypoint. Backup data requires no migration or rollback.
