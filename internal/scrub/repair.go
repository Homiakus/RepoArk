package scrub

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Homiakus/repoark/internal/cas"
	"github.com/Homiakus/repoark/internal/erasure"
)

// LocalErasureRepair reconstructs a corrupted CAS object from its verified
// local erasure set. Reconstruction is verified before the original inode is
// mutated; the final copy is written in-place so existing hard links are healed.
func LocalErasureRepair(casRoot string) RepairFunc {
	return func(_ context.Context, digest, _ string) error {
		digest = strings.ToLower(strings.TrimSpace(digest))
		if len(digest) != 64 {
			return fmt.Errorf("invalid digest")
		}
		dir := filepath.Join(casRoot, "erasure", digest[:2], digest)
		tmp, err := os.CreateTemp(filepath.Join(casRoot, "sha256", digest[:2]), ".repair-*")
		if err != nil {
			return err
		}
		tmpPath := tmp.Name()
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		defer os.Remove(tmpPath)
		if err := erasure.Reconstruct(dir, tmpPath); err != nil {
			return err
		}
		got, _, err := cas.HashFile(tmpPath)
		if err != nil {
			return err
		}
		if !strings.EqualFold(got, digest) {
			return fmt.Errorf("reconstructed digest mismatch")
		}
		src, err := os.Open(tmpPath)
		if err != nil {
			return err
		}
		defer src.Close()
		dstPath := cas.New(casRoot, 0).BlobPath(digest)
		dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_, cpErr := io.Copy(dst, src)
		if cpErr == nil {
			cpErr = dst.Sync()
		}
		closeErr := dst.Close()
		if cpErr == nil {
			cpErr = closeErr
		}
		if cpErr != nil {
			return cpErr
		}
		got, _, err = cas.HashFile(dstPath)
		if err != nil {
			return err
		}
		if !strings.EqualFold(got, digest) {
			return fmt.Errorf("post-repair digest mismatch")
		}
		return nil
	}
}
