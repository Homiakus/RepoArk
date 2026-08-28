package cas

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Store is a content-addressed object store. Objects are addressed by SHA-256
// and kept under sha256/ab/<digest>. User-facing backup paths remain ordinary
// files (hard links when possible), so restore logic does not depend on CAS.
type Store struct {
	Root        string
	MinFileSize int64
}

type IngestResult struct {
	SHA256         string `json:"sha256"`
	Size           int64  `json:"size"`
	BlobPath       string `json:"blob_path"`
	Existing       bool   `json:"existing"`
	HardLinked     bool   `json:"hard_linked"`
	ReclaimedBytes int64  `json:"reclaimed_bytes"`
}

type Stats struct {
	Objects       int   `json:"objects"`
	PhysicalBytes int64 `json:"physical_bytes"`
	LogicalFiles  int   `json:"logical_files,omitempty"`
	LogicalBytes  int64 `json:"logical_bytes,omitempty"`
	Reclaimed     int64 `json:"reclaimed_bytes,omitempty"`
}

func New(root string, minFileSize int64) *Store {
	if minFileSize < 0 {
		minFileSize = 0
	}
	return &Store{Root: root, MinFileSize: minFileSize}
}

func (s *Store) blobPath(sum string) string {
	return filepath.Join(s.Root, "sha256", sum[:2], sum)
}

type ObjectInfo struct {
	Digest  string    `json:"digest"`
	Path    string    `json:"path"`
	Bytes   int64     `json:"bytes"`
	ModTime time.Time `json:"mod_time"`
}

func (s *Store) BlobPath(sum string) string {
	sum = strings.ToLower(strings.TrimSpace(sum))
	if len(sum) != 64 {
		return ""
	}
	return s.blobPath(sum)
}

func (s *Store) ListObjects() ([]ObjectInfo, error) {
	root := filepath.Join(s.Root, "sha256")
	var out []ObjectInfo
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		digest := strings.ToLower(d.Name())
		if len(digest) != 64 {
			return fmt.Errorf("invalid CAS object name: %s", path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, ObjectInfo{Digest: digest, Path: path, Bytes: info.Size(), ModTime: info.ModTime().UTC()})
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Digest < out[j].Digest })
	return out, nil
}

func HashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Ingest stores a file in CAS and, when safe, replaces the original with a
// hard link to the CAS blob. If hard linking is impossible (cross-device,
// filesystem restrictions, Windows policy), the original file is retained.
func (s *Store) Ingest(path string) (IngestResult, error) {
	var out IngestResult
	info, err := os.Stat(path)
	if err != nil {
		return out, err
	}
	if !info.Mode().IsRegular() {
		return out, fmt.Errorf("not a regular file: %s", path)
	}
	if info.Size() < s.MinFileSize {
		return out, nil
	}
	sum, size, err := HashFile(path)
	if err != nil {
		return out, err
	}
	out.SHA256, out.Size = sum, size
	blob := s.blobPath(sum)
	out.BlobPath = blob
	if err := os.MkdirAll(filepath.Dir(blob), 0o700); err != nil {
		return out, err
	}

	if binfo, statErr := os.Stat(blob); statErr == nil {
		if binfo.Size() != size {
			return out, fmt.Errorf("CAS collision/size mismatch for %s", sum)
		}
		out.Existing = true
	} else if errors.Is(statErr, os.ErrNotExist) {
		tmp, err := os.CreateTemp(filepath.Dir(blob), ".repoark-cas-*")
		if err != nil {
			return out, err
		}
		tmpName := tmp.Name()
		ok := false
		defer func() {
			if !ok {
				_ = os.Remove(tmpName)
			}
		}()
		src, err := os.Open(path)
		if err != nil {
			_ = tmp.Close()
			return out, err
		}
		_, cpErr := io.Copy(tmp, src)
		closeErr := src.Close()
		if cpErr == nil {
			cpErr = closeErr
		}
		if cpErr == nil {
			cpErr = tmp.Sync()
		}
		if e := tmp.Close(); cpErr == nil {
			cpErr = e
		}
		if cpErr != nil {
			return out, cpErr
		}
		if err := os.Rename(tmpName, blob); err != nil {
			if _, st := os.Stat(blob); st != nil {
				return out, err
			}
			_ = os.Remove(tmpName)
			out.Existing = true
		} else {
			ok = true
		}
		_ = os.Chmod(blob, info.Mode().Perm())
	} else {
		return out, statErr
	}

	// If it is already the same inode there is nothing to do.
	if sameFile(path, blob) {
		out.HardLinked = true
		return out, nil
	}
	swap := path + ".repoark-cas-swap"
	_ = os.Remove(swap)
	if err := os.Link(blob, swap); err == nil {
		if err := os.Rename(swap, path); err == nil {
			out.HardLinked = true
			if out.Existing {
				out.ReclaimedBytes = size
			}
			return out, nil
		}
		_ = os.Remove(swap)
	}
	return out, nil
}

func sameFile(a, b string) bool {
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ai, bi)
}

