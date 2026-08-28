package storagehealth

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	Healthy   = "healthy"
	Degraded  = "degraded"
	Unhealthy = "unhealthy"
)

type Report struct {
	Root        string    `json:"root"`
	Health      string    `json:"health"`
	TotalBytes  uint64    `json:"total_bytes"`
	FreeBytes   uint64    `json:"free_bytes"`
	FreePercent float64   `json:"free_percent"`
	ProbeMS     int64     `json:"probe_ms"`
	Error       string    `json:"error,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
}

type Thresholds struct {
	MinFreeBytes   uint64
	MinFreePercent float64
	MaxProbe       time.Duration
}

func Probe(root string, th Thresholds) Report {
	start := time.Now()
	r := Report{Root: root, Health: Healthy, CheckedAt: start.UTC()}
	if err := os.MkdirAll(root, 0o700); err != nil {
		r.Health, r.Error = Unhealthy, err.Error()
		return r
	}
	total, free, err := diskSpace(root)
	if err != nil {
		r.Health, r.Error = Unhealthy, err.Error()
		return r
	}
	r.TotalBytes, r.FreeBytes = total, free
	if total > 0 {
		r.FreePercent = float64(free) * 100 / float64(total)
	}
	// A small write+fsync+read probe catches read-only mounts and many common
	// storage failures without walking the repository tree.
	name := filepath.Join(root, ".repoark-storage-probe")
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err == nil {
		_, err = f.Write(buf)
	}
	if err == nil {
		err = f.Sync()
	}
	if f != nil {
		if e := f.Close(); err == nil {
			err = e
		}
	}
	if err == nil {
		got, e := os.ReadFile(name)
		if e != nil {
			err = e
		} else if string(got) != string(buf) {
			err = fmt.Errorf("storage probe readback mismatch")
		}
	}
	_ = os.Remove(name)
	r.ProbeMS = time.Since(start).Milliseconds()
	if err != nil {
		r.Health, r.Error = Unhealthy, err.Error()
		return r
	}
	if th.MinFreeBytes > 0 && free < th.MinFreeBytes {
		r.Health = Degraded
	}
	if th.MinFreePercent > 0 && r.FreePercent < th.MinFreePercent {
		r.Health = Degraded
	}
	if th.MaxProbe > 0 && time.Duration(r.ProbeMS)*time.Millisecond > th.MaxProbe {
		r.Health = Degraded
	}
	return r
}
