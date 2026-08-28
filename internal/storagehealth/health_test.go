package storagehealth

import "testing"

func TestProbe(t *testing.T) {
	r := Probe(t.TempDir(), Thresholds{})
	if r.Health != Healthy {
		t.Fatalf("probe failed: %#v", r)
	}
	if r.TotalBytes == 0 || r.FreeBytes == 0 {
		t.Fatalf("missing capacity: %#v", r)
	}
}
