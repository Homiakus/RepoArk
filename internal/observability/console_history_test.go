package observability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/audit"
	"github.com/Homiakus/repoark/internal/config"
)

func TestConsoleHistoryFromAuditGroupsOperationsNewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "ledger.jsonl")
	appendHistoryAudit(t, path, "req-1", "backup", "local-browser", "normal", "requested", "")
	appendHistoryAudit(t, path, "req-1", "backup", "local-browser", "normal", "success", "")
	appendHistoryAudit(t, path, "req-2", "offsite", "operator@example", "elevated", "requested", "")
	appendHistoryAudit(t, path, "req-2", "offsite", "operator@example", "elevated", "error", "restic failed")

	entries, err := consoleHistoryFromAudit(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries=%d want 2", len(entries))
	}
	if entries[0].RequestID != "req-2" || entries[0].Operation != "offsite" || entries[0].State != "failed" {
		t.Fatalf("unexpected newest entry: %+v", entries[0])
	}
	if entries[0].Actor != "operator@example" || entries[0].Risk != "elevated" || entries[0].Detail != "restic failed" {
		t.Fatalf("unexpected metadata: %+v", entries[0])
	}
	if entries[0].EndedAt == nil || entries[0].StartedAt.IsZero() || entries[0].EndedAt.Before(entries[0].StartedAt) {
		t.Fatalf("invalid timestamps: %+v", entries[0])
	}
	if entries[1].RequestID != "req-1" || entries[1].State != "succeeded" {
		t.Fatalf("unexpected older entry: %+v", entries[1])
	}
}

func TestConsoleHistoryPreservesStartedWithoutTerminalRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	appendHistoryAudit(t, path, "req-live", "backup", "local-browser", "normal", "requested", "")

	entries, err := consoleHistoryFromAudit(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries=%d want 1", len(entries))
	}
	if entries[0].State != "started" || entries[0].EndedAt != nil {
		t.Fatalf("unexpected started entry: %+v", entries[0])
	}
}

func TestConsoleHistoryHandlerDisabledAuditIsExplicit(t *testing.T) {
	cfg := config.Default()
	cfg.Backup.Root = t.TempDir()
	cfg.Audit.Enabled = false
	c := &consoleServer{base: New(cfg), jobs: newConsoleJobManager(t.Context())}

	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9787/api/v1/console/history", nil)
	w := httptest.NewRecorder()
	c.history(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response consoleHistoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Enabled || response.Persistent || response.Verified || len(response.Entries) != 0 {
		t.Fatalf("unexpected disabled response: %+v", response)
	}
}

func TestConsoleHistoryHandlerRejectsTamperedAudit(t *testing.T) {
	cfg := config.Default()
	cfg.Backup.Root = t.TempDir()
	cfg.Audit.Enabled = true
	cfg.Audit.Path = filepath.Join(t.TempDir(), "audit", "ledger.jsonl")
	appendHistoryAudit(t, cfg.Audit.Path, "req-1", "backup", "local-browser", "normal", "requested", "")
	b, err := os.ReadFile(cfg.Audit.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("audit ledger unexpectedly empty")
	}
	b[len(b)/2] ^= 1
	if err := os.WriteFile(cfg.Audit.Path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	c := &consoleServer{base: New(cfg), jobs: newConsoleJobManager(t.Context())}

	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9787/api/v1/console/history", nil)
	w := httptest.NewRecorder()
	c.history(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want %d body=%s", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}
}

func TestConsoleHistoryPageLinksBackToConsole(t *testing.T) {
	cfg := config.Default()
	c := &consoleServer{base: New(cfg), jobs: newConsoleJobManager(t.Context())}
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9787/history", nil)
	w := httptest.NewRecorder()
	c.historyPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Operation History") || !strings.Contains(w.Body.String(), "href=\"/\"") {
		t.Fatalf("unexpected history page: %s", w.Body.String())
	}
}

func appendHistoryAudit(t *testing.T, path, requestID, operation, actor, risk, status, detail string) {
	t.Helper()
	fields := map[string]any{"surface": "web", "request_id": requestID, "actor": actor, "risk": risk}
	if _, err := audit.Append(path, "web-operation", operation, status, detail, fields); err != nil {
		t.Fatal(err)
	}
	// Ensure requested and terminal records never share an identical timestamp
	// on platforms with coarse filesystem clocks when duration is rendered.
	time.Sleep(time.Millisecond)
}
