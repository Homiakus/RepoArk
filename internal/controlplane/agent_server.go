package controlplane

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/replication"
)

type AgentServer struct {
	Store       Store
	Config      config.AgentConfig
	Worker      config.WorkerConfig
	Generations config.GenerationConfig
	Replication config.ReplicationConfig
	Storage     config.StorageDataConfig
}

type agentHeartbeatRequest struct {
	Labels               map[string]string `json:"labels,omitempty"`
	ReplicationPublicKey string            `json:"replication_public_key,omitempty"`
	StorageHealth        string            `json:"storage_health,omitempty"`
	StorageTotalBytes    int64             `json:"storage_total_bytes,omitempty"`
	StorageFreeBytes     int64             `json:"storage_free_bytes,omitempty"`
	StorageFreePercent   float64           `json:"storage_free_percent,omitempty"`
	StorageProbeMS       int64             `json:"storage_probe_ms,omitempty"`
	StorageError         string            `json:"storage_error,omitempty"`
	DiskRiskScore        int               `json:"disk_risk_score,omitempty"`
	DiskModel            string            `json:"disk_model,omitempty"`
	DiskSerial           string            `json:"disk_serial,omitempty"`
	DiskTemperatureC     float64           `json:"disk_temperature_c,omitempty"`
	DiskPercentageUsed   float64           `json:"disk_percentage_used,omitempty"`
	DiskMediaErrors      int64             `json:"disk_media_errors,omitempty"`
	DiskCriticalWarning  int64             `json:"disk_critical_warning,omitempty"`
	InventoryRoot        string            `json:"inventory_root,omitempty"`
	InventoryObjects     int               `json:"inventory_objects,omitempty"`
	InventoryBytes       int64             `json:"inventory_bytes,omitempty"`
	InventoryJSON        string            `json:"inventory_json,omitempty"`
}
type agentFailRequest struct {
	Error string `json:"error"`
}

func (s AgentServer) Run(ctx context.Context) error {
	if err := InitPKI(s.Config); err != nil {
		return err
	}
	cert, err := tls.LoadX509KeyPair(s.Config.ServerCertPath, s.Config.ServerKeyPath)
	if err != nil {
		return err
	}
	caPEM, err := os.ReadFile(s.Config.CAPath)
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return errors.New("failed to parse agent CA")
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert}
	srv := &http.Server{Addr: s.Config.Listen, Handler: s.handler(), TLSConfig: tlsCfg, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	ln, err := tls.Listen("tcp", s.Config.Listen, tlsCfg)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	err = srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return ctx.Err()
	}
	return err
}

func (s AgentServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/agent/heartbeat", s.heartbeat)
	mux.HandleFunc("POST /api/v1/agent/lease", s.lease)
	mux.HandleFunc("POST /api/v1/agent/jobs/{id}/complete", s.complete)
	mux.HandleFunc("POST /api/v1/agent/jobs/{id}/fail", s.fail)
	mux.HandleFunc("POST /api/v1/agent/jobs/{id}/generation", s.generation)
	mux.HandleFunc("POST /api/v1/agent/jobs/{id}/backup-result", s.backupResult)
	mux.HandleFunc("POST /api/v1/agent/jobs/{id}/enqueue", s.enqueue)
	mux.HandleFunc("POST /api/v1/agent/jobs/{id}/replica", s.replica)
	mux.HandleFunc("POST /api/v1/agent/jobs/{id}/object-ref", s.objectRef)
	mux.HandleFunc("POST /api/v1/agent/jobs/{id}/erasure-set", s.erasureSet)
	mux.HandleFunc("POST /api/v1/agent/jobs/{id}/erasure-shard", s.erasureShard)
	mux.HandleFunc("POST /api/v1/agent/jobs/{id}/corrupt-object", s.corruptObject)
	mux.HandleFunc("PUT /api/v1/agent/jobs/{id}/replication/upload/{transfer}", s.replicationUpload)
	mux.HandleFunc("HEAD /api/v1/agent/jobs/{id}/replication/upload/{transfer}", s.replicationUploadStatus)
	mux.HandleFunc("PATCH /api/v1/agent/jobs/{id}/replication/upload/{transfer}", s.replicationUploadChunk)
	mux.HandleFunc("POST /api/v1/agent/jobs/{id}/replication/upload/{transfer}/finalize", s.replicationUploadFinalize)
	mux.HandleFunc("GET /api/v1/agent/jobs/{id}/replication/download/{transfer}", s.replicationDownload)
	mux.HandleFunc("GET /api/v1/agent/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "identity": agentIdentity(r)})
	})
	return limitBody(mux, 1<<20)
}

