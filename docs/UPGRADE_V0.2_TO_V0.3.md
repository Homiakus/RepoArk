# Upgrade RepoArk v0.2 -> v0.3

1. Replace the binary/source tree with v0.3.
2. Keep the existing backup root unchanged.
3. Run `repoark doctor`.
4. Run `repoark backup` once. New manifests are written as version 3; old manifests remain readable.
5. Run `repoark verify`.
6. Run `repoark audit verify` after at least one audited command.

Existing v0.2 configuration is accepted. v0.3 adds safe defaults for:

- `github.actions_artifacts`
- `github.projects_v2`
- `github.max_artifact_bytes`
- `fleet`
- `audit`
- `offsite.object_lock`
- `gitlab.preserve_namespaces`
- `gitlab.restore_drill`

## GitLab migration behavior change

The v0.3 default is `gitlab.preserve_namespaces: true`. A GitHub repository `owner/repo` is migrated to GitLab `owner/repo`.

If an existing v0.2 recovery GitLab already uses flattened names and you want to keep them, set:

```yaml
gitlab:
  preserve_namespaces: false
```

before running `repoark gitlab migrate`.

## S3 Object Lock

RepoArk does not create or mutate an Object Lock retention policy automatically. Configure versioning + Object Lock + default retention on the bucket first, then use:

```bash
repoark offsite verify-lock
```

Only after that should immutable replication be enabled.

## GitLab restore drills

Use unused host ports for `gitlab.restore_drill.http_port` and `ssh_port`. The drill is intentionally resource-heavy because it starts a real GitLab instance.
