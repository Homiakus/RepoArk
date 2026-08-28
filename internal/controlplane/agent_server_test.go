package controlplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/replication"
)

func requestAs(method, target, identity string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{Subject: pkix.Name{CommonName: identity}}}}
	r.SetPathValue("id", "")
	return r
}

func TestAgentAuthorizationBoundToLeaseIdentity(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	j, _, err := s.Enqueue(ctx, Job{Kind: "backup-repo", Target: "acme/demo", MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	xs, err := s.Lease(ctx, "worker-a", 1, time.Minute)
	if err != nil || len(xs) != 1 {
		t.Fatal("lease failed")
	}
	srv := AgentServer{Store: s}
	r := requestAs("POST", "https://control/api/v1/agent/jobs/"+j.ID+"/generation", "worker-b")
	r.SetPathValue("id", j.ID)
	if _, _, err := srv.authorizeRunningJob(r); err == nil {
		t.Fatal("different certificate identity must not control lease")
	}
	r = requestAs("POST", "https://control/api/v1/agent/jobs/"+j.ID+"/generation", "worker-a")
	r.SetPathValue("id", j.ID)
	got, id, err := srv.authorizeRunningJob(r)
	if err != nil || id != "worker-a" || got.ID != j.ID {
		t.Fatalf("owner rejected: %v %q %#v", err, id, got)
	}
}

func handlerRequest(method, target, identity string, body any) *http.Request {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, target, bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{Subject: pkix.Name{CommonName: identity}}}}
	return r
}

func TestAgentHandlerRegistersReportsAndPinsFollowupAffinity(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	repo := Repository{ID: "repo-1", Account: "acme", FullName: "acme/demo", Enabled: true, NextRunAt: time.Now().UTC()}
	if err := store.UpsertRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	parent, _, err := store.Enqueue(ctx, Job{Kind: "backup-repo", Target: repo.FullName, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	leased, err := store.Lease(ctx, "worker-a", 1, time.Minute)
	if err != nil || len(leased) != 1 || leased[0].ID != parent.ID {
		t.Fatalf("lease parent: %#v %v", leased, err)
	}
	srv := AgentServer{Store: store, Generations: config.GenerationConfig{KeepPerRepo: 2}}
	h := srv.handler()

	g := Generation{ID: "gen-1", RepositoryID: repo.ID, Repository: repo.FullName, MetaPath: "agent://worker-a/gen-1/generation.json", CreatedAt: time.Now().UTC(), Verified: true}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, handlerRequest(http.MethodPost, "/api/v1/agent/jobs/"+parent.ID+"/generation", "worker-a", g))
	if rr.Code != http.StatusOK {
		t.Fatalf("generation route status=%d body=%s", rr.Code, rr.Body.String())
	}
	gens, err := store.ListGenerations(ctx, repo.ID, 10)
	if err != nil || len(gens) != 1 || gens[0].ID != g.ID {
		t.Fatalf("generation not recorded: %#v %v", gens, err)
	}

	child := Job{Kind: "mirror-gitlab", Target: repo.FullName, Payload: `{"full_name":"acme/demo"}`, MaxAttempts: 3}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, handlerRequest(http.MethodPost, "/api/v1/agent/jobs/"+parent.ID+"/enqueue", "worker-a", child))
	if rr.Code != http.StatusOK {
		t.Fatalf("enqueue route status=%d body=%s", rr.Code, rr.Body.String())
	}
	jobs, err := store.ListJobs(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	var found *Job
	for i := range jobs {
		if jobs[i].Kind == "mirror-gitlab" {
			found = &jobs[i]
			break
		}
	}
	if found == nil || found.Affinity != "worker-a" {
		t.Fatalf("follow-up not pinned to agent: %#v", found)
	}
	if err := store.Complete(ctx, parent.ID, "worker-a"); err != nil {
		t.Fatal(err)
	}
	other, err := store.Lease(ctx, "worker-b", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("worker-b leased worker-a local follow-up: %#v", other)
	}
	own, err := store.Lease(ctx, "worker-a", 10, time.Minute)
	if err != nil || len(own) != 1 || own[0].ID != found.ID {
		t.Fatalf("worker-a did not receive follow-up: %#v %v", own, err)
	}
}

func TestAgentGenerationRejectsRepositoryIDSubstitution(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	for _, repo := range []Repository{
		{ID: "repo-a", Account: "acme", FullName: "acme/a", Enabled: true, NextRunAt: time.Now().UTC()},
		{ID: "repo-b", Account: "acme", FullName: "acme/b", Enabled: true, NextRunAt: time.Now().UTC()},
	} {
		if err := store.UpsertRepository(ctx, repo); err != nil {
			t.Fatal(err)
		}
	}
	parent, _, _ := store.Enqueue(ctx, Job{Kind: "backup-repo", Target: "acme/a"})
	_, _ = store.Lease(ctx, "worker-a", 1, time.Minute)
	srv := AgentServer{Store: store}
	g := Generation{ID: "evil", RepositoryID: "repo-b", Repository: "acme/a", CreatedAt: time.Now().UTC()}
	rr := httptest.NewRecorder()
	srv.handler().ServeHTTP(rr, handlerRequest(http.MethodPost, "/api/v1/agent/jobs/"+parent.ID+"/generation", "worker-a", g))
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected repository binding conflict, got %d: %s", rr.Code, rr.Body.String())
	}
}

