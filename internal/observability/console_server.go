package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/controlplane"
	"github.com/Homiakus/repoark/internal/execx"
	"github.com/Homiakus/repoark/internal/githubapi"
	"github.com/Homiakus/repoark/internal/webauth"
)

type consoleServer struct {
	base *Server
	jobs *consoleJobManager
}

// RunConsole starts the primary browser UI. It intentionally reuses the
// observability/control-plane handlers so health, metrics, recovery and
// operator actions share one process and one listen address.
func (s *Server) RunConsole(ctx context.Context) error {
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
	if s.auth == nil && !loopbackListen(s.cfg.Observability.Listen) {
		return fmt.Errorf("web console without OIDC may only listen on loopback; got %q (configure control_plane.web_auth before remote exposure)", s.cfg.Observability.Listen)
	}

	c := &consoleServer{base: s, jobs: newConsoleJobManager(ctx)}
	mux := http.NewServeMux()
	c.registerRoutes(mux)

	s.http = &http.Server{
		Addr:              s.cfg.Observability.Listen,
		Handler:           consoleSecurityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
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

func (c *consoleServer) registerRoutes(mux *http.ServeMux) {
	s := c.base
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

	mux.HandleFunc("GET /api/v1/console/state", c.state)
	mux.HandleFunc("GET /api/v1/console/session", c.session)
	mux.HandleFunc("GET /api/v1/console/job", c.job)
	mux.HandleFunc("POST /api/v1/console/jobs/{name}", c.startJob)
	mux.HandleFunc("POST /api/v1/console/job/cancel", c.cancelJob)

	if s.auth != nil {
		mux.HandleFunc("GET /auth/login", s.authLogin)
		mux.HandleFunc("GET /auth/callback", c.authCallback)
		mux.HandleFunc("GET /auth/step-up", s.authStepUp)
		mux.HandleFunc("POST /auth/logout", s.authLogout)
		mux.HandleFunc("GET /restore", s.restoreWizard)
		mux.HandleFunc("POST /restore/request", s.restoreRequest)
		mux.HandleFunc("POST /restore/approve", s.restoreApprove)
		mux.HandleFunc("POST /restore/schedule", s.restoreSchedule)
	}
	mux.HandleFunc("GET /", c.dashboard)
}

func (c *consoleServer) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprint(w, consoleHTML)
}

func (c *consoleServer) state(w http.ResponseWriter, _ *http.Request) {
	cfg := c.base.cfg
	writeConsoleJSON(w, http.StatusOK, map[string]any{
		"actions": consoleActions(cfg),
		"github_auth": map[string]any{
			"ready": githubapi.ResolveToken(cfg.GitHub.TokenEnv) != "",
		},
		"tools": map[string]bool{
			"git":     execx.Exists("git"),
			"git_lfs": execx.Exists("git-lfs"),
			"docker":  execx.Exists("docker"),
			"restic":  execx.Exists("restic"),
			"rclone":  execx.Exists("rclone"),
		},
		"features": map[string]bool{
			"fleet":         cfg.Fleet.Enabled,
			"gitlab":        cfg.GitLab.Enabled,
			"offsite":       cfg.Offsite.Enabled,
			"cas":           cfg.CAS.Enabled,
			"control_plane": cfg.ControlPlane.Enabled,
			"recovery_ui":   c.base.auth != nil && cfg.ControlPlane.Generations.Enabled,
		},
		"backup_root": cfg.Backup.Root,
		"listen":      cfg.Observability.Listen,
	})
}

func (c *consoleServer) session(w http.ResponseWriter, r *http.Request) {
	if c.base.auth == nil {
		writeConsoleJSON(w, http.StatusOK, map[string]any{"authenticated": true, "local": true, "role": "local"})
		return
	}
	id, err := c.base.auth.Authorize(r, webauth.RoleViewer, false)
	if err != nil {
		writeConsoleJSON(w, http.StatusUnauthorized, map[string]any{"authenticated": false, "login": "/auth/login"})
		return
	}
	writeConsoleJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"local":         false,
		"role":          id.Role,
		"email":         id.Email,
		"csrf":          id.CSRF,
	})
}

func (c *consoleServer) job(w http.ResponseWriter, _ *http.Request) {
	writeConsoleJSON(w, http.StatusOK, map[string]any{"job": c.jobs.Snapshot()})
}

func (c *consoleServer) startJob(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	risk := ""
	for _, action := range consoleActions(c.base.cfg) {
		if action.Name == name {
			risk = action.Risk
			break
		}
	}
	if risk == "" {
		writeConsoleJSON(w, http.StatusNotFound, map[string]any{"error": "unknown operation"})
		return
	}
	if !c.authorizeMutation(w, r, risk == "danger") {
		return
	}
	run, err := consoleOperation(c.base.cfg, name)
	if err != nil {
		writeConsoleJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	job, err := c.jobs.Start(name, run)
	if errorsIsJobRunning(err) {
		writeConsoleJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "job": job})
		return
	}
	if err != nil {
		writeConsoleJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeConsoleJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

func (c *consoleServer) cancelJob(w http.ResponseWriter, r *http.Request) {
	if !c.authorizeMutation(w, r, false) {
		return
	}
	if err := c.jobs.Cancel(); err != nil {
		writeConsoleJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	writeConsoleJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (c *consoleServer) authorizeMutation(w http.ResponseWriter, r *http.Request, stepUp bool) bool {
	if c.base.auth == nil {
		return true
	}
	role := webauth.RoleOperator
	if stepUp {
		role = webauth.RoleAdmin
	}
	id, err := c.base.auth.Authorize(r, role, stepUp)
	if err != nil {
		writeConsoleJSON(w, http.StatusForbidden, map[string]any{"error": "unauthorized: " + err.Error()})
		return false
	}
	if err := c.base.auth.ValidateCSRF(r, id); err != nil {
		writeConsoleJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return false
	}
	return true
}

func (c *consoleServer) authCallback(w http.ResponseWriter, r *http.Request) {
	if err := c.base.auth.Callback(w, r); err != nil {
		http.Error(w, "OIDC callback: "+err.Error(), http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func writeConsoleJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func errorsIsJobRunning(err error) bool {
	return err == errConsoleJobRunning
}

func loopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func consoleSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}
