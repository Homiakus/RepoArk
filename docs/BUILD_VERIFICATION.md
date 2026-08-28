# RepoArk v0.8 Build Verification

## Verification performed in this environment

The release source tree targets Go 1.25.0. The execution environment used to assemble this artifact has Go 1.23.x and cannot reach `proxy.golang.org`, so a real dependency download/build cannot be truthfully claimed here.

To still verify RepoArk's own code, an isolated test copy was created with API-compatible local stubs for external modules only. Those stubs and `replace` directives are **not present in the release tree**.

The synchronized v0.8 source passed:

```text
go test ./...                              PASS
go vet ./...                               PASS
go test -race ./internal/controlplane      PASS
go test -race ./internal/cas               PASS
go test -race ./internal/cassync           PASS
go test -race ./internal/erasure            PASS
go test -race ./internal/scrub              PASS
go test -race ./internal/storagehealth      PASS
go test -race ./internal/tiering            PASS
go test -race ./internal/replication        PASS
go test -race ./internal/generation         PASS
go test -race ./internal/observability      PASS
go test -race ./internal/webauth            PASS
```

The final release tree is also checked with `gofmt`, source hygiene scans, SHA-256 source inventory, and ZIP integrity verification.

## Database migration verification

The v0.8 SQL migration is additive and advances `repoark_meta.schema_version` to `5`. The migration adds durable object refs/owners/leases, erasure sets/shard copies, and disk telemetry columns while preserving the previous generation/replication/job tables.

CI retains real Go 1.25 SQLite/PostgreSQL integration gates. The PostgreSQL workflow uses PostgreSQL 17 and the real pgx driver.

## Real dependency build gate

On a networked Go 1.25 machine, run:

```bash
go mod tidy
go mod download
go test ./...
go vet ./...
go test -tags=integration ./internal/controlplane
go test -race ./internal/controlplane ./internal/cas ./internal/cassync ./internal/erasure ./internal/scrub ./internal/storagehealth ./internal/tiering ./internal/replication ./internal/generation ./internal/observability ./internal/webauth
go build -trimpath -o repoark ./cmd/repoark
```

The repository CI also contains separate PostgreSQL, distributed-storage, HA chaos, and full disposable GitLab restore-drill workflows.
