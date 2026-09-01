# Repository recovery status

RepoArk's public `main` branch was imported without several files referenced by the release documentation and `SOURCE_SHA256SUMS.txt`.

## Confirmed missing from the imported tree

- `cmd/repoark/main.go`
- `.github/workflows/ci.yml`
- `.github/workflows/controlplane-postgres.yml`
- `.github/workflows/distributed-storage.yml`
- `.github/workflows/gitlab-restore-drill.yml`
- `.github/workflows/ha-chaos.yml`

The stale `.repoark-bootstrap` marker also remained in the imported tree, indicating that the intended full-tree import did not complete.

## Recovery policy

The files above are being reconstructed from the current implementation, Makefile, integration tests, recovery scripts and release documentation. Reconstructed files are **not claimed to be byte-for-byte identical** to the files whose SHA-256 hashes are recorded in `SOURCE_SHA256SUMS.txt`.

`SOURCE_SHA256SUMS.txt` is retained as historical evidence of the intended source tree until either the original files are recovered or a new verified source inventory is generated after a clean Go 1.25 build and CI pass.

## Recovery gates

A recovered tree is not considered release-ready until all of the following pass with real dependencies:

1. `go mod download`
2. `go test ./...`
3. `go vet ./...`
4. race tests
5. `go build -trimpath ./cmd/repoark`
6. SQLite/PostgreSQL integration tests
7. distributed-storage/HA chaos tests
8. disposable full GitLab backup + restore drill
9. fresh source checksum inventory generated from the verified commit

Do not use the historical checksum file as an assertion that reconstructed files match the original lost bytes.