func (s AgentServer) heartbeat(w http.ResponseWriter, r *http.Request) {
	id := agentIdentity(r)
	if id == "" {
		http.Error(w, "client certificate identity missing", 401)
		return
	}
	var req agentHeartbeatRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	labels, _ := json.Marshal(req.Labels)
	a := Agent{ID: id, Name: id, CertSubject: r.TLS.PeerCertificates[0].Subject.String(), LabelsJSON: string(labels), ReplicationPublicKey: strings.TrimSpace(req.ReplicationPublicKey), Status: "online", StorageHealth: req.StorageHealth, StorageTotalBytes: req.StorageTotalBytes, StorageFreeBytes: req.StorageFreeBytes, StorageFreePercent: req.StorageFreePercent, StorageProbeMS: req.StorageProbeMS, StorageError: req.StorageError, DiskRiskScore: req.DiskRiskScore, DiskModel: req.DiskModel, DiskSerial: req.DiskSerial, DiskTemperatureC: req.DiskTemperatureC, DiskPercentageUsed: req.DiskPercentageUsed, DiskMediaErrors: req.DiskMediaErrors, DiskCriticalWarning: req.DiskCriticalWarning, InventoryRoot: req.InventoryRoot, InventoryObjects: req.InventoryObjects, InventoryBytes: req.InventoryBytes, InventoryJSON: req.InventoryJSON}
	if err := s.Store.HeartbeatAgent(r.Context(), a); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "agent": id})
}
func (s AgentServer) lease(w http.ResponseWriter, r *http.Request) {
	id := agentIdentity(r)
	if id == "" {
		http.Error(w, "client certificate identity missing", 401)
		return
	}
	lease, _ := time.ParseDuration(s.Worker.Lease)
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	jobs, err := s.Store.Lease(r.Context(), id, 1, lease)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, jobs)
}
func (s AgentServer) complete(w http.ResponseWriter, r *http.Request) {
	j, id, err := s.authorizeRunningJob(r)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	if err := s.Store.Complete(r.Context(), j.ID, id); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if j.Kind == "install-cas" {
		var p installCASPayload
		if json.Unmarshal([]byte(j.Payload), &p) == nil && p.TransferID != "" {
			_ = os.Remove(replicationSpoolPath(s.Replication.SpoolRoot, p.TransferID))
			_ = s.Store.DeleteReplicationTransfer(r.Context(), p.TransferID)
			if p.Erasure != nil && p.Erasure.LeaseID != "" {
				_ = s.Store.ReleaseObjectLease(r.Context(), p.Erasure.LeaseID)
			}
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s AgentServer) fail(w http.ResponseWriter, r *http.Request) {
	j, id, err := s.authorizeRunningJob(r)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	var req agentFailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if len(req.Error) > 4096 {
		req.Error = req.Error[:4096]
	}
	if err := s.Store.Fail(r.Context(), j.ID, id, req.Error, retryDelay(j.Attempts)); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func agentIdentity(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return ""
	}
	c := r.TLS.PeerCertificates[0]
	if len(c.DNSNames) > 0 && strings.TrimSpace(c.DNSNames[0]) != "" {
		return strings.TrimSpace(c.DNSNames[0])
	}
	return strings.TrimSpace(c.Subject.CommonName)
}
func limitBody(next http.Handler, n int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !((r.Method == http.MethodPut || r.Method == http.MethodPatch) && strings.Contains(r.URL.Path, "/replication/upload/")) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
		}
		next.ServeHTTP(w, r)
	})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decodeJSON(resp *http.Response, v any) error {
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("agent API %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if v == nil {
		return nil
	}
	return json.Unmarshal(b, v)
}

