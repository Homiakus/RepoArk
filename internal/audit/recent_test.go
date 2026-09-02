package audit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecentReturnsNewestMatchingRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "ledger.jsonl")
	for i, tc := range []struct {
		action string
		status string
	}{
		{"web-operation", "requested"},
		{"other", "ok"},
		{"web-operation", "success"},
		{"web-operation", "requested"},
		{"web-operation", "error"},
	} {
		if _, err := Append(path, tc.action, "op", tc.status, "", map[string]any{"i": i}); err != nil {
			t.Fatal(err)
		}
	}

	records, err := Recent(path, 3, "web-operation")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("records=%d want 3", len(records))
	}
	if records[0].Status != "error" || records[1].Status != "requested" || records[2].Status != "success" {
		t.Fatalf("unexpected newest-first statuses: %q %q %q", records[0].Status, records[1].Status, records[2].Status)
	}
}

func TestRecentRejectsTamperedLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	if _, err := Append(path, "web-operation", "backup", "requested", "", nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range b {
		if b[i] == 'b' {
			b[i] = 'x'
			break
		}
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Recent(path, 10, "web-operation"); err == nil {
		t.Fatal("Recent accepted a tampered audit ledger")
	}
}

func TestRecentIfExistsReturnsEmptyForMissingLedger(t *testing.T) {
	records, err := RecentIfExists(filepath.Join(t.TempDir(), "missing.jsonl"), 10, "web-operation")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("records=%d want 0", len(records))
	}
}
