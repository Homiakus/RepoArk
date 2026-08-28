# Package payload disaster recovery

## Why metadata is insufficient

GitHub's package management APIs expose package/version metadata, but actual binary retrieval follows ecosystem registry protocols. RepoArk therefore treats package metadata and package payloads as two separate backup layers.

## npm

RepoArk queries the package document, resolves the requested version's `dist.tarball`, downloads it with authorization, enforces `max_bytes`, writes a `.tgz` and SHA-256 sidecar.

## NuGet

RepoArk reads the namespace V3 service index, discovers a `PackageBaseAddress` resource and downloads `.nupkg` from the flat-container endpoint.

## Maven

RepoArk invokes `mvn dependency:get` with:

- an isolated local repository;
- a mode-0600 temporary `settings.xml`;
- username/token supplied through environment expansion, not argv;
- `transitive=false` so the requested published payload is archived rather than silently expanding into an unrelated dependency mirror.

## RubyGems

RepoArk invokes `gem fetch` under an isolated temporary HOME with a mode-0600 `.gemrc`. The temporary credential-bearing tree is deleted after use.

## Verification

All materialized payloads receive SHA-256 sidecars. `repoark verify` walks the package tree and verifies every sidecar.

## Operational caveat

Use a GitHub token with the scopes/permissions required by the package registry. Package auth requirements can differ from repository REST permissions. Test package restore independently of Git restore for package-critical systems.
