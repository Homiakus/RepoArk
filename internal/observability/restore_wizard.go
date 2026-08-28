package observability

import (
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/controlplane"
	"github.com/Homiakus/repoark/internal/webauth"
)

type restoreWizardData struct {
	Identity  webauth.Identity
	CSRF      string
	Repos     []controlplane.Repository
	Repo      string
	Gens      []controlplane.Generation
	Approvals []controlplane.RestoreApproval
	Message   string
	Error     string
	Approval  bool
}

var restoreWizardTemplate = template.Must(template.New("restore").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>RepoArk Recovery</title><style>
:root{color-scheme:light dark;font-family:ui-sans-serif,system-ui,sans-serif}body{max-width:1050px;margin:0 auto;padding:28px}a{color:inherit}.top{display:flex;justify-content:space-between;align-items:center;gap:16px}.card{border:1px solid #8886;border-radius:14px;padding:18px;margin:16px 0}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:12px}label{display:block;font-size:.9rem;margin:.5rem 0}input,select,button{font:inherit;padding:.65rem .75rem;border-radius:9px;border:1px solid #8888;max-width:100%}button{cursor:pointer;font-weight:650}.danger{border-color:#c33}.muted{opacity:.7}.ok{padding:10px;border-radius:8px;background:#2a74}.err{padding:10px;border-radius:8px;background:#b224}.mono{font-family:ui-monospace,SFMono-Regular,monospace;font-size:.88rem;overflow-wrap:anywhere}table{width:100%;border-collapse:collapse}td,th{padding:8px;text-align:left;border-bottom:1px solid #8884}.actions{display:flex;gap:8px;flex-wrap:wrap}</style></head><body>
<div class="top"><div><h1>RepoArk Recovery</h1><div class="muted">{{.Identity.Email}} · {{.Identity.Role}}</div></div><div class="actions"><a href="/">Dashboard</a><a href="/auth/step-up">Step-up sign in</a><form method="post" action="/auth/logout"><input type="hidden" name="_csrf" value="{{.CSRF}}"><button>Sign out</button></form></div></div>
{{if .Message}}<div class="ok">{{.Message}}</div>{{end}}{{if .Error}}<div class="err">{{.Error}}</div>{{end}}
<div class="card"><h2>1. Select repository</h2><form method="get" action="/restore"><select name="repo" onchange="this.form.submit()"><option value="">Choose…</option>{{range .Repos}}<option value="{{.FullName}}" {{if eq $.Repo .FullName}}selected{{end}}>{{.FullName}}</option>{{end}}</select></form></div>
{{if .Repo}}<div class="card"><h2>2. Point in time</h2><div class="muted">Verified immutable generations indexed by the control plane.</div><table><thead><tr><th>Generation</th><th>Created</th><th>Verified</th><th>Action</th></tr></thead><tbody>{{range .Gens}}<tr><td class="mono">{{.ID}}</td><td>{{.CreatedAt}}</td><td>{{.Verified}}</td><td><form method="post" action="/restore/request"><input type="hidden" name="_csrf" value="{{$.CSRF}}"><input type="hidden" name="repo" value="{{$.Repo}}"><input type="hidden" name="generation" value="{{.ID}}"><button class="danger">Request restore</button></form></td></tr>{{end}}</tbody></table></div>{{end}}
{{if .Approval}}<div class="card"><h2>3. Approval queue</h2><p class="muted">Approve/schedule endpoints require the configured OIDC step-up AMR (for example WebAuthn/MFA) and enforce RepoArk roles.</p><table><thead><tr><th>ID</th><th>Repository</th><th>Generation</th><th>Requester</th><th>Status</th><th>Action</th></tr></thead><tbody>{{range .Approvals}}<tr><td class="mono">{{.ID}}</td><td>{{.Repository}}</td><td class="mono">{{.GenerationID}}</td><td>{{.RequestedBy}}</td><td>{{.Status}}</td><td><div class="actions">{{if eq .Status "pending"}}<form method="post" action="/restore/approve"><input type="hidden" name="_csrf" value="{{$.CSRF}}"><input type="hidden" name="id" value="{{.ID}}"><button>Approve</button></form>{{end}}{{if eq .Status "approved"}}<form method="post" action="/restore/schedule"><input type="hidden" name="_csrf" value="{{$.CSRF}}"><input type="hidden" name="id" value="{{.ID}}"><button class="danger">Schedule restore</button></form>{{end}}</div></td></tr>{{end}}</tbody></table></div>{{end}}
</body></html>`))

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request)  { s.auth.Login(w, r) }
func (s *Server) authStepUp(w http.ResponseWriter, r *http.Request) { s.auth.StepUp(w, r) }

func (s *Server) authCallback(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.Callback(w, r); err != nil {
		http.Error(w, "OIDC callback: "+err.Error(), http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/restore", http.StatusSeeOther)
}

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	id, err := s.auth.Authorize(r, webauth.RoleViewer, false)
	if err == nil && s.auth.ValidateCSRF(r, id) == nil {
		s.auth.Logout(w)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) restoreWizard(w http.ResponseWriter, r *http.Request) {
	id, err := s.auth.Authorize(r, webauth.RoleViewer, false)
	if err != nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	data := restoreWizardData{Identity: id, CSRF: id.CSRF, Repo: strings.TrimSpace(r.URL.Query().Get("repo")), Approval: s.cfg.ControlPlane.RestoreAuth.Enabled}
	data.Repos, err = s.control.ListRepositories(r.Context())
	if err != nil {
		data.Error = err.Error()
	} else {
		sort.Slice(data.Repos, func(i, j int) bool { return data.Repos[i].FullName < data.Repos[j].FullName })
	}
	if data.Repo != "" {
		var repoID string
		for _, rp := range data.Repos {
			if strings.EqualFold(rp.FullName, data.Repo) {
				repoID = rp.ID
				data.Repo = rp.FullName
				break
			}
		}
		if repoID == "" {
			data.Error = "repository is not in control-plane state"
		} else if data.Gens, err = s.control.ListGenerations(r.Context(), repoID, 250); err != nil {
			data.Error = err.Error()
		}
	}
	if s.cfg.ControlPlane.RestoreAuth.Enabled && webauthRoleAtLeast(id.Role, "operator") {
		data.Approvals, _ = s.control.ListRestoreApprovals(r.Context(), 100)
	}
	data.Message = strings.TrimSpace(r.URL.Query().Get("message"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = restoreWizardTemplate.Execute(w, data)
}

func (s *Server) restoreRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authorizedMutation(w, r, webauth.RoleOperator, false)
	if !ok {
		return
	}
	repository := strings.TrimSpace(r.FormValue("repo"))
	generationID := strings.TrimSpace(r.FormValue("generation"))
	// Browser recovery is deliberately staged only under the configured restore_root.
	// Arbitrary filesystem targets remain an explicit administrative CLI capability.
	target := ""
	if _, _, err := controlplane.ResolveGeneration(r.Context(), s.control, repository, generationID); err != nil {
		s.redirectRestore(w, r, repository, "", err)
		return
	}
	if s.cfg.ControlPlane.RestoreAuth.Enabled && !webIdentityAllowed(id, s.cfg.ControlPlane.RestoreAuth.Requesters) {
		http.Error(w, "OIDC identity is not in restore_approval.requesters", http.StatusForbidden)
		return
	}
	if !s.cfg.ControlPlane.RestoreAuth.Enabled {
		// Without a two-person approval policy, scheduling itself is still a
		// privileged step-up action instead of a one-click viewer operation.
		if _, err := s.auth.Authorize(r, webauth.RoleOperator, true); err != nil {
			http.Error(w, "step-up required: "+err.Error(), http.StatusForbidden)
			return
		}
		j, created, err := controlplane.EnqueueGenerationRestore(r.Context(), s.control, s.cfg, repository, generationID, target, "", 250)
		msg := fmt.Sprintf("restore job %s created=%t", j.ID, created)
		s.redirectRestore(w, r, repository, msg, err)
		return
	}
	ttl, _ := time.ParseDuration(s.cfg.ControlPlane.RestoreAuth.ApprovalTTL)
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	now := time.Now().UTC()
	a := controlplane.RestoreApproval{ID: fmt.Sprintf("restore-%x", now.UnixNano()), Repository: repository, GenerationID: generationID, Target: target, RequestedBy: id.Subject, Status: controlplane.ApprovalPending, CreatedAt: now, ExpiresAt: now.Add(ttl)}
	if err := s.control.CreateRestoreApproval(r.Context(), a); err != nil {
		s.redirectRestore(w, r, repository, "", err)
		return
	}
	s.redirectRestore(w, r, repository, "restore request "+a.ID+" created", nil)
}

func (s *Server) restoreApprove(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authorizedMutation(w, r, webauth.RoleAdmin, true)
	if !ok {
		return
	}
	if !webIdentityAllowed(id, s.cfg.ControlPlane.RestoreAuth.Approvers) {
		http.Error(w, "OIDC identity is not in restore_approval.approvers", http.StatusForbidden)
		return
	}
	requestID := strings.TrimSpace(r.FormValue("id"))
	err := s.control.ApproveRestore(r.Context(), requestID, id.Subject, s.cfg.ControlPlane.RestoreAuth.RequireDistinctApprover)
	s.redirectRestore(w, r, "", "approved "+requestID, err)
}

func (s *Server) restoreSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authorizedMutation(w, r, webauth.RoleAdmin, true)
	if !ok {
		return
	}
	if !webIdentityAllowed(id, s.cfg.ControlPlane.RestoreAuth.Approvers) {
		http.Error(w, "OIDC identity is not in restore_approval.approvers", http.StatusForbidden)
		return
	}
	requestID := strings.TrimSpace(r.FormValue("id"))
	a, err := s.control.GetRestoreApproval(r.Context(), requestID)
	if err != nil {
		s.redirectRestore(w, r, "", "", err)
		return
	}
	if a.Status != controlplane.ApprovalApproved {
		s.redirectRestore(w, r, a.Repository, "", fmt.Errorf("restore request is %s", a.Status))
		return
	}
	if err := s.control.ScheduleRestore(r.Context(), a.ID); err != nil {
		s.redirectRestore(w, r, a.Repository, "", err)
		return
	}
	j, created, err := controlplane.EnqueueGenerationRestore(r.Context(), s.control, s.cfg, a.Repository, a.GenerationID, a.Target, a.ID, 250)
	if err != nil {
		_ = s.control.ReleaseRestoreSchedule(r.Context(), a.ID)
		s.redirectRestore(w, r, a.Repository, "", err)
		return
	}
	s.redirectRestore(w, r, a.Repository, fmt.Sprintf("restore job %s created=%t", j.ID, created), nil)
}

func (s *Server) authorizedMutation(w http.ResponseWriter, r *http.Request, role webauth.Role, stepUp bool) (webauth.Identity, bool) {
	id, err := s.auth.Authorize(r, role, stepUp)
	if err != nil {
		http.Error(w, "unauthorized: "+err.Error(), http.StatusForbidden)
		return id, false
	}
	if err := s.auth.ValidateCSRF(r, id); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return id, false
	}
	return id, true
}

func (s *Server) redirectRestore(w http.ResponseWriter, r *http.Request, repo, message string, err error) {
	q := ""
	if repo != "" {
		q = "?repo=" + template.URLQueryEscaper(repo)
	}
	if message != "" && err == nil {
		sep := "?"
		if q != "" {
			sep = "&"
		}
		q += sep + "message=" + template.URLQueryEscaper(message)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/restore"+q, http.StatusSeeOther)
}

func webauthRoleAtLeast(got, want string) bool {
	rank := map[string]int{"viewer": 1, "operator": 2, "admin": 3}
	return rank[strings.ToLower(got)] >= rank[strings.ToLower(want)]
}

func webIdentityAllowed(id webauth.Identity, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, x := range allowed {
		x = strings.TrimSpace(x)
		if strings.EqualFold(x, id.Subject) || (id.Email != "" && strings.EqualFold(x, id.Email)) {
			return true
		}
	}
	return false
}
