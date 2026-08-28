# Automated recovery drills

`repoark drill [N]` proves that backups can actually be restored.

For a rotating sample of successful repositories it:

1. verifies the detached latest-manifest signature when present;
2. performs a real `repoark restore` into an isolated drill directory;
3. runs `git fsck --full` against the restored repository;
4. compares branch and tag refs with the authoritative mirror when `verify_refs` is enabled;
5. runs `git lfs fsck` when an LFS archive exists and Git LFS is installed;
6. deletes successful drill copies;
7. optionally preserves failed drill copies for forensic analysis.

A deterministic daily rotation is used instead of pure random selection so repeated scheduled drills eventually cover the repository fleet while remaining reproducible for a given day.

Enable automatic drills after successful daemon backup cycles:

```yaml
recovery_drill:
  enabled: true
  sample_size: 3
  verify_refs: true
  keep_on_failure: true
```

## Full GitLab service restore drill

`repoark gitlab drill [ARCHIVE]` validates the GitLab recovery plane itself rather than a single repository. It selects a GitLab recovery export (or uses the supplied archive), verifies that recorded image/version metadata is compatible with the configured pinned GitLab image, extracts the export into an isolated work directory, starts a disposable GitLab container on dedicated ports, restores the application backup, restarts the service, waits for the health endpoint, and runs `gitlab-rake gitlab:check SANITIZE=true`.

The drill never restores over the normal recovery GitLab target. Successful temporary instances are removed automatically. Failed instances can be retained for forensic inspection with `gitlab.restore_drill.keep_on_failure: true`.
