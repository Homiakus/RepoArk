//go:build integration

package controlplane

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/config"
)

func TestSQLiteStoreIntegration(t *testing.T) {
	ctx := context.Background()
	s, err := OpenStore(config.StoreConfig{Driver: "sqlite", SQLitePath: filepath.Join(t.TempDir(), "repoark.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, created, err := s.Enqueue(ctx, Job{Kind: "backup-repo", Target: "acme/demo", MaxAttempts: 3})
	if err != nil || !created {
		t.Fatalf("enqueue: %v %v", created, err)
	}
	xs, err := s.Lease(ctx, "worker", 1, time.Minute)
	if err != nil || len(xs) != 1 || xs[0].ID != j.ID {
		t.Fatalf("lease: %#v %v", xs, err)
	}
	if err := s.Complete(ctx, j.ID, "worker"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJob(ctx, j.ID)
	if err != nil || got.Status != JobSucceeded {
		t.Fatalf("completed: %#v %v", got, err)
	}

	// Affinity is enforced by SQL leasing, not only by the in-memory test store.
	aff, created, err := s.Enqueue(ctx, Job{Kind: "mirror-gitlab", Target: "acme/affinity", Affinity: "agent-a", MaxAttempts: 2})
	if err != nil || !created {
		t.Fatalf("affinity enqueue: %v %v", created, err)
	}
	other, err := s.Lease(ctx, "agent-b", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range other {
		if x.ID == aff.ID {
			t.Fatal("wrong agent leased affinitized SQL job")
		}
	}
	own, err := s.Lease(ctx, "agent-a", 10, time.Millisecond)
	if err != nil || len(own) == 0 || own[0].ID != aff.ID {
		t.Fatalf("affinity lease: %#v %v", own, err)
	}
	// Simulate a crash on the final attempt and verify the SQL reaper marks it failed.
	if _, err := s.db.ExecContext(ctx, s.q(`UPDATE jobs SET attempts=max_attempts,lease_until=? WHERE id=?`), ts(time.Now().UTC().Add(-time.Second)), aff.ID); err != nil {
		t.Fatal(err)
	}
	reissued, err := s.Lease(ctx, "agent-a", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range reissued {
		if x.ID == aff.ID {
			t.Fatal("expired final SQL lease was reissued")
		}
	}
	terminal, err := s.GetJob(ctx, aff.ID)
	if err != nil || terminal.Status != JobFailed {
		t.Fatalf("expected SQL terminal failure, got %#v %v", terminal, err)
	}
	transfer := ReplicationTransfer{ID: "sql-transfer", GenerationID: "g", RepositoryID: "r", SourceAgent: "a", TargetAgent: "b", State: TransferReady, Bytes: 123, SHA256: "abc", ExpiresAt: time.Now().UTC().Add(-time.Second)}
	if err := s.RecordReplicationTransfer(ctx, transfer); err != nil {
		t.Fatal(err)
	}
	xfers, err := s.ListExpiredReplicationTransfers(ctx, time.Now().UTC(), 10)
	if err != nil || len(xfers) != 1 || xfers[0].ID != transfer.ID {
		t.Fatalf("transfer lease %#v %v", xfers, err)
	}
	if err := s.DeleteReplicationTransfer(ctx, transfer.ID); err != nil {
		t.Fatal(err)
	}
	createdRef, err := s.EnsureObjectRef(ctx, ObjectRef{Digest: strings.Repeat("a", 64), Kind: "erasure-shard", Bytes: 123}, "set:0")
	if err != nil || !createdRef {
		t.Fatalf("ensure object ref: created=%t err=%v", createdRef, err)
	}
	createdRef, err = s.EnsureObjectRef(ctx, ObjectRef{Digest: strings.Repeat("a", 64), Kind: "erasure-shard", Bytes: 123}, "set:0")
	if err != nil || createdRef {
		t.Fatalf("object ref retry must be idempotent: created=%t err=%v", createdRef, err)
	}
	ref, ok, err := s.GetObjectRef(ctx, strings.Repeat("a", 64))
	if err != nil || !ok || ref.RefCount != 1 {
		t.Fatalf("object ref=%+v ok=%t err=%v", ref, ok, err)
	}
	protected, err := s.ProtectedObjectDigests(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := protected[ref.Digest]; !ok {
		t.Fatal("ref-counted object missing from GC roots")
	}
}

func TestPostgresStoreIntegration(t *testing.T) {
	dsn := os.Getenv("REPOARK_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("REPOARK_TEST_DATABASE_URL not set")
	}
	env := "REPOARK_TEST_DATABASE_URL"
	s, err := OpenStore(config.StoreConfig{Driver: "postgres", DSNEnv: env})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	target := "acme/postgres-" + time.Now().UTC().Format("150405.000000000")
	j, created, err := s.Enqueue(ctx, Job{Kind: "backup-repo", Target: target, MaxAttempts: 3})
	if err != nil || !created {
		t.Fatalf("enqueue: %v %v", created, err)
	}
	xs, err := s.Lease(ctx, "pg-worker", 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range xs {
		if x.ID == j.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("leased job %s not found in %#v", j.ID, xs)
	}
	if err := s.Complete(ctx, j.ID, "pg-worker"); err != nil {
		t.Fatal(err)
	}
	transfer := ReplicationTransfer{ID: "pg-transfer-" + time.Now().UTC().Format("150405.000000000"), GenerationID: "g", RepositoryID: "r", SourceAgent: "a", TargetAgent: "b", State: TransferReady, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := s.RecordReplicationTransfer(ctx, transfer); err != nil {
		t.Fatal(err)
	}
	gotTransfer, err := s.GetReplicationTransfer(ctx, transfer.ID)
	if err != nil || gotTransfer.TargetAgent != "b" {
		t.Fatalf("postgres transfer %#v %v", gotTransfer, err)
	}
	if err := s.DeleteReplicationTransfer(ctx, transfer.ID); err != nil {
		t.Fatal(err)
	}
}
