package observability

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

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

func waitConsoleJobState(t *testing.T, m *consoleJobManager, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
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
