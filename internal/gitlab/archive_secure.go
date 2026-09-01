package gitlab

import (
	"archive/tar"
	"compress/gzip"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func verifyGitLabBackupArchive(archive, configuredImage string) error {
	meta, err := readBackupMeta(archive + ".meta.json")
	if err != nil {
		return fmt.Errorf("read GitLab backup metadata: %w", err)
	}
	if meta.Archive != "" && meta.Archive != filepath.Base(archive) {
		return fmt.Errorf("GitLab backup metadata archive mismatch: metadata=%s actual=%s", meta.Archive, filepath.Base(archive))
	}
	if meta.Image != "" && configuredImage != "" && meta.Image != configuredImage {
		return fmt.Errorf("GitLab restore drill image mismatch: backup=%s configured=%s", meta.Image, configuredImage)
	}
	expected := strings.TrimSpace(meta.SHA256)
	if expected == "" {
		return errors.New("GitLab backup metadata is missing sha256")
	}
	if len(expected) != 64 {
		return errors.New("GitLab backup metadata contains invalid sha256")
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return errors.New("GitLab backup metadata contains invalid sha256")
	}
	sum, err := gitlabFileSHA256(archive)
	if err != nil {
		return err
	}
	if !strings.EqualFold(sum, expected) {
		return errors.New("GitLab backup archive checksum mismatch")
	}
	return nil
}

func extractGitLabArchive(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open GitLab backup gzip stream: %w", err)
	}
	defer gz.Close()

	root, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read GitLab backup archive: %w", err)
		}
		name, err := safeGitLabArchiveName(hdr.Name)
		if err != nil {
			return err
		}
		if !allowedGitLabArchivePath(name) {
			return fmt.Errorf("GitLab backup archive contains unexpected path %q", hdr.Name)
		}

		local := filepath.FromSlash(name)
		target := filepath.Join(root, local)
		rel, err := filepath.Rel(root, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
			return fmt.Errorf("GitLab backup archive path escapes restore root: %q", hdr.Name)
		}

		mode := os.FileMode(hdr.Mode).Perm()
		switch hdr.Typeflag {
		case tar.TypeDir:
			if mode == 0 {
				mode = 0o700
			}
			if err := os.MkdirAll(target, mode); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			if _, err := os.Lstat(target); err == nil {
				return fmt.Errorf("GitLab backup archive contains duplicate path %q", hdr.Name)
			} else if !os.IsNotExist(err) {
				return err
			}
			if mode == 0 {
				mode = 0o600
			}
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			written, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if written != hdr.Size {
				return fmt.Errorf("GitLab backup archive entry %q size mismatch: wrote=%d expected=%d", hdr.Name, written, hdr.Size)
			}
		default:
			return fmt.Errorf("GitLab backup archive contains unsupported entry type %d at %q", hdr.Typeflag, hdr.Name)
		}
	}
}

func safeGitLabArchiveName(name string) (string, error) {
	normalized := strings.ReplaceAll(name, "\\", "/")
	clean := path.Clean(normalized)
	if clean == "." || clean == "" || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("unsafe GitLab backup archive path %q", name)
	}
	local := filepath.FromSlash(clean)
	if filepath.IsAbs(local) || filepath.VolumeName(local) != "" {
		return "", fmt.Errorf("unsafe GitLab backup archive path %q", name)
	}
	return clean, nil
}

func allowedGitLabArchivePath(name string) bool {
	return name == "compose.yml" ||
		name == "config" || strings.HasPrefix(name, "config/") ||
		name == "data" || name == "data/backups" || strings.HasPrefix(name, "data/backups/")
}
