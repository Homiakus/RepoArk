package tiering

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/cas"
)

type Config struct {
	ColdRoot     string
	MinAge       time.Duration
	MinBytes     int64
	RcloneRemote string
}

type Result struct {
	Scanned     int   `json:"scanned"`
	Eligible    int   `json:"eligible"`
	CopiedLocal int   `json:"copied_local"`
	CopiedS3    int   `json:"copied_remote"`
	Bytes       int64 `json:"bytes"`
	Skipped     int   `json:"skipped"`
}

// CopyTier never removes the hot CAS object. v0.8 treats SSD/HDD/S3 as a
// verified secondary copy tier so existing generation restore semantics remain
// authoritative and a tiering policy cannot silently make local recovery
// depend on a remote provider.
func CopyTier(ctx context.Context, st *cas.Store, cfg Config, now time.Time) (Result, error) {
	var out Result
	if st == nil {
		return out, fmt.Errorf("CAS store is required")
	}
	objects, err := st.ListObjects()
	if err != nil {
		return out, err
	}
	for _, o := range objects {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}
		out.Scanned++
		if o.Bytes < cfg.MinBytes || (cfg.MinAge > 0 && now.Sub(o.ModTime) < cfg.MinAge) {
			out.Skipped++
			continue
		}
		out.Eligible++
		if strings.TrimSpace(cfg.ColdRoot) != "" {
			dst := filepath.Join(cfg.ColdRoot, "sha256", o.Digest[:2], o.Digest)
			copied, err := copyVerified(o.Path, dst, o.Digest)
			if err != nil {
				return out, err
			}
			if copied {
				out.CopiedLocal++
				out.Bytes += o.Bytes
			}
		}
		if strings.TrimSpace(cfg.RcloneRemote) != "" {
			remote := strings.TrimRight(cfg.RcloneRemote, "/") + "/sha256/" + o.Digest[:2] + "/" + o.Digest
			cmd := exec.CommandContext(ctx, "rclone", "copyto", "--immutable", o.Path, remote)
			if b, err := cmd.CombinedOutput(); err != nil {
				return out, fmt.Errorf("rclone tier %s: %w: %s", o.Digest, err, strings.TrimSpace(string(b)))
			}
			out.CopiedS3++
		}
	}
	return out, nil
}

func copyVerified(src, dst, digest string) (bool, error) {
	if got, _, err := cas.HashFile(dst); err == nil {
		if strings.EqualFold(got, digest) {
			return false, nil
		}
		return false, fmt.Errorf("cold-tier object %s exists with wrong digest", digest)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return false, err
	}
	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".repoark-tier-")
	if err != nil {
		return false, err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if _, err = io.Copy(tmp, in); err == nil {
		err = tmp.Sync()
	}
	if e := tmp.Close(); err == nil {
		err = e
	}
	if err != nil {
		return false, err
	}
	got, _, err := cas.HashFile(name)
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(got, digest) {
		return false, fmt.Errorf("cold-tier copy digest mismatch")
	}
	if err := os.Rename(name, dst); err != nil {
		return false, err
	}
	ok = true
	return true, nil
}
