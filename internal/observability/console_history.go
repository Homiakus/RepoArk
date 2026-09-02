package observability

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/audit"
	"github.com/Homiakus/repoark/internal/webauth"
)

const (
	defaultConsoleHistoryLimit = 50
	maxConsoleHistoryLimit     = 200
	consoleHistoryAuditScan    = 2000
)

type consoleHistoryEntry struct {
	RequestID string     `json:"request_id"`
	Operation string     `json:"operation"`
	Actor     string     `json:"actor,omitempty"`
	Risk      string     `json:"risk,omitempty"`
	State     string     `json:"state"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Detail    string     `json:"detail,omitempty"`
	AuditSeq  uint64     `json:"audit_seq"`
}

type consoleHistoryResponse struct {
	Enabled    bool                  `json:"enabled"`
	Persistent bool                  `json:"persistent"`
	Verified   bool                  `json:"verified"`
	Entries    []consoleHistoryEntry `json:"entries"`
	Error      string                `json:"error,omitempty"`
}

func (c *consoleServer) history(w http.ResponseWriter, r *http.Request) {
	if !c.authorizeRead(w, r) {
		return
	}
	if !c.base.cfg.Audit.Enabled {
		writeConsoleJSON(w, http.StatusOK, consoleHistoryResponse{
			Enabled:    false,
			Persistent: false,
			Verified:   false,
			Entries:    []consoleHistoryEntry{},
		})
		return
	}

	limit := defaultConsoleHistoryLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxConsoleHistoryLimit {
		limit = maxConsoleHistoryLimit
	}

	entries, err := consoleHistoryFromAudit(c.base.cfg.Audit.Path, limit)
	if err != nil {
		writeConsoleJSON(w, http.StatusServiceUnavailable, consoleHistoryResponse{
			Enabled:    true,
			Persistent: true,
			Verified:   false,
			Entries:    []consoleHistoryEntry{},
			Error:      "audit-backed operation history unavailable: " + err.Error(),
		})
		return
	}
	writeConsoleJSON(w, http.StatusOK, consoleHistoryResponse{
		Enabled:    true,
		Persistent: true,
		Verified:   true,
		Entries:    entries,
	})
}

func (c *consoleServer) historyPage(w http.ResponseWriter, r *http.Request) {
	if c.base.auth != nil {
		if _, err := c.base.auth.Authorize(r, webauth.RoleViewer, false); err != nil {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprint(w, consoleHistoryHTML)
}

func consoleHistoryFromAudit(path string, limit int) ([]consoleHistoryEntry, error) {
	if limit <= 0 {
		return []consoleHistoryEntry{}, nil
	}
	if limit > maxConsoleHistoryLimit {
		limit = maxConsoleHistoryLimit
	}

	records, err := audit.RecentIfExists(path, consoleHistoryAuditScan, "web-operation")
	if err != nil {
		return nil, err
	}

	byRequest := make(map[string]*consoleHistoryEntry, limit)
	order := make([]string, 0, limit)
	for _, r := range records {
		requestID := historyFieldString(r.Fields, "request_id")
		if requestID == "" {
			continue
		}
		entry, ok := byRequest[requestID]
		if !ok {
			entry = &consoleHistoryEntry{RequestID: requestID, Operation: r.Target, State: "incomplete", AuditSeq: r.Seq}
			byRequest[requestID] = entry
			order = append(order, requestID)
		}
		if entry.Operation == "" {
			entry.Operation = r.Target
		}
		if entry.Actor == "" {
			entry.Actor = historyFieldString(r.Fields, "actor")
		}
		if entry.Risk == "" {
			entry.Risk = historyFieldString(r.Fields, "risk")
		}

		switch r.Status {
		case "requested":
			entry.StartedAt = r.Time
		case "success", "error", "cancelled", "rejected":
			if entry.EndedAt == nil {
				ended := r.Time
				entry.EndedAt = &ended
				entry.State = consoleHistoryState(r.Status)
				entry.Detail = r.Detail
			}
		}
	}

	entries := make([]consoleHistoryEntry, 0, consoleHistoryMin(limit, len(order)))
	for _, requestID := range order {
		entry := byRequest[requestID]
		if entry.StartedAt.IsZero() && entry.EndedAt != nil {
			entry.StartedAt = *entry.EndedAt
		}
		entries = append(entries, *entry)
		if len(entries) == limit {
			break
		}
	}
	return entries, nil
}

func consoleHistoryState(status string) string {
	switch status {
	case "success":
		return "succeeded"
	case "error":
		return "failed"
	case "cancelled":
		return "cancelled"
	case "rejected":
		return "rejected"
	default:
		return "incomplete"
	}
}

func historyFieldString(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	value, ok := fields[key]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func consoleHistoryMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

const consoleHistoryHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="dark">
<title>RepoArk Operation History</title>
<style>
:root{--bg:#0a0d12;--panel:#11161e;--line:#26303d;--text:#eef3f8;--muted:#8d99a8;--ok:#58d68d;--warn:#f4bf4f;--bad:#ff6b6b;--accent:#8b7cff;font-family:Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color-scheme:dark;background:var(--bg);color:var(--text)}
*{box-sizing:border-box}body{margin:0;background:radial-gradient(circle at 15% -10%,#19213a 0,transparent 34%),var(--bg);min-height:100vh}.shell{max-width:1200px;margin:auto;padding:20px}.top{display:flex;align-items:center;justify-content:space-between;gap:14px;margin-bottom:16px}.title{font-size:22px;font-weight:800}.muted{color:var(--muted)}.nav{display:flex;gap:8px}.nav a,.nav button{color:var(--text);text-decoration:none;border:1px solid var(--line);background:#111722;border-radius:10px;padding:8px 11px;font:inherit;cursor:pointer}.panel{background:linear-gradient(180deg,#121821,#0f141b);border:1px solid var(--line);border-radius:16px;padding:16px;box-shadow:0 12px 30px #0002}.banner{display:none;margin-bottom:12px;padding:10px 12px;border:1px solid #60444a;background:#2b1d21;border-radius:10px;color:#ffd6da}.meta{font-size:12px;margin-bottom:12px}.tablewrap{overflow:auto}.history{width:100%;border-collapse:collapse;font-size:13px;min-width:760px}.history th,.history td{text-align:left;padding:10px 8px;border-bottom:1px solid var(--line);vertical-align:top}.history th{color:var(--muted);font-weight:650}.state{font-weight:750}.ok{color:var(--ok)}.warn{color:var(--warn)}.bad{color:var(--bad)}.mono{font-family:ui-monospace,SFMono-Regular,Consolas,monospace}.empty{color:var(--muted);padding:20px 4px}.detail{max-width:360px;overflow-wrap:anywhere}@media(max-width:680px){.shell{padding:12px}.top{align-items:flex-start;flex-direction:column}.nav{width:100%}.nav a,.nav button{flex:1;text-align:center}}
</style>
</head>
<body><div class="shell">
<div class="top"><div><div class="title">Operation History</div><div class="muted">Tamper-evident browser operation timeline</div></div><div class="nav"><a href="/">Console</a><button onclick="loadHistory()">Refresh</button></div></div>
<div id="banner" class="banner"></div>
<section class="panel"><div id="meta" class="meta muted">Loading…</div><div class="tablewrap"><div id="history" class="empty">Loading operation history…</div></div></section>
</div>
<script>
const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const duration=e=>{if(!e.started_at||!e.ended_at)return'—';const ms=Math.max(0,new Date(e.ended_at)-new Date(e.started_at));if(ms<1000)return ms+'ms';if(ms<60000)return(ms/1000).toFixed(1)+'s';return(ms/60000).toFixed(1)+'m'};
const cls=s=>s==='succeeded'?'ok':(s==='failed'||s==='rejected')?'bad':'warn';
async function loadHistory(){const b=document.querySelector('#banner'),root=document.querySelector('#history'),meta=document.querySelector('#meta');b.style.display='none';try{const r=await fetch('/api/v1/console/history?limit=100',{cache:'no-store'});const x=await r.json();if(!r.ok)throw new Error(x.error||('HTTP '+r.status));if(!x.enabled){meta.textContent='Persistent history is disabled because audit.enabled is false.';root.innerHTML='<div class="empty">Enable the audit ledger to retain verified browser operation history across restarts.</div>';return}meta.textContent=(x.verified?'Verified hash-chain':'Unverified')+' · '+(x.entries||[]).length+' recent operations';const rows=x.entries||[];root.innerHTML=rows.length?'<table class="history"><thead><tr><th>State</th><th>Operation</th><th>Started</th><th>Duration</th><th>Actor</th><th>Risk</th><th>Detail</th></tr></thead><tbody>'+rows.map(e=>'<tr><td class="state '+cls(e.state)+'">'+esc(e.state)+'</td><td>'+esc(e.operation)+'</td><td>'+esc(new Date(e.started_at).toLocaleString())+'</td><td>'+esc(duration(e))+'</td><td class="mono">'+esc(e.actor||'—')+'</td><td>'+esc(e.risk||'—')+'</td><td class="detail">'+esc(e.detail||'')+'</td></tr>').join('')+'</tbody></table>':'<div class="empty">No browser operations have been recorded yet.</div>'}catch(err){b.textContent=err.message;b.style.display='block';meta.textContent='History unavailable';root.innerHTML='<div class="empty">The audit-backed history could not be verified.</div>'}}
loadHistory();setInterval(loadHistory,15000);
</script></body></html>`