func (s AgentServer) authorizeRunningJob(r *http.Request) (Job, string, error) {
	agent := agentIdentity(r)
	if agent == "" {
		return Job{}, "", errors.New("client certificate identity missing")
	}
	j, err := s.Store.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		return Job{}, agent, err
	}
	if j.Status != JobRunning || j.LeaseOwner != agent {
		return Job{}, agent, fmt.Errorf("job is not leased by authenticated agent")
	}
	return j, agent, nil
}
func (s AgentServer) generation(w http.ResponseWriter, r *http.Request) {
	j, _, err := s.authorizeRunningJob(r)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	var g Generation
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if g.Repository != j.Target {
		http.Error(w, "generation repository does not match leased job", 409)
		return
	}
	if err := s.requireRepositoryBinding(r.Context(), g.RepositoryID, j.Target); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if err := s.Store.RecordGeneration(r.Context(), g); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if s.Generations.KeepPerRepo > 0 {
		if err := s.Store.PruneGenerationRecords(r.Context(), g.RepositoryID, s.Generations.KeepPerRepo); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s AgentServer) backupResult(w http.ResponseWriter, r *http.Request) {
	j, _, err := s.authorizeRunningJob(r)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	var req struct {
		RepositoryID string    `json:"repository_id"`
		OK           bool      `json:"ok"`
		GenerationID string    `json:"generation_id"`
		At           time.Time `json:"at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if err := s.requireRepositoryBinding(r.Context(), req.RepositoryID, j.Target); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if req.At.IsZero() {
		req.At = time.Now().UTC()
	}
	if err := s.Store.MarkBackupResult(r.Context(), req.RepositoryID, req.OK, req.GenerationID, req.At); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s AgentServer) replica(w http.ResponseWriter, r *http.Request) {
	j, agent, err := s.authorizeRunningJob(r)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	var rp GenerationReplica
	if err := json.NewDecoder(r.Body).Decode(&rp); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	switch j.Kind {
	case "backup-repo":
		if err := s.requireRepositoryBinding(r.Context(), rp.RepositoryID, j.Target); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
	case "install-replica":
		var p installReplicaPayload
		if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
			http.Error(w, "invalid install job", 500)
			return
		}
		if p.TargetAgent != agent || rp.GenerationID != p.GenerationID || rp.RepositoryID != p.RepositoryID {
			http.Error(w, "replica does not match install job", 409)
			return
		}
	default:
		http.Error(w, "job kind may not report replicas", 403)
		return
	}
	rp.AgentID = agent
	if rp.MetaPath != "" && !strings.HasPrefix(rp.MetaPath, "agent://") {
		rp.MetaPath = "agent://" + agent + "/" + strings.TrimLeft(strings.ReplaceAll(rp.MetaPath, "\\", "/"), "/")
	}
	if rp.State == "" {
		rp.State = ReplicaReady
	}
	if rp.VerifiedAt.IsZero() {
		rp.VerifiedAt = time.Now().UTC()
	}
	if err := s.Store.RecordReplica(r.Context(), rp); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if j.Kind == "install-replica" {
		var p installReplicaPayload
		_ = json.Unmarshal([]byte(j.Payload), &p)
		_ = os.Remove(replicationSpoolPath(s.Replication.SpoolRoot, p.TransferID))
		_ = s.Store.DeleteReplicationTransfer(r.Context(), p.TransferID)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

type agentObjectRefRequest struct {
	Ref    ObjectRef `json:"ref"`
	Delta  int64     `json:"delta"`
	Owner  string    `json:"owner,omitempty"`
	Ensure bool      `json:"ensure,omitempty"`
}

func (s AgentServer) objectRef(w http.ResponseWriter, r *http.Request) {
	j, id, err := s.authorizeRunningJob(r)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	if j.Kind != "install-cas" && j.Kind != "protect-erasure" && j.Kind != "repair-object" {
		http.Error(w, "job may not mutate object refs", 403)
		return
	}
	var req agentObjectRefRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if req.Ref.Digest == "" || req.Delta < -1 || req.Delta > 1 {
		http.Error(w, "invalid object ref mutation", 400)
		return
	}
	if req.Ensure {
		if strings.TrimSpace(req.Owner) == "" {
			http.Error(w, "object ref owner required", 400)
			return
		}
		created, err := s.Store.EnsureObjectRef(r.Context(), req.Ref, req.Owner)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		writeJSON(w, 200, map[string]any{"created": created})
		return
	}
	out, err := s.Store.AdjustObjectRef(r.Context(), req.Ref, req.Delta)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	_ = id
	writeJSON(w, 200, out)
}

func (s AgentServer) erasureSet(w http.ResponseWriter, r *http.Request) {
	j, _, err := s.authorizeRunningJob(r)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	if j.Kind != "install-cas" && j.Kind != "protect-erasure" && j.Kind != "repair-object" {
		http.Error(w, "job may not mutate erasure sets", 403)
		return
	}
	var e ErasureSet
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if err := s.Store.RecordErasureSet(r.Context(), e); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s AgentServer) erasureShard(w http.ResponseWriter, r *http.Request) {
	j, id, err := s.authorizeRunningJob(r)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	if j.Kind != "install-cas" && j.Kind != "protect-erasure" && j.Kind != "repair-object" {
		http.Error(w, "job may not mutate erasure shards", 403)
		return
	}
	var sh ErasureShard
	if err := json.NewDecoder(r.Body).Decode(&sh); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	// A remote agent can only report a copy it owns. Identity and failure
	// domain are derived from mTLS + the server-side heartbeat record, not
	// trusted from the request body.
	sh.AgentID = id
	if a, err := s.Store.GetAgent(r.Context(), id); err == nil {
		sh.FailureDomain = agentFailureDomain(a, s.Storage.Erasure.FailureDomainLabel)
	}
	if err := s.Store.RecordErasureShard(r.Context(), sh); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s AgentServer) corruptObject(w http.ResponseWriter, r *http.Request) {
	j, id, err := s.authorizeRunningJob(r)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	if j.Kind != "scrub-cas" {
		http.Error(w, "job may not report object corruption", 403)
		return
	}
	var req struct {
		Digest string `json:"digest"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || len(strings.TrimSpace(req.Digest)) != 64 {
		http.Error(w, "invalid digest", 400)
		return
	}
	n, err := ScheduleDistributedRepair(r.Context(), s.Store, config.Config{ControlPlane: config.ControlPlaneConfig{Workers: s.Worker, Replication: s.Replication, Storage: s.Storage}}, id, strings.ToLower(strings.TrimSpace(req.Digest)))
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	writeJSON(w, 200, map[string]any{"queued": n})
}

func (s AgentServer) replicationUpload(w http.ResponseWriter, r *http.Request) {
	j, agent, err := s.authorizeRunningJob(r)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	if j.Kind != "replicate-generation" {
		http.Error(w, "job is not replication source", 403)
		return
	}
	var p replicateGenerationPayload
	if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
		http.Error(w, "invalid replication job", 500)
		return
	}
	transfer := r.PathValue("transfer")
	if transfer == "" || transfer != p.TransferID || p.SourceAgent != agent {
		http.Error(w, "replication transfer identity mismatch", 409)
		return
	}
	if p.TargetAgent == LocalWorkerAffinity {
		pub, keyErr := replication.EnsureKey(s.Replication.LocalKeyPath)
		if keyErr != nil || pub != p.TargetReplicationPubKey {
			http.Error(w, "target replication key is not current", 409)
			return
		}
	} else {
		target, keyErr := s.Store.GetAgent(r.Context(), p.TargetAgent)
		if keyErr != nil || strings.TrimSpace(target.ReplicationPublicKey) == "" || target.ReplicationPublicKey != p.TargetReplicationPubKey {
			http.Error(w, "target replication key is not current", 409)
			return
		}
	}
	if err := os.MkdirAll(s.Replication.SpoolRoot, 0o700); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	spool := replicationSpoolPath(s.Replication.SpoolRoot, transfer)
	part := spool + ".part"
	out, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h := sha256.New()
	max := s.Replication.MaxTransferBytes
	if max <= 0 {
		max = 20 << 30
	}
	n, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(r.Body, max+1))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(part)
		http.Error(w, copyErr.Error(), 400)
		return
	}
	if closeErr != nil {
		_ = os.Remove(part)
		http.Error(w, closeErr.Error(), 500)
		return
	}
	if n > max {
		_ = os.Remove(part)
		http.Error(w, "replication transfer exceeds maximum", http.StatusRequestEntityTooLarge)
		return
	}
	if err := os.Rename(part, spool); err != nil {
		_ = os.Remove(part)
		http.Error(w, err.Error(), 500)
		return
	}
	install := installReplicaPayload{TransferID: transfer, RepositoryID: p.RepositoryID, Repository: p.Repository, GenerationID: p.GenerationID, SourceAgent: agent, TargetAgent: p.TargetAgent, CipherSHA256: hex.EncodeToString(h.Sum(nil)), CipherBytes: n}
	lease := ReplicationTransfer{ID: transfer, GenerationID: p.GenerationID, RepositoryID: p.RepositoryID, SourceAgent: agent, TargetAgent: p.TargetAgent, SpoolPath: spool, State: TransferReady, Bytes: n, SHA256: install.CipherSHA256, ExpiresAt: replicationTransferExpiry(s.Replication, time.Now().UTC())}
	if err := s.Store.RecordReplicationTransfer(r.Context(), lease); err != nil {
		_ = os.Remove(spool)
		http.Error(w, err.Error(), 500)
		return
	}
	b, _ := json.Marshal(install)
	_, _, err = s.Store.Enqueue(r.Context(), Job{Kind: "install-replica", Target: p.GenerationID + "@" + p.TargetAgent, Payload: string(b), Affinity: p.TargetAgent, Priority: j.Priority, MaxAttempts: s.Worker.MaxAttempts, NotBefore: time.Now().UTC()})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, install)
}

