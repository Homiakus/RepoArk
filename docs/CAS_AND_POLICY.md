# CAS, RPO and RTO operations

## CAS lifecycle

Normal backup:

```text
logical artifact -> SHA-256 -> CAS blob -> optional hard-link swap
```

Safe maintenance:

```bash
repoark cas verify
repoark cas gc --dry-run
repoark cas gc
```

Do not delete logical backup files merely because a matching CAS object exists. Logical paths are part of the portable DR contract.

## RPO gates

- `max_backup_age`
- `max_recovery_drill_age`
- `max_gitlab_drill_age`
- `max_offsite_age`

They answer: *how stale is the latest proven protection/recovery evidence?*

## RTO gates

- `max_recovery_drill_duration`
- `max_gitlab_drill_duration`

They answer: *how long did the latest successful recovery actually take?*

A healthy backup with an unacceptable restore time is intentionally considered policy-degraded.

## Production sequence

1. backup;
2. verify;
3. CAS verify;
4. off-site sync;
5. repository restore drill;
6. periodically full GitLab drill;
7. policy check;
8. monitor `/healthz` and metrics.
