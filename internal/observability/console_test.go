package observability

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/audit"
	"github.com/Homiakus/repoark/internal/config"
)

const consoleTestWaitTimeout = 10 * time.Second

func TestConsoleJobManagerSingleFlight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newConsoleJobManager(ctx)
	gate := make(chan struct{})

	first, err := m.Start("backup", func(context.Context, func(string)) error {
		<-gate
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.State != "running" {
		t.Fatalf("state=%q want running", first.State)
	}
	if _, err := m.Start("verify", func(context.Context, func(string)) error { return nil }); !errors.Is(err, errConsoleJobRunning) {
		t.Fatalf("second start err=%v want %v", err, errConsoleJobRunning)
	}
	close(gate)
	waitConsoleJobState(t, m, "succeeded")
}

func TestConsoleJobManagerCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newConsoleJobManager(ctx)

	_, err := m.Start("backup", func(ctx context.Context, _ func(string)) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Cancel(); err != nil {
		t.Fatal(err)
	}
	waitConsoleJobState(t, m, "cancelled")
}

func TestLoopbackListen(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:9787", "[::1]:9787", "localhost:9787"} {
		if !loopbackListen(addr) {
			t.Fatalf("%q should be accepted as loopback", addr)
		}
	}
	for _, addr := range []string{"0.0.0.0:9787", ":9787", "192.168.1.10:9787", "bad"} {
		if loopbackListen(addr) {
			t.Fatalf("%q should not be accepted as loopback", addr)
		}
	}
}

func TestLocalMutationAllowed(t *testing.T) {
	tests := []struct {
		name   string
		target string
		host   string
		origin string
		want   bool
	}{
		{name: "same origin", target: "http://127.0.0.1:9787/api", host: "127.0.0.1:9787", origin: "http://127.0.0.1:9787", want: true},
		{name: "localhost", target: "http://localhost:9787/api", host: "localhost:9787", origin: "http://localhost:9787", want: true},
		{name: "curl style no origin", target: "http://127.0.0.1:9787/api", host: "127.0.0.1:9787", want: true},
		{name: "cross site", target: "http://127.0.0.1:9787/api", host: "127.0.0.1:9787", origin: "https://evil.example", want: false},
		{name: "dns rebinding host", target: "http://evil.example/api", host: "evil.example", origin: "http://evil.example", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", tc.target, nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := localMutationAllowed(r); got != tc.want {
				t.Fatalf("localMutationAllowed=%t want %t", got, tc.want)
			}
		})
	}
}

