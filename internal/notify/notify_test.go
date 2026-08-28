package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Homiakus/repoark/internal/config"
)

func TestWebhookReadsEndpointFromEnvironment(t *testing.T) {
	var got struct {
		Source  string `json:"source"`
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.Notifications.Enabled = true
	cfg.Notifications.OnSuccess = true
	cfg.Notifications.WebhookEnv = "REPOARK_TEST_WEBHOOK"
	t.Setenv("REPOARK_TEST_WEBHOOK", srv.URL)
	if err := Send(context.Background(), cfg, "cycle ok", true); err != nil {
		t.Fatal(err)
	}
	if got.Source != "repoark" || !got.Success || got.Message != "cycle ok" {
		t.Fatalf("unexpected payload: %+v", got)
	}
}