func (s AgentServer) replicationUploadStatus(w http.ResponseWriter, r *http.Request) {
	j, agent, err := s.authorizeRunningJob(r)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	p, err := decodeTransferSourceJob(j, agent, r.PathValue("transfer"))
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	part := replicationSpoolPath(s.Replication.SpoolRoot, p.TransferID) + ".part"
	var size int64
	if info, e := os.Stat(part); e == nil {
		size = info.Size()
	} else if !os.IsNotExist(e) {
		http.Error(w, e.Error(), 500)
		return
	}
	if t, e := s.Store.GetReplicationTransfer(r.Context(), p.TransferID); e == nil {
		if t.SourceAgent != agent || t.TargetAgent != p.TargetAgent || t.GenerationID != p.GenerationID {
			http.Error(w, "replication transfer metadata mismatch", 409)
			return
		}
		if t.State == TransferReady {
			w.Header().Set("X-RepoArk-Offset", fmt.Sprintf("%d", t.Bytes))
			w.Header().Set("X-RepoArk-State", TransferReady)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	w.Header().Set("X-RepoArk-Offset", fmt.Sprintf("%d", size))
	w.Header().Set("X-RepoArk-State", TransferReceiving)
	w.WriteHeader(http.StatusNoContent)
}

func (s AgentServer) replicationUploadChunk(w http.ResponseWriter, r *http.Request) {
	j, agent, err := s.authorizeRunningJob(r)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	p, err := decodeTransferSourceJob(j, agent, r.PathValue("transfer"))
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if err := s.validateTransferTargetKey(r.Context(), p); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if err := os.MkdirAll(s.Replication.SpoolRoot, 0o700); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	wantOffset, err := strconv.ParseInt(strings.TrimSpace(r.Header.Get("X-RepoArk-Offset")), 10, 64)
	if err != nil || wantOffset < 0 {
		http.Error(w, "invalid X-RepoArk-Offset", 400)
		return
	}
	wantHash := strings.ToLower(strings.TrimSpace(r.Header.Get("X-RepoArk-Chunk-SHA256")))
	if len(wantHash) != 64 {
		http.Error(w, "X-RepoArk-Chunk-SHA256 required", 400)
		return
	}
	spool := replicationSpoolPath(s.Replication.SpoolRoot, p.TransferID)
	part := spool + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if info.Size() != wantOffset {
		w.Header().Set("X-RepoArk-Offset", fmt.Sprintf("%d", info.Size()))
		http.Error(w, "upload offset mismatch", http.StatusConflict)
		return
	}
	chunkMax := s.Storage.ChunkBytes
	if chunkMax <= 0 {
		chunkMax = 8 << 20
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r.Body, chunkMax+1))
	if err != nil {
		_ = f.Truncate(wantOffset)
		http.Error(w, err.Error(), 400)
		return
	}
	if n > chunkMax {
		_ = f.Truncate(wantOffset)
		http.Error(w, "replication chunk too large", http.StatusRequestEntityTooLarge)
		return
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHash {
		_ = f.Truncate(wantOffset)
		http.Error(w, "replication chunk hash mismatch", 422)
		return
	}
	if max := s.Replication.MaxTransferBytes; max > 0 && wantOffset+n > max {
		_ = f.Truncate(wantOffset)
		http.Error(w, "replication transfer exceeds maximum", http.StatusRequestEntityTooLarge)
		return
	}
	if err := f.Sync(); err != nil {
		_ = f.Truncate(wantOffset)
		http.Error(w, err.Error(), 500)
		return
	}
	t := ReplicationTransfer{ID: p.TransferID, GenerationID: p.GenerationID, RepositoryID: p.RepositoryID, SourceAgent: agent, TargetAgent: p.TargetAgent, SpoolPath: part, State: TransferReceiving, Bytes: wantOffset + n, ExpiresAt: replicationTransferExpiry(s.Replication, time.Now().UTC())}
	if err := s.Store.RecordReplicationTransfer(r.Context(), t); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("X-RepoArk-Offset", fmt.Sprintf("%d", wantOffset+n))
	writeJSON(w, 200, map[string]any{"ok": true, "offset": wantOffset + n})
}

type replicationFinalizeRequest struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

func (s AgentServer) replicationUploadFinalize(w http.ResponseWriter, r *http.Request) {
	j, agent, err := s.authorizeRunningJob(r)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	p, err := decodeTransferSourceJob(j, agent, r.PathValue("transfer"))
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if err := s.validateTransferTargetKey(r.Context(), p); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	var req replicationFinalizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Bytes < 0 || len(strings.TrimSpace(req.SHA256)) != 64 {
		http.Error(w, "invalid finalize request", 400)
		return
	}
	spool := replicationSpoolPath(s.Replication.SpoolRoot, p.TransferID)
	part := spool + ".part"
	// Idempotent finalize: a lost HTTP response after durable rename must not
	// force the source to retransmit a multi-gigabyte object/generation.
	if existing, e := s.Store.GetReplicationTransfer(r.Context(), p.TransferID); e == nil && existing.State == TransferReady {
		if existing.SourceAgent != agent || existing.TargetAgent != p.TargetAgent || existing.GenerationID != p.GenerationID || existing.RepositoryID != p.RepositoryID || existing.Bytes != req.Bytes || !strings.EqualFold(existing.SHA256, req.SHA256) {
			http.Error(w, "replication transfer metadata mismatch", 409)
			return
		}
		out, err := s.enqueueTransferInstall(r.Context(), j, p, strings.ToLower(req.SHA256), req.Bytes, false)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, 200, out)
		return
	}
	got, n, err := hashFileSHA256(part)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	if n != req.Bytes || !strings.EqualFold(got, req.SHA256) {
		http.Error(w, "replication final checksum/size mismatch", 422)
		return
	}
	if err := os.Rename(part, spool); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	out, err := s.enqueueTransferInstall(r.Context(), j, p, strings.ToLower(req.SHA256), n, true)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, out)
}

