package observability

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const consoleSSEKeepalive = 15 * time.Second

// events streams complete immutable job snapshots whenever the job revision
// changes. Complete snapshots make reconnects and dropped intermediate log
// updates harmless: the browser always converges to current server state.
func (c *consoleServer) events(w http.ResponseWriter, r *http.Request) {
	if !c.authorizeRead(w, r) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	// The console server uses a finite WriteTimeout for normal API handlers.
	// An SSE stream is intentionally long-lived, so clear the per-response
	// write deadline while retaining all other server timeouts.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()

	seq := ^uint64(0) // force an initial snapshot on a fresh connection
	if raw := strings.TrimSpace(r.Header.Get("Last-Event-ID")); raw != "" {
		if parsed, err := strconv.ParseUint(raw, 10, 64); err == nil {
			seq = parsed
		}
	}
	keepalive := time.NewTicker(consoleSSEKeepalive)
	defer keepalive.Stop()

	for {
		job, next, changed := c.jobs.Observe(seq)
		if changed == nil {
			payload, err := json.Marshal(map[string]any{"job": job})
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: job\ndata: %s\n\n", next, payload); err != nil {
				return
			}
			flusher.Flush()
			seq = next
			continue
		}

		select {
		case <-r.Context().Done():
			return
		case <-changed:
			continue
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
