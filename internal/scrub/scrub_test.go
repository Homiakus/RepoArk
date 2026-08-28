package scrub

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Homiakus/repoark/internal/cas"
)

func TestScrubDetectsAndRepairsCASObject(t *testing.T) {
	root := t.TempDir()
	st := cas.New(filepath.Join(root, "cas"), 0)
	p := filepath.Join(root, "x.bin")
	original := []byte("immutable payload")
	if err := os.WriteFile(p, original, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := st.Ingest(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.BlobPath, []byte("tamper"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixed := false
	s := Scrubber{CAS: st, SampleObjects: 10, Now: func() time.Time { return time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC) }, Repair: func(_ context.Context, digest, path string) error {
		fixed = true
		return os.WriteFile(path, original, 0o600)
	}}
	got, err := s.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !fixed || got.Corrupt != 1 || got.Repaired != 1 || got.Failed != 0 {
		t.Fatalf("result=%+v fixed=%v", got, fixed)
	}
}

func TestScrubSamplingIsBounded(t *testing.T) {
	root := t.TempDir()
	st := cas.New(filepath.Join(root, "cas"), 0)
	for i := 0; i < 20; i++ {
		p := filepath.Join(root, fmt.Sprintf("%d.bin", i))
		_ = os.WriteFile(p, []byte(fmt.Sprintf("payload-%d", i)), 0o600)
		if _, err := st.Ingest(p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := (Scrubber{CAS: st, SampleObjects: 3, Now: func() time.Time { return time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC) }}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Sampled != 3 {
		t.Fatalf("sampled=%d", got.Sampled)
	}
}
