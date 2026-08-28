package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/config"
)

func Send(ctx context.Context, cfg config.Config, message string, success bool) error {
	n := cfg.Notifications
	if !n.Enabled || (success && !n.OnSuccess) || (!success && !n.OnFailure) {
		return nil
	}
	var errs []string
	webhook := ""
	if n.WebhookEnv != "" {
		webhook = strings.TrimSpace(os.Getenv(n.WebhookEnv))
	}
	if webhook != "" {
		if err := sendWebhook(ctx, webhook, message, success); err != nil {
			errs = append(errs, "webhook: "+err.Error())
		}
	}
	token := strings.TrimSpace(os.Getenv(n.TelegramToken))
	chat := strings.TrimSpace(os.Getenv(n.TelegramChatID))
	if token != "" && chat != "" {
		if err := sendTelegram(ctx, token, chat, message); err != nil {
			errs = append(errs, "telegram: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("notifications: %s", strings.Join(errs, "; "))
	}
	return nil
}

func sendWebhook(ctx context.Context, endpoint, message string, success bool) error {
	body, _ := json.Marshal(map[string]any{"source": "repoark", "success": success, "message": message, "timestamp": time.Now().UTC().Format(time.RFC3339)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return do(req)
}

func sendTelegram(ctx context.Context, token, chat, message string) error {
	form := url.Values{"chat_id": {chat}, "text": {message}, "disable_web_page_preview": {"true"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+token+"/sendMessage", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return do(req)
}

func do(req *http.Request) error {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}
