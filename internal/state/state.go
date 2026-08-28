package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type Record struct {
	Kind      string         `json:"kind"`
	OK        bool           `json:"ok"`
	StartedAt time.Time      `json:"started_at,omitempty"`
	EndedAt   time.Time      `json:"ended_at"`
	Detail    string         `json:"detail,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

func Write(path string, r Record) error {
	if r.EndedAt.IsZero() {
		r.EndedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".repoark-state-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func Read(path string) (Record, error) {
	var r Record
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, err
	}
	return r, nil
}

func Age(path string, now time.Time) (time.Duration, bool, error) {
	r, err := Read(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if r.EndedAt.IsZero() {
		return 0, false, nil
	}
	return now.Sub(r.EndedAt), true, nil
}
