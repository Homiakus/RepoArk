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

### Phase 2 — Soak period and parity validation — in progress

Keep the old `internal/tui` implementation for one compatibility release while validating:

- all TUI actions have equivalent web behavior;
- long backup/drill jobs survive browser refreshes;
- cancel semantics match CLI/TUI context cancellation;
- OIDC operator/admin flows work behind the intended reverse proxy;
- `/healthz`, `/metrics`, control plane and recovery wizard remain backward-compatible.

Hardening already completed in this phase:

- browser mutation audit records with actor/request correlation;
- fail-closed behavior when required audit storage is unavailable;
- HTTP regression tests for operation completion, rejection and cancellation;
- local-origin/DNS-rebinding regression coverage;
- SSE live job/log delivery with reconnect-safe full snapshots and polling fallback;
- race coverage for the job/audit/event paths;
- a real-browser Chromium CI gate that launches the compiled `repoark web` binary and verifies startup, routing, security headers, local session rendering, operation cards, SSE transport, polling suppression and a narrow mobile viewport;
- an authenticated Chromium path behind a disposable HTTPS reverse proxy and OIDC provider, covering PKCE, signed ID tokens, viewer/operator/admin RBAC, CSRF, Secure/SameSite session cookies and WebAuthn-style step-up AMR/ACR;
- failure diagnostics preserve RepoArk/auth-harness server logs and browser screenshots without adding Node.js to the runtime distribution.

Remaining soak work should focus on long-duration refresh/reconnect scenarios and production-like execution of elevated/danger operation flows with disposable backends.

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
- a second operation receives HTTP 409 while one is running;
- cancel propagates through the operation context;
- disabled features cannot be started even by direct API calls;
- no-auth web console refuses non-loopback listen addresses;
- authenticated mutations enforce RepoArk roles and CSRF;
- dangerous remote GitLab operations require admin step-up;
- OIDC browser CI proves Authorization Code + PKCE, signed ID-token verification, Secure/SameSite session cookies and viewer/operator/admin role mapping through an HTTPS reverse proxy;
- an admin session without the configured step-up AMR is rejected, while a fresh step-up session carrying the required WebAuthn AMR passes the authorization boundary;
- recognized web mutations are represented in the tamper-evident audit ledger;
- `audit.required` blocks mutation when the audit ledger is unavailable;
- a CI browser smoke test starts the compiled binary and checks desktop/mobile rendering plus the live SSE path;
- existing `/healthz`, `/metrics`, `/api/v1/*`, and `/restore` behavior remains available in console mode;
- CLI commands and daemon/systemd workflows remain unchanged.

## Rollback

The migration does not change backup formats, manifests, CAS layout, control-plane schema, or GitLab recovery data. If the browser UI must be rolled back, use the unchanged CLI subcommands while reverting the web-console entrypoint. Backup data requires no migration or rollback.
