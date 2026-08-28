package generation

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/execx"
	"github.com/Homiakus/repoark/internal/manifest"
	"github.com/Homiakus/repoark/internal/signing"
)

type Meta struct {
	Version          int       `json:"version"`
	ID               string    `json:"id"`
	Repository       string    `json:"repository"`
	CreatedAt        time.Time `json:"created_at"`
	SourceUpdatedAt  time.Time `json:"source_updated_at,omitempty"`
	Verified         bool      `json:"verified"`
	BundlePath       string    `json:"bundle_path,omitempty"`
	BundleSHA256     string    `json:"bundle_sha256,omitempty"`
	LFSPath          string    `json:"lfs_path,omitempty"`
	LFSSHA256        string    `json:"lfs_sha256,omitempty"`
	MirrorArchive    string    `json:"mirror_archive,omitempty"`
	MirrorSHA256     string    `json:"mirror_sha256,omitempty"`
	SourceMirrorPath string    `json:"source_mirror_path,omitempty"`
	SignatureType    string    `json:"signature_type,omitempty"`
}

func Capture(backupRoot, generationRoot string, r manifest.RepoResult, keep int) (Meta, error) {
	if strings.TrimSpace(r.FullName) == "" {
		return Meta{}, errors.New("generation repository is empty")
	}
	stamp := time.Now().UTC()
	id := stamp.Format("20060102T150405.000000000Z")
	owner, repo, ok := strings.Cut(r.FullName, "/")
	if !ok || owner == "" || repo == "" {
		return Meta{}, fmt.Errorf("invalid repository %q", r.FullName)
	}
	dir := filepath.Join(generationRoot, owner, repo, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Meta{}, err
	}
	m := Meta{Version: 1, ID: id, Repository: r.FullName, CreatedAt: stamp, SourceUpdatedAt: r.UpdatedAt, Verified: r.Verified, SourceMirrorPath: r.MirrorPath}

	if r.BundlePath != "" {
		src := resolve(backupRoot, r.BundlePath)
		dst := filepath.Join(dir, "repo.bundle")
		if err := linkOrCopy(src, dst); err != nil {
			return Meta{}, err
		}
		sum, err := fileSHA256(dst)
		if err != nil {
			return Meta{}, err
		}
		if r.BundleSHA256 != "" && sum != r.BundleSHA256 {
			return Meta{}, errors.New("generation bundle checksum changed during capture")
		}
		m.BundlePath = "repo.bundle"
		m.BundleSHA256 = sum
	} else {
		mirror := resolve(backupRoot, r.MirrorPath)
		dst := filepath.Join(dir, "mirror.tar.gz")
		if err := archiveDir(mirror, dst); err != nil {
			return Meta{}, err
		}
		sum, err := fileSHA256(dst)
		if err != nil {
			return Meta{}, err
		}
		m.MirrorArchive = "mirror.tar.gz"
		m.MirrorSHA256 = sum
	}
	if r.LFSArchivePath != "" {
		src := resolve(backupRoot, r.LFSArchivePath)
		dst := filepath.Join(dir, "lfs.tar.gz")
		if err := linkOrCopy(src, dst); err != nil {
			return Meta{}, err
		}
		sum, err := fileSHA256(dst)
		if err != nil {
			return Meta{}, err
		}
		if r.LFSArchiveSHA256 != "" && sum != r.LFSArchiveSHA256 {
			return Meta{}, errors.New("generation LFS checksum changed during capture")
		}
		m.LFSPath = "lfs.tar.gz"
		m.LFSSHA256 = sum
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Meta{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "generation.json"), b, 0o600); err != nil {
		return Meta{}, err
	}
	if keep > 0 {
		_ = prune(filepath.Join(generationRoot, owner, repo), keep)
	}
	return m, nil
}

func CaptureSigned(backupRoot, generationRoot string, r manifest.RepoResult, keep int, keyPath string) (Meta, error) {
	m, err := Capture(backupRoot, generationRoot, r, keep)
	if err != nil {
		return m, err
	}
	_, dir, err := Find(generationRoot, r.FullName, m.ID)
	if err != nil {
		return m, err
	}
	m.SignatureType = "ed25519-detached-v1"
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, err
	}
	metaPath := filepath.Join(dir, "generation.json")
	if err := os.WriteFile(metaPath, b, 0o600); err != nil {
		return m, err
	}
	priv, _, err := signing.EnsureKey(keyPath)
	if err != nil {
		return m, err
	}
	if err := os.WriteFile(metaPath+".sig", []byte(signing.Sign(priv, b)+"\n"), 0o600); err != nil {
		return m, err
	}
	return m, nil
}

func VerifySignature(dir, trustedPublicKeyPath string) error {
	data, err := os.ReadFile(filepath.Join(dir, "generation.json"))
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(filepath.Join(dir, "generation.json.sig"))
	if err != nil {
		return err
	}
	pub, err := signing.LoadPublic(trustedPublicKeyPath)
	if err != nil {
		return err
	}
	return signing.Verify(pub, data, strings.TrimSpace(string(sig)))
}

