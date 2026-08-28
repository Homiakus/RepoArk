# RepoArk v0.7 Web Recovery

## Threat model

The browser recovery UI can expose historic source code and initiate destructive/sensitive recovery operations. It is therefore disabled by default and does not implement a local password or WebAuthn credential database.

RepoArk delegates primary authentication and WebAuthn/MFA ceremony to an OIDC provider, then enforces local authorization and recovery workflow rules.

## OIDC flow

RepoArk uses:

- Authorization Code flow;
- PKCE S256;
- random `state`;
- random OIDC `nonce`;
- ID-token signature/issuer/audience/expiry verification through the OIDC provider metadata/JWK set;
- configurable group claim;
- encrypted local session cookie;
- HttpOnly + SameSite cookie flags;
- configurable `Secure` cookie requirement;
- per-session CSRF token on every POST mutation.

The RepoArk session expires no later than the verified OIDC ID token and is additionally capped at eight hours.

OIDC client secret and session-sealing secret (at least 32 bytes of high-entropy material) are read only from environment variable names in configuration:

```text
REPOARK_OIDC_CLIENT_SECRET
REPOARK_SESSION_KEY
```

## RBAC

Groups map to the highest matching role:

- `viewer` — browse repository/generation recovery inventory;
- `operator` — create restore requests;
- `admin` — approve and schedule recovery.

Example:

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
    secure_cookies: true
```

At least one role group must be configured; RepoArk rejects a web-auth configuration that would authenticate identities but authorize nobody.

## Step-up / WebAuthn

RepoArk does not need to know how the IdP performed WebAuthn. For critical actions it can require evidence in the OIDC `amr` claim:

```yaml
required_amr: [webauthn]
```

or an IdP-specific value such as `mfa`. The recovery page exposes a **Step-up sign in** action that starts a fresh IdP login with `prompt=login` and optional configured `step_up_acr_values`. Every configured value must be present in the sealed identity for step-up-protected operations.

The IdP is responsible for WebAuthn credential enrollment, phishing-resistant policy and reauthentication behavior. Configure the IdP so the returned `amr` accurately reflects the assurance you intend to require.

## Restore workflow

When `restore_approval.requesters` / `approvers` are non-empty, browser identities must additionally match an OIDC subject or email in the corresponding allowlist.

With two-person restore approval enabled:

```text
viewer:   inspect repository/generation
operator: request restore
admin + required AMR: approve
admin + required AMR: schedule
worker:   restore only on a node with a verified ready generation
store:    scheduled -> executed only after successful job completion
```

Without the two-person approval feature, the operator can schedule directly only after satisfying the configured step-up AMR.

## Destination-path safety

The browser never accepts an arbitrary filesystem restore target. It stages the recovery beneath the executing worker's configured:

```yaml
control_plane:
  generations:
    restore_root: ~/.repoark/recovery
```

An administrator who intentionally needs a custom target can still use the explicit CLI recovery command under host-level access controls.

## Routes

When `control_plane.enabled`, observability and web auth are enabled:

```text
GET  /auth/login
GET  /auth/callback
GET  /auth/step-up     # fresh IdP login; optional acr_values
POST /auth/logout
GET  /restore
POST /restore/request
POST /restore/approve
POST /restore/schedule
```

The ordinary observability API remains separate from the mTLS agent listener.
