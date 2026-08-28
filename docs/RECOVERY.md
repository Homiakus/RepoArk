# Recovery runbook

## 1. Validate the backup set

After restoring/moving the RepoArk backup tree, first verify the recovery inventory and artifacts:

```bash
repoark keys verify
repoark verify
```

`repoark verify` validates the detached manifest signature when signing is configured/present, Git mirrors, bundles, LFS archive hashes and recorded release-asset hashes.

If a signed manifest fails verification, treat the backup set as untrusted until the cause is understood.

## 2. Recover one repository without GitLab

```bash
repoark restore OWNER/REPO /desired/path
```

RepoArk prefers the portable bundle when available and falls back to the bare mirror for cases such as an empty repository. If an LFS archive exists, its payload is restored into the new repository and Git LFS checkout is attempted when `git-lfs` is installed.

After restore:

```bash
git -C /desired/path fsck --full
git -C /desired/path show-ref
git -C /desired/path lfs fsck   # when applicable
```

Application-specific data and deployment credentials remain a separate recovery concern.

## 3. Prove recovery automatically

```bash
repoark drill 3
```

The drill restores a rotating sample into an isolated directory, checks Git integrity, compares branch/tag refs to the source mirror and validates LFS when possible. Failed drill artifacts can be preserved for investigation.

Use daemon configuration to run this after successful scheduled backups.

## 4. Recover GitHub platform evidence

The `metadata/`, `release-assets/`, `oci/` and `official-exports/` trees are recovery evidence/inputs for platform reconstruction.

They are separate from the Git repository because GitHub-specific objects such as Discussions, release binary assets and Packages are not Git refs.

When available, preserve both RepoArk's continuous mirror set and an official GitHub migration export. They protect different failure modes.

## 5. Rebuild the GitLab DR target from mirrors

If no usable GitLab application backup exists:

1. provision Docker Engine + Compose;
2. restore RepoArk's backup root;
3. deploy a clean configured GitLab instance:

```bash
repoark gitlab deploy
```

4. set `GITLAB_TOKEN` for an account allowed to create/push projects;
5. migrate:

```bash
repoark gitlab migrate
```

RepoArk creates projects and sends Git refs with `git push --mirror`; LFS objects are transferred separately when present.

## 6. Restore a GitLab application backup

When recovering the GitLab service itself:

1. provision a host with compatible Docker/Compose;
2. restore the exported GitLab configuration/secrets and application backup from independent storage;
3. deploy the **matching GitLab edition/version** required by that application backup;
4. restore configuration/secrets before completing application-data recovery where required;
5. perform the GitLab-supported restore procedure;
6. verify repositories, users, permissions, CI/CD integrations and application-level state.

Do not treat a GitLab container image and its data directory on one disk as two independent copies.

## 7. Off-site recovery

### restic

Restore the RepoArk root and GitLab exports with normal restic recovery tooling, then point RepoArk configuration at the recovered backup root and run `repoark verify` before using it.

### rclone/S3/MinIO

RepoArk writes distinct remote prefixes:

```text
<remote>/backups
<remote>/gitlab-exports
```

Recover both where applicable, then verify locally.

## Suggested drill cadence

Choose cadence from actual RPO/RTO and repository criticality rather than relying on a universal interval. A useful baseline is frequent automated repository sample drills plus a less frequent isolated full GitLab restore exercise.

## Known non-recreated state

A Git mirror cannot recreate every GitHub product feature. In particular, intentionally non-exportable secrets remain outside scope. Non-container package payloads require package-format-specific backup/recovery if they are part of the recovery objective.
