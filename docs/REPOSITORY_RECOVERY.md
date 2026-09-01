# Repository recovery status

RepoArk's public `main` branch was imported without several files referenced by the release documentation and the original source checksum inventory. The recovery is now functionally complete: the missing paths have been reconstructed, the real dependency graph builds, all fast and integration gates pass, and a full disposable GitLab backup/restore drill has completed successfully.

## Files missing from the imported tree

The imported tree was missing:

- `cmd/repoark/main.go`
- `.github/workflows/ci.yml`
- `.github/workflows/controlplane-postgres.yml`
- `.github/workflows/distributed-storage.yml`
- `.github/workflows/gitlab-restore-drill.yml`
- `.github/workflows/ha-chaos.yml`

The stale `.repoark-bootstrap` marker also remained, indicating that the intended full-tree import had not completed.

All six missing paths have been reconstructed from the surviving implementation, Makefile, integration tests, recovery scripts and release documentation. `.repoark-bootstrap` has been removed and a canonical `go.sum` has been generated with Go 1.25 against the real dependency graph.

A complete review of the original checksum inventory found no additional missing v0.8 source paths.

## Provenance policy

The reconstructed files are **not claimed to be byte-for-byte identical** to the lost originals. The old checksum inventory is retained as `SOURCE_SHA256SUMS.HISTORICAL.txt`; it is evidence of the intended imported tree, not an integrity assertion for the reconstructed files.

The recovered tree uses a new `SOURCE_SHA256SUMS.txt`, generated deterministically by `scripts/generate-source-checksums.sh`. The generator hashes all Git-tracked files except `SOURCE_SHA256SUMS.txt` itself. General CI regenerates the inventory and fails closed if any tracked source, workflow, script, documentation or other tracked file differs from the committed inventory.

## Recovery gates

The recovery has been evaluated with real dependencies and real service instances:

1. `go mod download` — **PASS**
2. `go test ./...` — **PASS**
3. `go vet ./...` — **PASS**
4. race tests — **PASS**
5. `go build -trimpath ./cmd/repoark` — **PASS**
6. SQLite/PostgreSQL integration tests — **PASS**
7. distributed-storage/HA chaos tests — **PASS**
8. disposable full GitLab backup + restore drill — **PASS**
9. fresh source checksum inventory from the final recovered tree — **CI ENFORCED**

Linux and Windows amd64 cross-builds also pass.

## GitLab restore-drill findings

### 1. Docker-published monitoring endpoint false negative

The first real recovered GitLab drill started a functioning GitLab CE 19.2.4 instance, but the integration harness waited on `/-/health` through a Docker-published port. GitLab returned 404 because monitoring endpoints are IP-allowlisted by default and the request arrived from the Docker bridge rather than container localhost.

The source wait and disposable restore-target wait now use the external `/users/sign_in` readiness path. A regression test reproduces the important condition: `/-/health` may return 404 while the externally reachable GitLab application is ready.

### 2. Host-side archive permission failure

The second real drill advanced through source GitLab readiness and `gitlab-backup create`, then failed when RepoArk attempted to archive the bind-mounted `config` and `data/backups` paths directly from the host. GitLab correctly stores secrets, SSH host keys and application backups with restrictive root/git ownership, so a normal RepoArk user cannot reliably read those bind mounts.

Backup export now stages `/etc/gitlab` and `/var/opt/gitlab/backups` with `docker cp`, then creates the outer archive from the user-owned staging tree and forces the resulting sensitive archive to mode `0600`. The remote GitLab backup path uses the same Docker-mediated export model instead of host-side reads of the bind mounts.

### 3. Full destructive restore proof

GitHub Actions run `33524464424`, executed from commit `01fdcd2f334658f40812149b85c4f500a7c4b075`, completed the entire workflow successfully using `gitlab/gitlab-ce:19.2.4-ce.0`:

- source GitLab became externally ready;
- `gitlab-backup create` completed;
- configuration and application backup were exported;
- a disposable GitLab instance was started with the exact configured image;
- backup ownership was prepared and database clients stopped;
- the application backup was restored;
- the disposable GitLab was restarted;
- `gitlab:check SANITIZE=true` completed successfully.

The restore reported backup identifier `1788275963_2026_09_01_19.2.4` and a restore-drill duration of `6m33s`.

## Integrity rule

`SOURCE_SHA256SUMS.HISTORICAL.txt` remains historical evidence only. `SOURCE_SHA256SUMS.txt` is the current integrity inventory for the reconstructed and verified source tree. CI must remain configured to regenerate and compare it on every normal quality run; an inventory mismatch is a release-blocking failure until the changed tree is independently verified and a new inventory-only commit is made.