func rawAgentRequest(method, target, identity string, body []byte) *http.Request {
	r := httptest.NewRequest(method, target, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/octet-stream")
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{Subject: pkix.Name{CommonName: identity}}}}
	return r
}

func TestReplicationRelayBoundToSourceTargetAndDurableTransfer(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	cfg := config.Default().ControlPlane.Replication
	cfg.Enabled = true
	cfg.SpoolRoot = t.TempDir()
	cfg.TransferTTL = "1h"
	cfg.MaxTransferBytes = 1 << 20

	// Target key is pinned in the agent heartbeat state.
	targetKey := filepath.Join(t.TempDir(), "target-x25519.key")
	pub, err := replication.EnsureKey(targetKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.HeartbeatAgent(ctx, Agent{ID: "worker-b", Name: "worker-b", ReplicationPublicKey: pub}); err != nil {
		t.Fatal(err)
	}

	p := replicateGenerationPayload{TransferID: "relay-1", RepositoryID: "repo-1", Repository: "acme/demo", GenerationID: "gen-1", SourceAgent: "worker-a", TargetAgent: "worker-b", TargetReplicationPubKey: pub}
	pb, _ := json.Marshal(p)
	job, created, err := store.Enqueue(ctx, Job{Kind: "replicate-generation", Target: "gen-1@worker-b", Payload: string(pb), Affinity: "worker-a", MaxAttempts: 3})
	if err != nil || !created {
		t.Fatalf("enqueue: %v %v", created, err)
	}
	leased, err := store.Lease(ctx, "worker-a", 1, time.Minute)
	if err != nil || len(leased) != 1 || leased[0].ID != job.ID {
		t.Fatalf("source lease %#v %v", leased, err)
	}

	srv := AgentServer{Store: store, Worker: config.WorkerConfig{MaxAttempts: 3}, Replication: cfg}
	h := srv.handler()
	cipher := []byte("opaque-encrypted-generation")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, rawAgentRequest(http.MethodPut, "/api/v1/agent/jobs/"+job.ID+"/replication/upload/relay-1", "worker-a", cipher))
	if rr.Code != http.StatusOK {
		t.Fatalf("upload=%d %s", rr.Code, rr.Body.String())
	}
	tr, err := store.GetReplicationTransfer(ctx, "relay-1")
	if err != nil || tr.TargetAgent != "worker-b" || tr.SourceAgent != "worker-a" || tr.Bytes != int64(len(cipher)) || tr.SHA256 == "" {
		t.Fatalf("transfer %#v %v", tr, err)
	}

	// The install job is hard-affined to the destination certificate identity.
	install, err := store.Lease(ctx, "worker-b", 10, time.Minute)
	if err != nil || len(install) != 1 || install[0].Kind != "install-replica" {
		t.Fatalf("install lease %#v %v", install, err)
	}
	bad := httptest.NewRecorder()
	h.ServeHTTP(bad, rawAgentRequest(http.MethodGet, "/api/v1/agent/jobs/"+install[0].ID+"/replication/download/relay-1", "worker-c", nil))
	if bad.Code != http.StatusForbidden {
		t.Fatalf("wrong target downloaded transfer: %d", bad.Code)
	}
	good := httptest.NewRecorder()
	h.ServeHTTP(good, rawAgentRequest(http.MethodGet, "/api/v1/agent/jobs/"+install[0].ID+"/replication/download/relay-1", "worker-b", nil))
	if good.Code != http.StatusOK || !bytes.Equal(good.Body.Bytes(), cipher) {
		t.Fatalf("target download=%d body=%q", good.Code, good.Body.Bytes())
	}
}
