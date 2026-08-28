# GitHub platform disaster recovery

RepoArk treats Git data and GitHub platform data differently.

## Authoritative Git layer

Every selected repository is protected by a `--mirror` clone, optional portable bundle, Git LFS payload archive, checksums and `git fsck` verification.

## Platform metadata layer

With `github.metadata: full`, RepoArk additionally captures repository metadata, issues, issue comments, pull requests, pull-request review comments, per-PR reviews, releases, labels, milestones, workflows, deployments, environments, hooks, branches and tags. API permission failures are recorded as metadata warnings instead of invalidating the Git backup.

## Release assets

With `github.release_assets: true`, binary release assets are downloaded under `release-assets/` and indexed by `metadata/<owner>/<repo>/release_assets.json`. Each successfully downloaded asset has a SHA-256 value. `repoark verify` recomputes those hashes.

`github.max_asset_bytes` prevents an unexpectedly large release asset from consuming unlimited storage.

## Discussions

GitHub Discussions are retrieved through GraphQL and written to `discussions.json`, including categories, answers, comments and replies. RepoArk paginates discussion records. Nested comment/reply collections above the configured per-discussion window are explicitly marked as truncated rather than silently claimed as complete.

## GitHub Packages and OCI

Package metadata and versions are stored in `packages.json`. GitHub Packages permissions vary by token type and namespace.

Optional `github.oci_export: true` uses `skopeo` for packages of type `container`. Authentication is passed through a temporary `REGISTRY_AUTH_FILE` with mode 0600, not a command-line credential. OCI archives and SHA-256 sidecars are stored under `oci/`.

Non-container package payloads (npm, Maven, RubyGems, NuGet) are not reconstructed from API metadata; their metadata is still archived.

## Official GitHub migration archives

RepoArk also exposes GitHub's own migration-export API as an independent backup channel:

```bash
repoark github export user
repoark github export org MY_ORG
```

RepoArk never asks GitHub to lock repositories for these backup exports. It waits until the migration state is `exported`, downloads the archive into `official-exports/`, and writes a SHA-256 sidecar.

GitHub's migration API has separate authentication restrictions; in particular, its user/organization migration endpoints do not accept fine-grained PATs. Treat these exports as a complementary platform archive, not as a replacement for the continuously updated Git mirror.

## GitHub Actions artifacts

With `github.actions_artifacts: true`, RepoArk enumerates non-expired repository Actions artifacts, downloads their ZIP payloads under `actions-artifacts/`, records metadata, and writes SHA-256 sidecars. Existing valid payloads are reused instead of downloaded again. `github.max_artifact_bytes` bounds unexpectedly large artifacts.

Artifacts are retention-bound GitHub objects; RepoArk can only preserve an artifact while GitHub still exposes it. A skipped expired artifact is reported as a platform warning rather than silently treated as backed up.

## Projects v2

With `github.projects_v2: true`, RepoArk captures user/organization Projects v2 through GraphQL, including project metadata and paginated project items. Results are stored under `metadata/_account/projects-v2/` by owner. Projects are a platform-state backup and do not change the recoverability status of the underlying Git graph.
