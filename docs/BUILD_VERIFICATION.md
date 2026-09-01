# RepoArk v0.8 Build Verification

## Verified recovery build

RepoArk targets Go 1.25.0. The recovered source tree has been built and tested on GitHub-hosted runners with the real module graph and the committed `go.sum`; no API-compatible dependency stubs or temporary `replace` directives are involved in the current verification.

The recovered tree has passed the following gates with Go 1.25.0:

```text
go mod download                           PASS
go mod tidy + clean-tree check            PASS
gofmt clean-tree check                    PASS
go vet ./...                              PASS
go test ./...                             PASS
race test suite                           PASS
go build -trimpath ./cmd/repoark          PASS
Linux amd64 cross-build                   PASS
Windows amd64 cross-build                 PASS
SQLite integration                        PASS
PostgreSQL 17 integration                 PASS
distributed-storage repeated race tests   PASS
HA chaos repeated tests                   PASS
full GitLab backup/restore + gitlab:check PASS
source checksum inventory                 CI ENFORCED
```

The general CI verifies that `go mod tidy` does not change `go.mod` or `go.sum`. Normal builds use the committed lock data via `go mod download`; `go mod tidy` is a maintenance/invariance check, not a required mutation before every build.

The same CI regenerates `SOURCE_SHA256SUMS.txt` with `scripts/generate-source-checksums.sh` and fails if the committed inventory differs. The generator hashes all tracked files except `SOURCE_SHA256SUMS.txt` itself, so the inventory can be updated in an inventory-only commit without recursion.

## Database migration verification

The v0.8 SQL migration is additive and advances `repoark_meta.schema_version` to `5`. The migration adds durable object refs/owners/leases, erasure sets/shard copies, and disk telemetry columns while preserving the previous generation/replication/job tables.

The PostgreSQL workflow runs the real pgx-backed integration suite against PostgreSQL 17. SQLite integration remains covered by the same control-plane integration gate.

## Disaster-recovery verification

The repository contains a separate slow GitLab application backup/restore workflow using the pinned `gitlab/gitlab-ce:19.2.4-ce.0` image.

The first recovered run exposed a real integration defect: polling GitLab's `/-/health` endpoint through a Docker-published port returned 404 because GitLab monitoring endpoints are IP-allowlisted and the request arrived from the Docker bridge address. The source and restore-target waits now probe the externally reachable `/users/sign_in` path. A regression test covers the condition where `/-/health` returns 404 while the application is externally ready.

The second recovered run advanced through `gitlab-backup create` and exposed a second real defect: host-side archiving could not read restrictive GitLab bind-mount files such as `gitlab-secrets.json`, SSH host keys, `gitlab.rb`, and the application backup. Backup export now stages `/etc/gitlab` and `/var/opt/gitlab/backups` through `docker cp`, then creates the outer archive from user-owned staging data and forces the archive to mode `0600`. The remote backup path uses the same Docker-mediated model.

The third destructive run, GitHub Actions run `33524464424` on commit `01fdcd2f334658f40812149b85c4f500a7c4b075`, completed successfully. It created a source application backup, started a disposable GitLab with the same pinned image, restored the application backup and configuration, restarted the disposable instance, and ran `gitlab:check SANITIZE=true`. The restore stage reported:

```text
GitLab restore drill passed
backup=1788275963_2026_09_01_19.2.4
duration=6m33s
```

This closes the full GitLab disaster-recovery release gate for the recovered v0.8 tree.

## Reproduction

On a networked Go 1.25 machine:

```bash
go mod download
go test ./...
go vet ./...
go test -tags=integration ./internal/controlplane
go test -race ./internal/controlplane ./internal/cas ./internal/cassync ./internal/erasure ./internal/scrub ./internal/storagehealth ./internal/tiering ./internal/replication ./internal/generation ./internal/observability ./internal/webauth
go build -trimpath -o repoark ./cmd/repoark
bash scripts/generate-source-checksums.sh > /tmp/SOURCE_SHA256SUMS.current.txt
cmp SOURCE_SHA256SUMS.txt /tmp/SOURCE_SHA256SUMS.current.txt
```

For the full GitLab disaster-recovery gate, use the scheduled/manual `GitLab Restore Drill` workflow or run `scripts/integration-gitlab-drill.sh` on a Docker-capable host.