// Verify checks that every CAS object's path agrees with its content hash.
func (s *Store) Verify() (Stats, error) {
	var st Stats
	root := filepath.Join(s.Root, "sha256")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if len(name) != 64 {
			return fmt.Errorf("invalid CAS object name: %s", path)
		}
		got, size, err := HashFile(path)
		if err != nil {
			return err
		}
		if !strings.EqualFold(got, name) {
			return fmt.Errorf("CAS hash mismatch: %s", path)
		}
		st.Objects++
		st.PhysicalBytes += size
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	return st, err
}

func (s *Store) Stats() (Stats, error) { return s.Verify() }

// Compact walks selected immutable trees and deduplicates regular files. Sidecar
// checksum files and JSON indexes are intentionally skipped.
func (s *Store) Compact(paths []string) (Stats, error) {
	var out Stats
	sort.Strings(paths)
	for _, root := range paths {
		info, err := os.Stat(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return out, err
		}
		if !info.IsDir() {
			if info.Mode().IsRegular() && info.Size() >= s.MinFileSize {
				out.LogicalFiles++
				out.LogicalBytes += info.Size()
				r, err := s.Ingest(root)
				if err != nil {
					return out, err
				}
				out.Reclaimed += r.ReclaimedBytes
			}
			continue
		}
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".json" || ext == ".sig" || ext == ".sha256" || strings.HasSuffix(path, ".sig.json") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Size() < s.MinFileSize {
				return nil
			}
			out.LogicalFiles++
			out.LogicalBytes += info.Size()
			r, err := s.Ingest(path)
			if err != nil {
				return err
			}
			out.Reclaimed += r.ReclaimedBytes
			return nil
		})
		if err != nil {
			return out, err
		}
	}
	physical, err := s.Verify()
	if err != nil {
		return out, err
	}
	out.Objects, out.PhysicalBytes = physical.Objects, physical.PhysicalBytes
	return out, nil
}

type GCResult struct {
	ScannedFiles int   `json:"scanned_files"`
	Reachable    int   `json:"reachable_objects"`
	Removed      int   `json:"removed_objects"`
	RemovedBytes int64 `json:"removed_bytes"`
	DryRun       bool  `json:"dry_run"`
}

// GC removes CAS objects that are not reachable from any supplied logical
// backup root. The reachability pass hashes eligible immutable payload files
// first, so deletion never relies on link-count semantics or a particular
// filesystem implementation. Use dryRun for a deletion plan without mutation.
func (s *Store) GC(roots []string, dryRun bool) (GCResult, error) {
	return s.GCProtected(roots, dryRun, nil)
}

// GCProtected behaves like GC but treats durable control-plane object refs and
// active leases as additional roots. This prevents v0.8 erasure shards and
// in-flight object transfers from being collected between planning and use.
func (s *Store) GCProtected(roots []string, dryRun bool, protected map[string]struct{}) (GCResult, error) {
	out := GCResult{DryRun: dryRun}
	reachable := map[string]struct{}{}
	casAbs, _ := filepath.Abs(s.Root)
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		rootAbs, _ := filepath.Abs(root)
		if rootAbs == casAbs || strings.HasPrefix(rootAbs, casAbs+string(os.PathSeparator)) {
			continue
		}
		info, err := os.Stat(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return out, err
		}
		scan := func(path string, info fs.FileInfo) error {
			if !info.Mode().IsRegular() || info.Size() < s.MinFileSize || skipLogical(path) {
				return nil
			}
			sum, _, err := HashFile(path)
			if err != nil {
				return err
			}
			reachable[sum] = struct{}{}
			out.ScannedFiles++
			return nil
		}
		if !info.IsDir() {
			if err := scan(root, info); err != nil {
				return out, err
			}
			continue
		}
		if err := filepath.Walk(root, func(path string, info fs.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			return scan(path, info)
		}); err != nil {
			return out, err
		}
	}
	for d := range protected {
		if len(d) == 64 {
			reachable[strings.ToLower(d)] = struct{}{}
		}
	}
	out.Reachable = len(reachable)
	objectRoot := filepath.Join(s.Root, "sha256")
	err := filepath.WalkDir(objectRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if len(name) != 64 {
			return fmt.Errorf("invalid CAS object name: %s", path)
		}
		if _, ok := reachable[name]; ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out.Removed++
		out.RemovedBytes += info.Size()
		if !dryRun {
			return os.Remove(path)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if !dryRun {
		_ = removeEmptyDirs(objectRoot)
	}
	return out, nil
}

func skipLogical(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".sig") ||
		strings.HasSuffix(lower, ".sha256") || strings.HasSuffix(lower, ".sig.json") ||
		strings.Contains(lower, string(os.PathSeparator)+"manifests"+string(os.PathSeparator)) ||
		strings.Contains(lower, string(os.PathSeparator)+"state"+string(os.PathSeparator))
}

func removeEmptyDirs(root string) error {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		_ = os.Remove(d)
	}
	return nil
}
