package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Homiakus/repoark/internal/audit"
	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/controlplane"
	"github.com/Homiakus/repoark/internal/fleet"
	"github.com/Homiakus/repoark/internal/manifest"
	"github.com/Homiakus/repoark/internal/policy"
	"github.com/Homiakus/repoark/internal/webauth"
)

type Server struct {
	cfg     config.Config
	started time.Time
	http    *http.Server
	control controlplane.Store
	auth    *webauth.Manager
}

func New(cfg config.Config) *Server { return &Server{cfg: cfg, started: time.Now()} }

func (s *Server) Run(ctx context.Context) error {
	if s.cfg.ControlPlane.Enabled {
		st, err := controlplane.OpenStore(s.cfg.ControlPlane.Store)
		if err != nil {
			return fmt.Errorf("open control-plane store: %w", err)
		}
		s.control = st
		defer st.Close()
	}
	if s.cfg.ControlPlane.WebAuth.Enabled {
		if s.control == nil {
			return fmt.Errorf("control_plane.web_auth requires control_plane.enabled")
		}
		auth, err := webauth.New(ctx, s.cfg.ControlPlane.WebAuth)
		if err != nil {
			return fmt.Errorf("initialize web auth: %w", err)
		}
		s.auth = auth
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.health)
	mux.HandleFunc("/metrics", s.metrics)
	mux.HandleFunc("/api/v1/status", s.status)
	mux.HandleFunc("/api/v1/fleet", s.fleetStatus)
	mux.HandleFunc("/api/v1/policy", s.policyStatus)
	mux.HandleFunc("/api/v1/control/stats", s.controlStats)
	mux.HandleFunc("/api/v1/control/jobs", s.controlJobs)
	mux.HandleFunc("/api/v1/control/repositories", s.controlRepositories)
	mux.HandleFunc("/api/v1/control/agents", s.controlAgents)
	mux.HandleFunc("/api/v1/control/inventories", s.controlInventories)
	mux.HandleFunc("/api/v1/control/replicas", s.controlReplicas)
	mux.HandleFunc("/api/v1/control/erasure", s.controlErasure)
	mux.HandleFunc("/api/v1/control/approvals", s.controlApprovals)
	if s.auth != nil {
		mux.HandleFunc("GET /auth/login", s.authLogin)
		mux.HandleFunc("GET /auth/callback", s.authCallback)
		mux.HandleFunc("GET /auth/step-up", s.authStepUp)
		mux.HandleFunc("POST /auth/logout", s.authLogout)
		mux.HandleFunc("GET /restore", s.restoreWizard)
		mux.HandleFunc("POST /restore/request", s.restoreRequest)
		mux.HandleFunc("POST /restore/approve", s.restoreApprove)
		mux.HandleFunc("POST /restore/schedule", s.restoreSchedule)
	}
	mux.HandleFunc("/", s.dashboard)
	s.http = &http.Server{Addr: s.cfg.Observability.Listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.http.Shutdown(shutdown)
	}()
	err := s.http.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	man, err := manifest.ReadLatest(s.cfg.Backup.Root)
	if err != nil {
		http.Error(w, "latest manifest unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	if _, err := os.Stat(filepath.Join(s.cfg.Backup.Root, "manifests", "latest.json.sig")); err == nil {
		if err := manifest.VerifyLatestSignature(s.cfg.Backup.Root, s.cfg.Security.SigningKeyPath+".pub"); err != nil {
			http.Error(w, "manifest signature invalid: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
	}
	if s.cfg.Audit.Enabled && s.cfg.Audit.Required {
		if _, err := audit.Verify(s.cfg.Audit.Path); err != nil {
			http.Error(w, "audit ledger invalid: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		if _, err := os.Stat(s.cfg.Audit.Path + ".checkpoint.json"); err == nil {
			if err := audit.VerifyCheckpoint(s.cfg.Audit.Path, s.cfg.Security.SigningKeyPath+".pub"); err != nil {
				http.Error(w, "audit checkpoint invalid: "+err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
	}
	if s.cfg.Policy.Enabled && s.cfg.Policy.EnforceInHealth {
		report := policy.Evaluate(context.Background(), s.cfg, time.Now())
		if !report.Healthy {
			http.Error(w, policy.Error(report).Error(), http.StatusServiceUnavailable)
			return
		}
	}
	if s.control != nil && s.cfg.ControlPlane.Replication.Enabled {
		rh, rerr := controlplane.ReplicationHealthWithStorage(context.Background(), s.control, s.cfg.ControlPlane.Replication, s.cfg.ControlPlane.Storage)
		if rerr != nil {
			http.Error(w, "replication health unavailable: "+rerr.Error(), http.StatusServiceUnavailable)
			return
		}
		if rh.Deficits > 0 {
			http.Error(w, fmt.Sprintf("replication policy deficit: %d generations below min_healthy", rh.Deficits), http.StatusServiceUnavailable)
			return
		}
	}
	if s.control != nil && s.cfg.ControlPlane.Storage.Erasure.Enabled && s.cfg.ControlPlane.Storage.Erasure.Distributed {
		eh, eerr := controlplane.EvaluateDistributedErasureHealth(context.Background(), s.control, s.cfg, time.Now().UTC())
		if eerr != nil {
			http.Error(w, "erasure health unavailable: "+eerr.Error(), http.StatusServiceUnavailable)
			return
		}
		if eh.Unrecoverable > 0 || eh.FailureDomainDeficits > 0 {
			http.Error(w, fmt.Sprintf("distributed erasure deficit: unrecoverable=%d failure_domains=%d", eh.Unrecoverable, eh.FailureDomainDeficits), http.StatusServiceUnavailable)
			return
		}
	}
	if man.Failed > 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_, _ = fmt.Fprintf(w, "ok=%t repos=%d failed=%d warnings=%d\n", man.Failed == 0, len(man.Repositories), man.Failed, man.WarningCount)
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	man, err := manifest.ReadLatest(s.cfg.Backup.Root)
	if err != nil {
		_, _ = fmt.Fprintln(w, "repoark_manifest_available 0")
		return
	}
	_, _ = fmt.Fprintln(w, "# HELP repoark_manifest_available Whether a latest manifest can be read.")
	_, _ = fmt.Fprintln(w, "# TYPE repoark_manifest_available gauge")
	_, _ = fmt.Fprintln(w, "repoark_manifest_available 1")
	_, _ = fmt.Fprintf(w, "repoark_repositories_total %d\n", len(man.Repositories))
	_, _ = fmt.Fprintf(w, "repoark_repositories_succeeded %d\n", man.Succeeded)
	_, _ = fmt.Fprintf(w, "repoark_repositories_failed %d\n", man.Failed)
	_, _ = fmt.Fprintf(w, "repoark_warnings_total %d\n", man.WarningCount)
	_, _ = fmt.Fprintf(w, "repoark_projects_v2_owners_backed_up %d\n", man.ProjectsV2OwnersBackedUp)
	actionsArtifacts := 0
	for _, repo := range man.Repositories {
		actionsArtifacts += repo.ActionArtifactsBackedUp
	}
	_, _ = fmt.Fprintf(w, "repoark_actions_artifacts_backed_up %d\n", actionsArtifacts)
	packagePayloads := 0
	for _, repo := range man.Repositories {
		packagePayloads += repo.PackagePayloadsBackedUp
	}
	_, _ = fmt.Fprintf(w, "repoark_package_payloads_backed_up %d\n", packagePayloads)
	_, _ = fmt.Fprintf(w, "repoark_cas_objects %d\n", man.CAS.Objects)
	_, _ = fmt.Fprintf(w, "repoark_cas_physical_bytes %d\n", man.CAS.PhysicalBytes)
	_, _ = fmt.Fprintf(w, "repoark_cas_reclaimed_bytes %d\n", man.CAS.ReclaimedBytes)
	if s.cfg.Policy.Enabled {
		pr := policy.Evaluate(context.Background(), s.cfg, time.Now())
		if pr.Healthy {
			_, _ = fmt.Fprintln(w, "repoark_policy_healthy 1")
		} else {
			_, _ = fmt.Fprintln(w, "repoark_policy_healthy 0")
		}
		_, _ = fmt.Fprintf(w, "repoark_policy_violations %d\n", len(pr.Violations))
	}
	if s.control != nil {
		if st, serr := s.control.Stats(context.Background(), time.Now().UTC()); serr == nil {
			_, _ = fmt.Fprintf(w, "repoark_control_jobs_queued %d\n", st.QueuedJobs)
			_, _ = fmt.Fprintf(w, "repoark_control_jobs_running %d\n", st.RunningJobs)
			_, _ = fmt.Fprintf(w, "repoark_control_jobs_failed %d\n", st.FailedJobs)
			_, _ = fmt.Fprintf(w, "repoark_control_jobs_stranded %d\n", st.StrandedJobs)
			_, _ = fmt.Fprintf(w, "repoark_control_repositories %d\n", st.Repositories)
			_, _ = fmt.Fprintf(w, "repoark_control_repositories_due %d\n", st.DueRepositories)
			_, _ = fmt.Fprintf(w, "repoark_control_generations %d\n", st.Generations)
			_, _ = fmt.Fprintf(w, "repoark_control_agents_connected %d\n", st.ConnectedAgents)
			_, _ = fmt.Fprintf(w, "repoark_control_storage_agents_degraded %d\n", st.DegradedStorageAgents)
			_, _ = fmt.Fprintf(w, "repoark_control_storage_agents_unhealthy %d\n", st.UnhealthyStorageAgents)
			_, _ = fmt.Fprintf(w, "repoark_control_replicas_ready %d\n", st.ReadyReplicas)
			_, _ = fmt.Fprintf(w, "repoark_control_restore_approvals_pending %d\n", st.PendingApprovals)
			_, _ = fmt.Fprintf(w, "repoark_control_replication_transfers_active %d\n", st.ActiveTransfers)
			_, _ = fmt.Fprintf(w, "repoark_control_object_refs %d\n", st.ObjectRefs)
			_, _ = fmt.Fprintf(w, "repoark_control_object_leases_active %d\n", st.ActiveObjectLeases)
			_, _ = fmt.Fprintf(w, "repoark_control_erasure_sets %d\n", st.ErasureSets)
			_, _ = fmt.Fprintf(w, "repoark_control_erasure_shard_copies %d\n", st.ErasureShardCopies)
			if s.cfg.ControlPlane.Replication.Enabled {
				if rh, e := controlplane.ReplicationHealthWithStorage(context.Background(), s.control, s.cfg.ControlPlane.Replication, s.cfg.ControlPlane.Storage); e == nil {
					_, _ = fmt.Fprintf(w, "repoark_control_replication_deficits %d\n", rh.Deficits)
					_, _ = fmt.Fprintf(w, "repoark_control_replication_healthy_generations %d\n", rh.HealthyGenerations)
					_, _ = fmt.Fprintf(w, "repoark_control_replication_failure_domain_deficits %d\n", rh.FailureDomainDeficits)
				}
			}
			if s.cfg.ControlPlane.Storage.Erasure.Enabled && s.cfg.ControlPlane.Storage.Erasure.Distributed {
				if eh, e := controlplane.EvaluateDistributedErasureHealth(context.Background(), s.control, s.cfg, time.Now().UTC()); e == nil {
					_, _ = fmt.Fprintf(w, "repoark_control_erasure_healthy_sets %d\n", eh.Healthy)
					_, _ = fmt.Fprintf(w, "repoark_control_erasure_unrecoverable_sets %d\n", eh.Unrecoverable)
					_, _ = fmt.Fprintf(w, "repoark_control_erasure_failure_domain_deficits %d\n", eh.FailureDomainDeficits)
				}
			}
		} else {
			_, _ = fmt.Fprintln(w, "repoark_control_store_available 0")
		}
	}
	if s.cfg.Audit.Enabled {
		n, aerr := audit.Verify(s.cfg.Audit.Path)
		if aerr != nil {
			_, _ = fmt.Fprintln(w, "repoark_audit_chain_valid 0")
		} else {
			_, _ = fmt.Fprintln(w, "repoark_audit_chain_valid 1")
		}
		_, _ = fmt.Fprintf(w, "repoark_audit_records_total %d\n", n)
	}
	_, _ = fmt.Fprintf(w, "repoark_last_backup_timestamp_seconds %.0f\n", float64(man.EndedAt.Unix()))
	_, _ = fmt.Fprintf(w, "repoark_process_uptime_seconds %.0f\n", time.Since(s.started).Seconds())
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	man, err := manifest.ReadLatest(s.cfg.Backup.Root)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	pr := policy.Evaluate(context.Background(), s.cfg, time.Now())
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": man.Failed == 0 && (!s.cfg.Policy.Enabled || pr.Healthy), "manifest": man, "policy": pr})
}

func (s *Server) policyStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	r := policy.Evaluate(context.Background(), s.cfg, time.Now())
	if !r.Healthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(r)
}

func (s *Server) controlErasure(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.control == nil {
		http.Error(w, "control plane disabled", http.StatusNotFound)
		return
	}
	h, err := controlplane.EvaluateDistributedErasureHealth(context.Background(), s.control, s.cfg, time.Now().UTC())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	_ = json.NewEncoder(w).Encode(h)
}

func (s *Server) controlInventories(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.control == nil {
		http.Error(w, "control plane disabled", http.StatusNotFound)
		return
	}
	left, right := r.URL.Query().Get("left"), r.URL.Query().Get("right")
	if left != "" && right != "" {
		cmp, err := controlplane.CompareInventories(r.Context(), s.control, left, right)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(cmp)
		return
	}
	xs, err := controlplane.AgentInventories(r.Context(), s.control)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(xs)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprint(w, dashboardHTML)
}

func (s *Server) fleetStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !s.cfg.Fleet.Enabled {
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": false, "accounts": []any{}})
		return
	}
	type account struct {
		Name      string    `json:"name"`
		Root      string    `json:"root"`
		OK        bool      `json:"ok"`
		Succeeded int       `json:"succeeded,omitempty"`
		Failed    int       `json:"failed,omitempty"`
		Warnings  int       `json:"warnings,omitempty"`
		EndedAt   time.Time `json:"ended_at,omitempty"`
		Error     string    `json:"error,omitempty"`
	}
	out := make([]account, 0, len(s.cfg.Fleet.Accounts))
	for _, a := range s.cfg.Fleet.Accounts {
		acfg := fleet.ResolveAccountConfig(s.cfg, a)
		row := account{Name: a.Name, Root: acfg.Backup.Root}
		m, err := manifest.ReadLatest(acfg.Backup.Root)
		if err != nil {
			row.Error = err.Error()
		} else {
			row.OK = m.Failed == 0
			row.Succeeded = m.Succeeded
			row.Failed = m.Failed
			row.Warnings = m.WarningCount
			row.EndedAt = m.EndedAt
		}
		out = append(out, row)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "accounts": out})
}

func (s *Server) controlStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.control == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": false})
		return
	}
	st, err := s.control.Stats(r.Context(), time.Now().UTC())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "stats": st})
}
func (s *Server) controlJobs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.control == nil {
		_ = json.NewEncoder(w).Encode([]any{})
		return
	}
	x, err := s.control.ListJobs(r.Context(), 100)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(x)
}
func (s *Server) controlRepositories(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.control == nil {
		_ = json.NewEncoder(w).Encode([]any{})
		return
	}
	x, err := s.control.ListRepositories(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(x)
}
func (s *Server) controlAgents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.control == nil {
		_ = json.NewEncoder(w).Encode([]any{})
		return
	}
	x, err := s.control.ListAgents(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(x)
}

func (s *Server) controlReplicas(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.control == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": false})
		return
	}
	x, err := s.control.ListAllReplicas(r.Context(), 50000)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	rh, err := controlplane.ReplicationHealthWithStorage(r.Context(), s.control, s.cfg.ControlPlane.Replication, s.cfg.ControlPlane.Storage)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"enabled": s.cfg.ControlPlane.Replication.Enabled, "health": rh, "replicas": x})
}
func (s *Server) controlApprovals(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.control == nil {
		_ = json.NewEncoder(w).Encode([]any{})
		return
	}
	x, err := s.control.ListRestoreApprovals(r.Context(), 200)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(x)
}
