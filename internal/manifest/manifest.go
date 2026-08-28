package manifest

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/signing"
)

type RepoResult struct {
	FullName                string    `json:"full_name"`
	SourceVisibility        string    `json:"source_visibility,omitempty"`
	MirrorPath              string    `json:"mirror_path"`
	BundlePath              string    `json:"bundle_path,omitempty"`
	BundleSHA256            string    `json:"bundle_sha256,omitempty"`
	UpdatedAt               time.Time `json:"updated_at"`
	DurationMS              int64     `json:"duration_ms"`
	Verified                bool      `json:"verified"`
	LFSFetched              bool      `json:"lfs_fetched"`
	LFSArchivePath          string    `json:"lfs_archive_path,omitempty"`
	LFSArchiveSHA256        string    `json:"lfs_archive_sha256,omitempty"`
	WikiBackedUp            bool      `json:"wiki_backed_up"`
	MetadataBackedUp        bool      `json:"metadata_backed_up"`
	ReleaseAssetsBackedUp   int       `json:"release_assets_backed_up,omitempty"`
	DiscussionsBackedUp     bool      `json:"discussions_backed_up,omitempty"`
	PackagesBackedUp        int       `json:"packages_backed_up,omitempty"`
	PackagePayloadsBackedUp int       `json:"package_payloads_backed_up,omitempty"`
	OCIArtifactsBackedUp    int       `json:"oci_artifacts_backed_up,omitempty"`
	ActionArtifactsBackedUp int       `json:"action_artifacts_backed_up,omitempty"`
	Error                   string    `json:"error,omitempty"`
	Warnings                []string  `json:"warnings,omitempty"`
}

type CASStats struct {
	Objects        int   `json:"objects,omitempty"`
	PhysicalBytes  int64 `json:"physical_bytes,omitempty"`
	LogicalFiles   int   `json:"logical_files,omitempty"`
	LogicalBytes   int64 `json:"logical_bytes,omitempty"`
	ReclaimedBytes int64 `json:"reclaimed_bytes,omitempty"`
}

type Manifest struct {
	Version                  int          `json:"version"`
	StartedAt                time.Time    `json:"started_at"`
	EndedAt                  time.Time    `json:"ended_at"`
	GitHubUser               string       `json:"github_user"`
	Repositories             []RepoResult `json:"repositories"`
	Succeeded                int          `json:"succeeded"`
	Failed                   int          `json:"failed"`
	WarningCount             int          `json:"warning_count,omitempty"`
	ProjectsV2OwnersBackedUp int          `json:"projects_v2_owners_backed_up,omitempty"`
	CAS                      CASStats     `json:"cas,omitempty"`
	Warnings                 []string     `json:"warnings,omitempty"`
	SignatureType            string       `json:"signature_type,omitempty"`
}

func Bytes(m Manifest) ([]byte, error) { return json.MarshalIndent(m, "", "  ") }

func Write(root string, m Manifest) (string, error) {
	return write(root, m, nil, nil)
}

func WriteSigned(root string, m Manifest, keyPath string) (string, error) {
	priv, pub, err := signing.EnsureKey(keyPath)
	if err != nil {
		return "", err
	}
	m.SignatureType = "ed25519-detached-v1"
	return write(root, m, priv, pub)
}

func write(root string, m Manifest, priv ed25519.PrivateKey, pub ed25519.PublicKey) (string, error) {
	dir := filepath.Join(root, "manifests")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := m.StartedAt.UTC().Format("20060102T150405Z") + ".json"
	path := filepath.Join(dir, name)
	b, err := Bytes(m)
	if err != nil {
		return "", err
	}
	if err := atomicWrite(path, b, 0o600); err != nil {
		return "", err
	}
	latest := filepath.Join(dir, "latest.json")
	if err := atomicWrite(latest, b, 0o600); err != nil {
		return "", err
	}
	if len(priv) > 0 {
		sig := []byte(signing.Sign(priv, b) + "\n")
		if err := atomicWrite(path+".sig", sig, 0o600); err != nil {
			return "", err
		}
		if err := atomicWrite(latest+".sig", sig, 0o600); err != nil {
			return "", err
		}
		if err := signing.WritePublic(filepath.Join(dir, "manifest-ed25519.pub"), pub); err != nil {
			return "", err
		}
	}
	return path, nil
}

func ReadLatest(root string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(root, "manifests", "latest.json"))
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(b, &m)
	return m, err
}

func VerifyLatestSignature(root, trustedPublicKeyPath string) error {
	dir := filepath.Join(root, "manifests")
	data, err := os.ReadFile(filepath.Join(dir, "latest.json"))
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(filepath.Join(dir, "latest.json.sig"))
	if err != nil {
		return err
	}
	pub, err := signing.LoadPublic(trustedPublicKeyPath)
	if err != nil {
		return err
	}
	return signing.Verify(pub, data, strings.TrimSpace(string(sig)))
}

func Prune(root string, keep int) error {
	if keep <= 0 {
		return nil
	}
	dir := filepath.Join(root, "manifests")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && e.Name() != "latest.json" && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) <= keep {
		return nil
	}
	for _, n := range names[:len(names)-keep] {
		_ = os.Remove(filepath.Join(dir, n))
		_ = os.Remove(filepath.Join(dir, n+".sig"))
	}
	return nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".repoark-manifest-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
	if err := os.Rename(tmpName, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		if retryErr := os.Rename(tmpName, path); retryErr != nil {
			return retryErr
		}
	}
	return nil
}
