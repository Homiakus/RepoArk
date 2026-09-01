# Repository recovery status

RepoArk's public `main` branch was imported without several files referenced by the release documentation and the original source checksum inventory.

## Files missing from the imported tree

The imported tree was missing:

- `cmd/repoark/main.go`
- `.github/workflows/ci.yml`
- `.github/workflows/controlplane-postgres.yml`
- `.github/workflows/distributed-storage.yml`
- `.github/workflows/gitlab-restore-drill.yml`
- `.github/workflows/ha-chaos.yml`

The stale `.repoark-bootstrap` marker also remained, indicating that the intended full-tree import had not completed.

All six missing paths have now been reconstructed from the surviving implementation, Makefile, integration tests, recovery scripts and release documentation. `.repoark-bootstrap` has been removed and a canonical `go.sum` has been generated with Go 1.25 against the real dependency graph.

A complete review of the original checksum inventory found no additional missing v0.8 source paths.

## Provenance policy

The reconstructed files are **not claimed to be byte-for-byte identical** to the lost originals. The old checksum inventory has therefore been retained as `SOURCE_SHA256SUMS.HISTORICAL.txt`; it is evidence of the intended imported tree, not an integrity assertion for the reconstructed files.

A deterministic generator is available at `scripts/generate-source-checksums.sh`. A new `SOURCE_SHA256SUMS.txt` must be generated only after the recovered tree has passed all release gates and after all recovery-related source/documentation changes are complete.

## Recovery gates

The recovery is evaluated against these gates using real dependencies:

1. `go mod download` — **PASS**
2. `go test ./...` — **PASS**
3. `go vet ./...` — **PASS**
4. race tests — **PASS**
5. `go build -trimpath ./cmd/repoark` — **PASS**
6. SQLite/PostgreSQL integration tests — **PASS**
7. distributed-storage/HA chaos tests — **PASS**
8. disposable full GitLab backup + restore drill — **IN PROGRESS after two integration fixes**
9. fresh source checksum inventory from the final verified tree — **PENDING gate 8**

Linux and Windows amd64 cross-builds also pass.

## GitLab restore-drill findings

### 1. Docker-published monitoring endpoint false negative

The first real recovered GitLab drill started a functioning GitLab CE 19.2.4 instance, but the integration harness waited on `/-/health` through a Docker-published port. GitLab returned 404 because monitoring endpoints are IP-allowlisted by default and the request arrived from the Docker bridge rather than container localhost.

The source wait and the disposable restore-target wait now use the external `/users/sign_in` readiness path. A regression test reproduces the important condition: `/-/health` may return 404 while the externally reachable GitLab application is ready.

### 2. Host-side archive permission failure

The second real drill advanced through source GitLab readiness and `gitlab-backup create`, then failed when RepoArk attempted to archive the bind-mounted `config` and `data/backups` paths directly from the host. GitLab correctly stores secrets, SSH host keys and application backups with restrictive root/git ownership, so a normal RepoArk user cannot reliably read those bind mounts.

Backup export now stages `/etc/gitlab` and `/var/opt/gitlab/backups` with `docker cp`, then creates the outer archive from the user-owned staging tree and forces the resulting sensitive archive to mode `0600`. The remote GitLab backup path uses the same Docker-mediated export model instead of host-side reads of the bind mounts.

A new full destructive drill is required to prove the complete application backup, isolated restore and `gitlab:check SANITIZE=true` sequence. This document deliberately does not call the GitLab gate successful until that workflow completes.

Do not use `SOURCE_SHA256SUMS.HISTORICAL.txt` as an assertion that reconstructed files match the original lost bytes.
