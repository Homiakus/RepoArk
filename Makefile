APP := repoark
PKG := ./cmd/repoark

.PHONY: build test test-integration test-race test-chaos fmt vet run clean

build:
	go build -trimpath -ldflags "-s -w" -o $(APP) $(PKG)

test:
	go test ./...

test-integration:
	go test -tags=integration ./internal/controlplane

test-race:
	go test -race ./internal/audit ./internal/backup ./internal/cas ./internal/cassync ./internal/controlplane ./internal/erasure ./internal/generation ./internal/githubapi ./internal/gitlab ./internal/manifest ./internal/objectinventory ./internal/observability ./internal/policy ./internal/replication ./internal/storagehealth ./internal/webauth

test-chaos:
	go test -race -count=20 -run "TestChaos|TestReplicationPlacement|TestReplicationHealth|TestSelectRestoreAffinity|TestExpiredReplicationTransfer" ./internal/controlplane
	go test -race -count=10 ./internal/replication ./internal/cassync ./internal/erasure ./internal/objectinventory ./internal/storagehealth

fmt:
	gofmt -w cmd internal

vet:
	go vet ./...

run:
	go run $(PKG)

clean:
	rm -f repoark repoark.exe