func (s AgentServer) enqueueTransferInstall(ctx context.Context, sourceJob Job, p transferSourceMeta, sha string, n int64, record bool) (any, error) {
	if record {
		lease := ReplicationTransfer{ID: p.TransferID, GenerationID: p.GenerationID, RepositoryID: p.RepositoryID, SourceAgent: p.SourceAgent, TargetAgent: p.TargetAgent, SpoolPath: replicationSpoolPath(s.Replication.SpoolRoot, p.TransferID), State: TransferReady, Bytes: n, SHA256: sha, ExpiresAt: replicationTransferExpiry(s.Replication, time.Now().UTC())}
		if err := s.Store.RecordReplicationTransfer(ctx, lease); err != nil {
			return nil, err
		}
	}
	var kind, target string
	var payload any
	if p.Kind == "replicate-cas" {
		kind = "install-cas"
		target = "cas@" + p.TargetAgent
		payload = installCASPayload{TransferID: p.TransferID, SourceAgent: p.SourceAgent, TargetAgent: p.TargetAgent, CipherSHA256: sha, CipherBytes: n, Erasure: p.CAS.Erasure}
	} else {
		kind = "install-replica"
		target = p.GenerationID + "@" + p.TargetAgent
		payload = installReplicaPayload{TransferID: p.TransferID, RepositoryID: p.RepositoryID, Repository: p.Repository, GenerationID: p.GenerationID, SourceAgent: p.SourceAgent, TargetAgent: p.TargetAgent, CipherSHA256: sha, CipherBytes: n}
	}
	b, _ := json.Marshal(payload)
	if _, _, err := s.Store.Enqueue(ctx, Job{Kind: kind, Target: target + "@" + p.TransferID, Payload: string(b), Affinity: p.TargetAgent, Priority: sourceJob.Priority, MaxAttempts: s.Worker.MaxAttempts, NotBefore: time.Now().UTC()}); err != nil {
		return nil, err
	}
	return payload, nil
}

