package scrub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/Homiakus/repoark/internal/cas"
)

type Result struct {
	StartedAt         time.Time `json:"started_at"`
	EndedAt           time.Time `json:"ended_at"`
	Available         int       `json:"available_objects"`
	Sampled           int       `json:"sampled_objects"`
	Healthy           int       `json:"healthy_objects"`
	Corrupt           int       `json:"corrupt_objects"`
	Repaired          int       `json:"repaired_objects"`
	Failed            int       `json:"failed_objects"`
	Digests           []string  `json:"corrupt_digests,omitempty"`
	UnrepairedDigests []string  `json:"unrepaired_digests,omitempty"`
	DurationMS        int64     `json:"duration_ms"`
}

type RepairFunc func(context.Context, string, string) error

type Scrubber struct {
	CAS           *cas.Store
	SampleObjects int
	SeedSalt      string
	Repair        RepairFunc
	Now           func() time.Time
}

func (s Scrubber) Run(ctx context.Context) (Result, error) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	out := Result{StartedAt: now().UTC()}
	if s.CAS == nil {
		return out, fmt.Errorf("CAS store is required")
	}
	objects, err := s.CAS.ListObjects()
	if err != nil {
		return out, err
	}
	out.Available = len(objects)
	limit := s.SampleObjects
	if limit <= 0 || limit > len(objects) {
		limit = len(objects)
	}
	seed := now().UTC().Format("2006-01-02") + "\x00" + s.SeedSalt
	type ranked struct {
		obj   cas.ObjectInfo
		score string
	}
	rankedObjects := make([]ranked, 0, len(objects))
	for _, o := range objects {
		h := sha256.Sum256([]byte(seed + "\x00" + o.Digest))
		rankedObjects = append(rankedObjects, ranked{obj: o, score: hex.EncodeToString(h[:])})
	}
	sort.Slice(rankedObjects, func(i, j int) bool {
		if rankedObjects[i].score == rankedObjects[j].score {
			return rankedObjects[i].obj.Digest < rankedObjects[j].obj.Digest
		}
		return rankedObjects[i].score < rankedObjects[j].score
	})
	for i := 0; i < limit; i++ {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}
		o := rankedObjects[i].obj
		out.Sampled++
		got, _, hashErr := cas.HashFile(o.Path)
		if hashErr == nil && got == o.Digest {
			out.Healthy++
			continue
		}
		out.Corrupt++
		out.Digests = append(out.Digests, o.Digest)
		if s.Repair == nil {
			out.Failed++
			out.UnrepairedDigests = append(out.UnrepairedDigests, o.Digest)
			continue
		}
		if err := s.Repair(ctx, o.Digest, o.Path); err != nil {
			out.Failed++
			out.UnrepairedDigests = append(out.UnrepairedDigests, o.Digest)
			continue
		}
		got, _, err := cas.HashFile(o.Path)
		if err == nil && got == o.Digest {
			out.Repaired++
			continue
		}
		out.Failed++
		out.UnrepairedDigests = append(out.UnrepairedDigests, o.Digest)
	}
	out.EndedAt = now().UTC()
	out.DurationMS = out.EndedAt.Sub(out.StartedAt).Milliseconds()
	if out.Failed > 0 {
		return out, fmt.Errorf("scrub found %d unrepaired corrupt objects", out.Failed)
	}
	return out, nil
}
