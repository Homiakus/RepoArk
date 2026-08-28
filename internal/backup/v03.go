package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Homiakus/repoark/internal/githubapi"
)

type actionArtifactRecord struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Expired   bool   `json:"expired"`
	Size      int64  `json:"size_bytes"`
	Path      string `json:"path,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (m *Manager) backupActionArtifacts(ctx context.Context, repo githubapi.Repository, owner, name string) (int, []string) {
	artifacts, err := m.GitHub.ActionArtifacts(ctx, repo.FullName)
	if err != nil {
		return 0, []string{"actions artifacts: " + err.Error()}
	}
	base := filepath.Join(m.Config.Backup.Root, "actions-artifacts", owner, name)
	metaDir := filepath.Join(m.Config.Backup.Root, "metadata", owner, name)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return 0, []string{"actions artifacts: " + err.Error()}
	}
	if err := os.MkdirAll(metaDir, 0o700); err != nil {
		return 0, []string{"actions artifacts metadata: " + err.Error()}
	}
	// Stable ordering keeps the index deterministic between identical API views.
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].ID < artifacts[j].ID })
	records := make([]actionArtifactRecord, 0, len(artifacts))
	warnings := []string{}
	count := 0
	for _, a := range artifacts {
		r := actionArtifactRecord{ID: a.ID, Name: a.Name, Expired: a.Expired, Size: a.SizeInBytes}
		if !a.CreatedAt.IsZero() {
			r.CreatedAt = a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		if !a.ExpiresAt.IsZero() {
			r.ExpiresAt = a.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		if a.Expired {
			r.Error = "expired before backup"
			records = append(records, r)
			continue
		}
		filename := strconv.FormatInt(a.ID, 10) + "-" + safe(a.Name) + ".zip"
		dst := filepath.Join(base, filename)
		if b, err := os.ReadFile(dst + ".sha256"); err == nil {
			fields := strings.Fields(string(b))
			if len(fields) > 0 {
				if sum, sumErr := sha256File(dst); sumErr == nil && strings.EqualFold(sum, fields[0]) {
					rel, _ := filepath.Rel(m.Config.Backup.Root, dst)
					r.Path, r.SHA256 = filepath.ToSlash(rel), sum
					count++
					records = append(records, r)
					continue
				}
			}
		}
		if err := m.GitHub.DownloadActionArtifact(ctx, repo.FullName, a, dst, m.Config.GitHub.MaxArtifactBytes); err != nil {
			r.Error = err.Error()
			warnings = append(warnings, fmt.Sprintf("actions artifact %d (%s): %v", a.ID, a.Name, err))
			records = append(records, r)
			continue
		}
		sum, err := sha256File(dst)
		if err != nil {
			r.Error = err.Error()
			warnings = append(warnings, fmt.Sprintf("actions artifact %d checksum: %v", a.ID, err))
			records = append(records, r)
			continue
		}
		rel, _ := filepath.Rel(m.Config.Backup.Root, dst)
		r.Path = filepath.ToSlash(rel)
		r.SHA256 = sum
		_ = os.WriteFile(dst+".sha256", []byte(sum+"  "+filepath.Base(dst)+"\n"), 0o600)
		count++
		records = append(records, r)
	}
	b, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return count, append(warnings, "actions artifacts index: "+err.Error())
	}
	if err := os.WriteFile(filepath.Join(metaDir, "actions_artifacts.json"), b, 0o600); err != nil {
		return count, append(warnings, "actions artifacts index: "+err.Error())
	}
	return count, warnings
}

func (m *Manager) backupProjectsV2(ctx context.Context, owner string) error {
	raw, err := m.GitHub.ProjectsV2ForOwner(ctx, owner)
	if err != nil {
		return err
	}
	dir := filepath.Join(m.Config.Backup.Root, "metadata", "_account", "projects-v2")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	name := safe(strings.ToLower(owner)) + ".json"
	return os.WriteFile(filepath.Join(dir, name), raw, 0o600)
}