func TestConsoleDisabledOperationIsAudited(t *testing.T) {
	cfg := testConsoleAuditConfig(t)
	cfg.Policy.Enabled = false
	c := &consoleServer{base: New(cfg), jobs: newConsoleJobManager(context.Background())}

	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9787/api/v1/console/jobs/policy", nil)
	r.Host = "127.0.0.1:9787"
	r.SetPathValue("name", "policy")
	w := httptest.NewRecorder()
	c.startJob(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	records := readConsoleAuditRecords(t, cfg.Audit.Path)
	if len(records) != 2 {
		t.Fatalf("records=%d want 2", len(records))
	}
	if records[0].Action != "web-operation" || records[0].Target != "policy" || records[0].Status != "requested" {
		t.Fatalf("unexpected first record: %+v", records[0])
	}
	if records[1].Status != "rejected" {
		t.Fatalf("second status=%q want rejected", records[1].Status)
	}
	if got := records[0].Fields["actor"]; got != "local-browser" {
		t.Fatalf("actor=%v want local-browser", got)
	}
}

func TestConsoleOperationCompletionIsAudited(t *testing.T) {
	cfg := testConsoleAuditConfig(t)
	c := &consoleServer{base: New(cfg), jobs: newConsoleJobManager(context.Background())}

	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9787/api/v1/console/jobs/verify", nil)
	r.Host = "127.0.0.1:9787"
	r.SetPathValue("name", "verify")
	w := httptest.NewRecorder()
	c.startJob(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d want %d body=%s", w.Code, http.StatusAccepted, w.Body.String())
	}
	job := waitConsoleJobDone(t, c.jobs)

	records := waitConsoleAuditRecords(t, cfg.Audit.Path, 2)
	if records[0].Status != "requested" {
		t.Fatalf("first status=%q want requested", records[0].Status)
	}
	wantStatus := "success"
	if job.State == "failed" {
		wantStatus = "error"
	} else if job.State == "cancelled" {
		wantStatus = "cancelled"
	}
	if records[len(records)-1].Status != wantStatus {
		t.Fatalf("completion status=%q want %q (job=%s)", records[len(records)-1].Status, wantStatus, job.State)
	}
	if records[0].Fields["request_id"] != records[len(records)-1].Fields["request_id"] {
		t.Fatalf("request_id mismatch: %v vs %v", records[0].Fields["request_id"], records[len(records)-1].Fields["request_id"])
	}
}

func TestConsoleCancelIsAudited(t *testing.T) {
	cfg := testConsoleAuditConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &consoleServer{base: New(cfg), jobs: newConsoleJobManager(ctx)}
	_, err := c.jobs.Start("test", func(ctx context.Context, _ func(string)) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9787/api/v1/console/job/cancel", nil)
	r.Host = "127.0.0.1:9787"
	w := httptest.NewRecorder()
	c.cancelJob(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d want %d body=%s", w.Code, http.StatusAccepted, w.Body.String())
	}
	waitConsoleJobState(t, c.jobs, "cancelled")

	records := readConsoleAuditRecords(t, cfg.Audit.Path)
	if len(records) != 2 {
		t.Fatalf("records=%d want 2", len(records))
	}
	if records[0].Action != "web-cancel" || records[0].Status != "requested" {
		t.Fatalf("unexpected first record: %+v", records[0])
	}
	if records[1].Action != "web-cancel" || records[1].Status != "accepted" {
		t.Fatalf("unexpected second record: %+v", records[1])
	}
}

func TestConsoleRequiredAuditFailureBlocksMutation(t *testing.T) {
	cfg := config.Default()
	cfg.Backup.Root = t.TempDir()
	cfg.Policy.Enabled = false
	cfg.Audit.Enabled = true
	cfg.Audit.Required = true
	blockedParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Audit.Path = filepath.Join(blockedParent, "ledger.jsonl")
	cfg.Security.SignManifests = false
	c := &consoleServer{base: New(cfg), jobs: newConsoleJobManager(context.Background())}

	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9787/api/v1/console/jobs/policy", nil)
	r.Host = "127.0.0.1:9787"
	r.SetPathValue("name", "policy")
	w := httptest.NewRecorder()
	c.startJob(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want %d body=%s", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}
	if job := c.jobs.Snapshot(); job != nil {
		t.Fatalf("job should not start when required audit is unavailable: %+v", job)
	}
}

func testConsoleAuditConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Backup.Root = t.TempDir()
	cfg.Audit.Enabled = true
	cfg.Audit.Required = true
	cfg.Audit.Path = filepath.Join(t.TempDir(), "audit", "ledger.jsonl")
	cfg.Security.SignManifests = false
	return cfg
}

func readConsoleAuditRecords(t *testing.T, path string) []audit.Record {
	t.Helper()
	if n, err := audit.Verify(path); err != nil {
		t.Fatalf("verify audit: %v", err)
	} else if n == 0 {
		t.Fatal("audit ledger is empty")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var records []audit.Record
	s := bufio.NewScanner(f)
	for s.Scan() {
		var r audit.Record
		if err := json.Unmarshal(s.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		records = append(records, r)
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}

func waitConsoleAuditRecords(t *testing.T, path string, want int) []audit.Record {
	t.Helper()
	deadline := time.Now().Add(consoleTestWaitTimeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			records := readConsoleAuditRecords(t, path)
			if len(records) >= want {
				return records
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return readConsoleAuditRecords(t, path)
}

func waitConsoleJobDone(t *testing.T, m *consoleJobManager) *consoleJob {
	t.Helper()
	deadline := time.Now().Add(consoleTestWaitTimeout)
	for time.Now().Before(deadline) {
		job := m.Snapshot()
		if job != nil && job.State != "running" {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	job := m.Snapshot()
	if job == nil {
		t.Fatal("job missing")
	}
	t.Fatalf("job still running after timeout: %+v", job)
	return nil
}

func waitConsoleJobState(t *testing.T, m *consoleJobManager, want string) {
	t.Helper()
	deadline := time.Now().Add(consoleTestWaitTimeout)
	for time.Now().Before(deadline) {
		job := m.Snapshot()
		if job != nil && job.State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	job := m.Snapshot()
	if job == nil {
		t.Fatalf("job missing, want state %q", want)
	}
	t.Fatalf("job state=%q want %q", job.State, want)
}