func decodeReplicationSourceJob(j Job, agent, transfer string) (replicateGenerationPayload, error) {
	if j.Kind != "replicate-generation" {
		return replicateGenerationPayload{}, errors.New("job is not replication source")
	}
	var p replicateGenerationPayload
	if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
		return p, errors.New("invalid replication job")
	}
	if transfer == "" || transfer != p.TransferID || p.SourceAgent != agent {
		return p, errors.New("replication transfer identity mismatch")
	}
	return p, nil
}

func (s AgentServer) validateTransferTargetKey(ctx context.Context, p transferSourceMeta) error {
	if p.TargetAgent == LocalWorkerAffinity {
		pub, err := replication.EnsureKey(s.Replication.LocalKeyPath)
		if err != nil || pub != p.TargetReplicationPubKey {
			return errors.New("target replication key is not current")
		}
		return nil
	}
	target, err := s.Store.GetAgent(ctx, p.TargetAgent)
	if err != nil || strings.TrimSpace(target.ReplicationPublicKey) == "" || target.ReplicationPublicKey != p.TargetReplicationPubKey {
		return errors.New("target replication key is not current")
	}
	return nil
}

func (s AgentServer) validateReplicationTargetKey(ctx context.Context, p replicateGenerationPayload) error {
	if p.TargetAgent == LocalWorkerAffinity {
		pub, err := replication.EnsureKey(s.Replication.LocalKeyPath)
		if err != nil || pub != p.TargetReplicationPubKey {
			return errors.New("target replication key is not current")
		}
		return nil
	}
	target, err := s.Store.GetAgent(ctx, p.TargetAgent)
	if err != nil || strings.TrimSpace(target.ReplicationPublicKey) == "" || target.ReplicationPublicKey != p.TargetReplicationPubKey {
		return errors.New("target replication key is not current")
	}
	return nil
}

func hashFileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func (s AgentServer) replicationDownload(w http.ResponseWriter, r *http.Request) {
	j, agent, err := s.authorizeRunningJob(r)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	transfer := r.PathValue("transfer")
	var target, generationID, repositoryID, sha string
	var bytesN int64
	switch j.Kind {
	case "install-replica":
		var p installReplicaPayload
		if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
			http.Error(w, "invalid install job", 500)
			return
		}
		if transfer == "" || transfer != p.TransferID || p.TargetAgent != agent {
			http.Error(w, "replication transfer identity mismatch", 409)
			return
		}
		target, generationID, repositoryID, sha, bytesN = p.TargetAgent, p.GenerationID, p.RepositoryID, p.CipherSHA256, p.CipherBytes
	case "install-cas":
		var p installCASPayload
		if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
			http.Error(w, "invalid CAS install job", 500)
			return
		}
		if transfer == "" || transfer != p.TransferID || p.TargetAgent != agent {
			http.Error(w, "CAS transfer identity mismatch", 409)
			return
		}
		target, generationID, repositoryID, sha, bytesN = p.TargetAgent, "cas:"+p.SourceAgent+">"+p.TargetAgent, "__cas__", p.CipherSHA256, p.CipherBytes
	default:
		http.Error(w, "job is not transfer installation", 403)
		return
	}
	t, err := s.Store.GetReplicationTransfer(r.Context(), transfer)
	if err != nil {
		http.Error(w, "replication transfer unavailable", 404)
		return
	}
	if !t.ExpiresAt.IsZero() && !t.ExpiresAt.After(time.Now().UTC()) {
		http.Error(w, "replication transfer expired", http.StatusGone)
		return
	}
	if t.TargetAgent != target || t.GenerationID != generationID || t.RepositoryID != repositoryID || t.SHA256 != sha || t.Bytes != bytesN {
		http.Error(w, "replication transfer metadata mismatch", 409)
		return
	}
	path := replicationSpoolPath(s.Replication.SpoolRoot, transfer)
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "replication blob unavailable", 404)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if bytesN > 0 && info.Size() != bytesN {
		http.Error(w, "replication blob size mismatch", 409)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-RepoArk-Cipher-SHA256", sha)
	http.ServeContent(w, r, transfer+".blob", info.ModTime(), f)
}

func (s AgentServer) requireRepositoryBinding(ctx context.Context, repositoryID, target string) error {
	repos, err := s.Store.ListRepositories(ctx)
	if err != nil {
		return err
	}
	for _, repo := range repos {
		if repo.ID == repositoryID && repo.FullName == target {
			return nil
		}
	}
	return errors.New("repository id does not match leased job")
}

func (s AgentServer) enqueue(w http.ResponseWriter, r *http.Request) {
	j, agent, err := s.authorizeRunningJob(r)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	var child Job
	if err := json.NewDecoder(r.Body).Decode(&child); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if child.Target != j.Target {
		http.Error(w, "child job target must match parent job", 409)
		return
	}
	if child.Kind != "mirror-gitlab" {
		http.Error(w, "remote agents may only enqueue mirror-gitlab follow-up jobs", 403)
		return
	}
	// The follow-up payload references the agent's local mirror/LFS paths. It must
	// execute on the same certificate identity unless those paths are on shared storage.
	child.Affinity = agent
	createdJob, created, err := s.Store.Enqueue(r.Context(), child)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"job": createdJob, "created": created})
}