func RestoreVerified(ctx context.Context, root, fullName, id, target, trustedPublicKeyPath string) error {
	_, dir, err := Find(root, fullName, id)
	if err != nil {
		return err
	}
	if err := VerifySignature(dir, trustedPublicKeyPath); err != nil {
		return fmt.Errorf("generation signature: %w", err)
	}
	return Restore(ctx, root, fullName, id, target)
}

func List(root, fullName string) ([]Meta, error) {
	owner, repo, ok := strings.Cut(fullName, "/")
	if !ok {
		return nil, fmt.Errorf("invalid repository %q", fullName)
	}
	base := filepath.Join(root, owner, repo)
	ents, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Meta, 0, len(ents))
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		m, err := Read(filepath.Join(base, e.Name()))
		if err == nil {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func Read(dir string) (Meta, error) {
	var m Meta
	b, err := os.ReadFile(filepath.Join(dir, "generation.json"))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	return m, nil
}

func Find(root, fullName, id string) (Meta, string, error) {
	owner, repo, ok := strings.Cut(fullName, "/")
	if !ok {
		return Meta{}, "", fmt.Errorf("invalid repository %q", fullName)
	}
	if id == "" || id == "latest" {
		all, err := List(root, fullName)
		if err != nil || len(all) == 0 {
			if err != nil {
				return Meta{}, "", err
			}
			return Meta{}, "", os.ErrNotExist
		}
		id = all[0].ID
	}
	dir := filepath.Join(root, owner, repo, id)
	m, err := Read(dir)
	return m, dir, err
}

func Restore(ctx context.Context, root, fullName, id, target string) error {
	m, dir, err := Find(root, fullName, id)
	if err != nil {
		return err
	}
	if target == "" {
		_, repo, _ := strings.Cut(fullName, "/")
		target = repo
	}
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("restore target already exists: %s", target)
	}
	if m.BundlePath != "" {
		bundle := filepath.Join(dir, m.BundlePath)
		if err := verifyFile(bundle, m.BundleSHA256); err != nil {
			return err
		}
		if _, err := execx.Run(ctx, "", nil, "git", "clone", bundle, target); err != nil {
			return err
		}
	} else if m.MirrorArchive != "" {
		archive := filepath.Join(dir, m.MirrorArchive)
		if err := verifyFile(archive, m.MirrorSHA256); err != nil {
			return err
		}
		tmp, err := os.MkdirTemp("", "repoark-generation-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		if err := extractTarGz(archive, tmp); err != nil {
			return err
		}
		mirror := filepath.Join(tmp, "mirror.git")
		if _, err := execx.Run(ctx, "", nil, "git", "clone", mirror, target); err != nil {
			return err
		}
	} else {
		return errors.New("generation has neither bundle nor mirror archive")
	}
	if m.LFSPath != "" {
		archive := filepath.Join(dir, m.LFSPath)
		if err := verifyFile(archive, m.LFSSHA256); err != nil {
			return err
		}
		gitDir := filepath.Join(target, ".git")
		if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
			if err := extractTarGz(archive, filepath.Join(gitDir, "lfs")); err != nil {
				return err
			}
		}
	}
	return nil
}

func prune(base string, keep int) error {
	ents, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		if err := os.RemoveAll(filepath.Join(base, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

func resolve(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, filepath.FromSlash(p))
}

func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err = out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func archiveDir(src, dst string) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	err = filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(filepath.Join("mirror.git", rel))
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		}
		return nil
	})
	if cerr := tw.Close(); err == nil {
		err = cerr
	}
	if cerr := gz.Close(); err == nil {
		err = cerr
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

func extractTarGz(path, dst string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(h.Name)
		if name == "." || filepath.IsAbs(name) || strings.HasPrefix(name, ".."+string(os.PathSeparator)) {
			return errors.New("unsafe archive path")
		}
		out := filepath.Join(dst, name)
		if !strings.HasPrefix(filepath.Clean(out), filepath.Clean(dst)+string(os.PathSeparator)) {
			return errors.New("archive path escaped target")
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(out, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
				return err
			}
			w, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(h.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(w, tr); err != nil {
				_ = w.Close()
				return err
			}
			if err := w.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}

func verifyFile(path, want string) error {
	got, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if want != "" && got != want {
		return fmt.Errorf("checksum mismatch for %s", path)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyDir validates a replicated generation without restoring it. It verifies
// the detached metadata signature when trustedPublicKeyPath is provided and
// then validates every payload checksum described by generation.json.
func VerifyDir(dir, trustedPublicKeyPath string) (Meta, error) {
	m, err := Read(dir)
	if err != nil {
		return Meta{}, err
	}
	if trustedPublicKeyPath != "" {
		if err := VerifySignature(dir, trustedPublicKeyPath); err != nil {
			return Meta{}, fmt.Errorf("generation signature: %w", err)
		}
	}
	if m.BundlePath != "" {
		if err := verifyFile(filepath.Join(dir, m.BundlePath), m.BundleSHA256); err != nil {
			return Meta{}, err
		}
	}
	if m.MirrorArchive != "" {
		if err := verifyFile(filepath.Join(dir, m.MirrorArchive), m.MirrorSHA256); err != nil {
			return Meta{}, err
		}
	}
	if m.LFSPath != "" {
		if err := verifyFile(filepath.Join(dir, m.LFSPath), m.LFSSHA256); err != nil {
			return Meta{}, err
		}
	}
	return m, nil
}
