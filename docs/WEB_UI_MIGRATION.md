# RepoArk TUI → Web UI migration

Status: **web console implemented as the primary interactive UI**. The CLI remains the automation and break-glass interface. `repoark tui` is kept temporarily as a compatibility alias that starts the web console.

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
  └── /api/v1/console/*             new interactive-operations API
       ├── state                    capabilities/tools/actions
       ├── session                  local/OIDC session state + CSRF
       ├── job                      active/last operation + log tail
       ├── jobs/{name}              start operation
       └── job/cancel               cooperative cancellation
                │
                ▼
        consoleJobManager
        single active operation
                │
                ▼
 backup / fleet / gitlab / offsite / CAS / policy packages
```

The web layer is an adapter only. It does not duplicate repository backup, verification, GitLab DR, CAS or policy algorithms.

## Interaction model

The console exposes the former TUI actions as explicit cards:

| TUI action | Web operation | Availability |
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

Only one operation can run at a time. This deliberately preserves the TUI execution invariant and avoids concurrent mutation of the same backup/GitLab state. Each job keeps a bounded in-memory log tail and supports cooperative cancellation through `context.Context`.

## Security model

### Local mode

Without `control_plane.web_auth`, `RunConsole` accepts only an explicit loopback listen address (`127.0.0.1`, `::1`, or `localhost`). A configuration such as `0.0.0.0:9787` is rejected rather than silently exposing privileged buttons.

### Authenticated remote mode

When OIDC web auth is enabled, the existing RepoArk encrypted session, role mapping and CSRF validation are reused:

- viewer: read-only console;
- operator: normal/elevated operations;
- admin + step-up: dangerous GitLab deploy/migrate operations;
- every browser mutation validates the session CSRF token.

The existing recovery wizard remains under `/restore` and retains its stronger restore approval and step-up rules.

### HTTP hardening

The console adds `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, `Permissions-Policy`, and a restrictive Content Security Policy. The UI has no CDN/runtime frontend dependencies.

## Migration phases

### Phase 1 — Primary web console — implemented

- `repoark` with no command starts the web console.
- `repoark web` starts the web console explicitly.
- `repoark tui` starts the web console with a deprecation notice.
- Existing CLI subcommands remain unchanged.
- Existing observability, metrics and control-plane endpoints are mounted into the same console server.
- Former TUI operations are available from the browser with job state, log tail and cancellation.
- Responsive layout supports desktop, tablet and narrow mobile screens.

### Phase 2 — Soak period and parity validation

Keep the old `internal/tui` implementation for one compatibility release while validating:

- all TUI actions have equivalent web behavior;
- long backup/drill jobs survive browser refreshes;
- cancel semantics match CLI/TUI context cancellation;
- OIDC operator/admin flows work behind the intended reverse proxy;
- `/healthz`, `/metrics`, control plane and recovery wizard remain backward-compatible.

During this phase, add end-to-end browser tests and audit-record assertions for every web-triggered mutation.

### Phase 3 — Remove terminal UI dependencies

After parity validation:

- delete `internal/tui`;
- remove Bubble Tea and Lip Gloss from `go.mod`/`go.sum`;
- remove the `tui` compatibility alias in the next major/minor breaking window;
- update README screenshots and deployment examples to use `repoark web` / default launch.

CLI commands are **not** removed.

### Phase 4 — Progressive operations UX

Recommended next increments:

- persisted job history instead of only the active/last in-memory job;
- Server-Sent Events for log delivery instead of one-second polling;
- per-operation structured result summaries;
- repository-level backup/verify filters;
- config validation/editor with secrets never returned to the browser;
- audit trail view and immutable drill evidence links;
- control-plane scheduler editor with dry-run/preview;
- browser E2E tests in CI using Playwright.

## Acceptance criteria

- `repoark` starts successfully with the default config and binds only to `127.0.0.1:9787`.
- the dashboard loads without Node.js or external static assets;
- backup/verify can be started from the browser and produce live job state/logs;
- a second operation receives HTTP 409 while one is running;
- cancel propagates through the operation context;
- disabled features cannot be started even by direct API calls;
- no-auth web console refuses non-loopback listen addresses;
- authenticated mutations enforce RepoArk roles and CSRF;
- dangerous remote GitLab operations require admin step-up;
- existing `/healthz`, `/metrics`, `/api/v1/*`, and `/restore` behavior remains available in console mode;
- CLI commands and daemon/systemd workflows remain unchanged.

## Rollback

The migration does not change backup formats, manifests, CAS layout, control-plane schema, or GitLab recovery data. If the browser UI must be rolled back, use the unchanged CLI subcommands while reverting the web-console entrypoint. Backup data requires no migration or rollback.
