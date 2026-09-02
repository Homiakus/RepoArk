package observability

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/config"
)

func TestConsoleJobObserveBroadcasts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newConsoleJobManager(ctx)

	job, seq, changed := m.Observe(^uint64(0))
	if job != nil || changed != nil {
		t.Fatalf("initial observation job=%v changed=%v want nil snapshot notification", job, changed)
	}
	_, sameSeq, changed := m.Observe(seq)
	if sameSeq != seq || changed == nil {
		t.Fatalf("unchanged observation seq=%d changed=%v", sameSeq, changed)
	}

	gate := make(chan struct{})
	if _, err := m.Start("stream-test", func(context.Context, func(string)) error {
		<-gate
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("job mutation did not broadcast")
	}

	job, next, changed := m.Observe(seq)
	if changed != nil || job == nil || job.State != "running" || next <= seq {
		t.Fatalf("observation job=%+v next=%d changed=%v", job, next, changed)
	}
	close(gate)
	waitConsoleJobState(t, m, "succeeded")
}

func TestConsoleSSEStreamsInitialAndJobUpdate(t *testing.T) {
	cfg := config.Default()
	cfg.Backup.Root = t.TempDir()
	cfg.Audit.Enabled = false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &consoleServer{base: New(cfg), jobs: newConsoleJobManager(ctx)}
	mux := http.NewServeMux()
	c.registerRoutes(mux)
	ts := httptest.NewServer(consoleSecurityHeaders(mux))
	defer ts.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/console/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content-type=%q", got)
	}
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering=%q want no", got)
	}

	reader := bufio.NewReader(resp.Body)
	first := readConsoleSSEEvent(t, reader)
	if first.Event != "job" || first.ID != "0" {
		t.Fatalf("initial event=%+v", first)
	}
	var firstBody struct {
		Job *consoleJob `json:"job"`
	}
	if err := json.Unmarshal([]byte(first.Data), &firstBody); err != nil {
		t.Fatal(err)
	}
	if firstBody.Job != nil {
		t.Fatalf("initial job=%+v want nil", firstBody.Job)
	}

	gate := make(chan struct{})
	if _, err := c.jobs.Start("stream-test", func(context.Context, func(string)) error {
		<-gate
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	second := readConsoleSSEEvent(t, reader)
	var secondBody struct {
		Job *consoleJob `json:"job"`
	}
	if err := json.Unmarshal([]byte(second.Data), &secondBody); err != nil {
		t.Fatal(err)
	}
	if second.Event != "job" || second.ID == first.ID || secondBody.Job == nil || secondBody.Job.State != "running" {
		t.Fatalf("job event=%+v body=%+v", second, secondBody.Job)
	}
	close(gate)
	waitConsoleJobState(t, c.jobs, "succeeded")
}

type consoleSSEEvent struct {
	ID    string
	Event string
	Data  string
}

func readConsoleSSEEvent(t *testing.T, r *bufio.Reader) consoleSSEEvent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var out consoleSSEEvent
	for time.Now().Before(deadline) {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "id: "):
			out.ID = strings.TrimSpace(strings.TrimPrefix(line, "id: "))
		case strings.HasPrefix(line, "event: "):
			out.Event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			out.Data = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		case line == "" && out.Event != "":
			return out
		}
	}
	t.Fatal("timed out waiting for SSE event")
	return consoleSSEEvent{}
}
