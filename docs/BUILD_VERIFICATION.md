# RepoArk v0.8 Build Verification

## Verified recovery build

RepoArk targets Go 1.25.0. The recovered source tree has now been built and tested on GitHub-hosted runners with the real module graph and the committed `go.sum`; no API-compatible dependency stubs or temporary `replace` directives are involved in the current verification.

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
```

The general CI also verifies that `go mod tidy` does not change `go.mod` or `go.sum`. Normal builds use the committed lock data via `go mod download`; `go mod tidy` is a maintenance/invariance check, not a required mutation before every build.

## Database migration verification

The v0.8 SQL migration is additive and advances `repoark_meta.schema_version` to `5`. The migration adds durable object refs/owners/leases, erasure sets/shard copies, and disk telemetry columns while preserving the previous generation/replication/job tables.

The PostgreSQL workflow runs the real pgx-backed integration suite against PostgreSQL 17. SQLite integration remains covered by the same control-plane integration gate.

## Disaster-recovery verification

The repository contains a separate slow GitLab application backup/restore workflow using the pinned `gitlab/gitlab-ce:19.2.4-ce.0` image. The first recovered run exposed a real integration defect: polling GitLab's `/-/health` endpoint through a Docker-published port returned 404 because GitLab monitoring endpoints are IP-allowlisted and the request arrived from the Docker bridge address.

The recovery code now probes the external `/users/sign_in` path instead, both when waiting for the source GitLab and when waiting for the disposable restore target. A regression test covers the case where `/-/health` returns 404 while the external sign-in path is ready.

The corrected full disposable GitLab backup → restore → `gitlab:check SANITIZE=true` run is a separate release gate. Do not mark that gate PASS until the workflow itself completes successfully.

## Reproduction

On a networked Go 1.25 machine:

```bash
go mod download
go test ./...
go vet ./...
go test -tags=integration ./internal/controlplane
go test -race ./internal/controlplane ./internal/cas ./internal/cassync ./internal/erasure ./internal/scrub ./internal/storagehealth ./internal/tiering ./internal/replication ./internal/generation ./internal/observability ./internal/webauth
go build -trimpath -o repoark ./cmd/repoark
```

For the full GitLab disaster-recovery gate, use the scheduled/manual `GitLab Restore Drill` workflow or run `scripts/integration-gitlab-drill.sh` on a Docker-capable host.
