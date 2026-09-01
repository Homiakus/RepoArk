package gitlab

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestWaitGitLabHTTPUsesExternalSignInProbe(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/-/health", func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	})
	mux.HandleFunc("/users/sign_in", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Handler: mux}
	defer srv.Close()
	go func() {
		_ = srv.Serve(ln)
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitGitLabHTTP(ctx, port); err != nil {
		t.Fatalf("waitGitLabHTTP failed while sign-in page was ready: %v", err)
	}
}
