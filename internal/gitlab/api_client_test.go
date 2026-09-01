package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnsureGroupCreatesOnlyAfterNotFound(t *testing.T) {
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/groups/team":
			http.Error(w, "missing", http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v4/groups":
			posts++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprint(w, `{"id":42,"name":"Team","path":"team","full_path":"team"}`)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := apiClient{base: srv.URL, token: "test", http: srv.Client()}
	g, err := c.ensureGroup(context.Background(), "team", "Team")
	if err != nil {
		t.Fatal(err)
	}
	if g.ID != 42 || posts != 1 {
		t.Fatalf("group=%+v posts=%d", g, posts)
	}
}

func TestEnsureGroupFailsClosedOnUnauthorizedLookup(t *testing.T) {
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := apiClient{base: srv.URL, token: "bad", http: srv.Client()}
	_, err := c.ensureGroup(context.Background(), "team", "Team")
	if err == nil {
		t.Fatal("expected unauthorized lookup to fail")
	}
	var statusErr *apiStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected typed 401 error, got %T: %v", err, err)
	}
	if posts != 0 {
		t.Fatalf("unexpected create attempt after unauthorized lookup: posts=%d", posts)
	}
}

func TestFindProjectByFullPathDoesNotMaskForbiddenLookup(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	c := apiClient{base: srv.URL, token: "bad", http: srv.Client()}
	_, found, err := c.findProjectByFullPath(context.Background(), "team/project")
	if err == nil || found {
		t.Fatalf("expected forbidden lookup failure, found=%v err=%v", found, err)
	}
	var statusErr *apiStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected typed 403 error, got %T: %v", err, err)
	}
	if requests != 1 {
		t.Fatalf("forbidden lookup unexpectedly fell back to search: requests=%d", requests)
	}
}
