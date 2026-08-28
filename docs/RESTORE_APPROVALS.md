# Restore Approval Workflow

RepoArk v0.6 can require a second operator before a point-in-time generation restore is scheduled.

## Configuration

```yaml
control_plane:
  restore_approval:
    enabled: true
    approval_ttl: 30m
    require_distinct_approver: true
    requesters:
      - backup-operator
    approvers:
      - dr-lead
```

Empty requester/approver lists allow any local OS actor for that role. When lists are populated, the current operating-system user must be explicitly present.

## State machine

```text
pending -> approved -> scheduled -> executed
   |          |           |
   +-> expired+<-----------+ (release on enqueue failure only)
```

- `pending`: request exists and is within TTL.
- `approved`: second actor authorized it.
- `scheduled`: RepoArk atomically reserved this approval for one restore job.
- `executed`: the leased restore job completed successfully.
- `expired`: no longer usable.

With `require_distinct_approver=true`, the requester cannot approve their own request.

## CLI

```bash
repoark control restore-request OWNER/REPO GENERATION_ID --target /srv/recovery/case-42
repoark control approvals
repoark control approve REQUEST_ID
repoark control restore-approved REQUEST_ID
```

If HA replication is enabled, `restore-approved` resolves the currently available `ready` storage replica before creating the job.

## Bypass prevention

When the approval gate is enabled, direct generation restore commands refuse to bypass it. The approval is consumed atomically with successful job completion in both the memory test store and SQL store.

## Security boundary

This is a deliberately small **two-person local-operator gate**, not a full identity provider. It uses the OS account name as actor identity. Stronger deployments should combine it with hardened host access, sudo/PAM policy, session recording, an external bastion or an identity-aware control-plane frontend. Future work may add OIDC/LDAP-backed roles without weakening the existing state-machine semantics.
