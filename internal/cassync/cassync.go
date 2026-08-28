package cassync

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Manifest struct {
	Version int      `json:"version"`
	Objects []Object `json:"objects"`
}
type Object struct {
	Digest string `json:"digest"`
	Bytes  int64  `json:"bytes"`
}

// Archive writes only objects in divergent Merkle prefixes whose rendezvous
// placement includes target. Existing target segments that are identical are
// skipped without enumerating individual leaves.
func Archive(casRoot string, dst io.Writer, prefixes []string, pool []string, factor int, targetAgent string) (Manifest, error) {
	if factor <= 0 {
		return Manifest{Version: 1}, nil
	}
	if factor > len(pool) {
		factor = len(pool)
	}
	allowed := map[string]bool{}
	for _, p := range prefixes {
		allowed[strings.ToLower(p)] = true
	}
	tw := tar.NewWriter(dst)
	defer tw.Close()
	m := Manifest{Version: 1}
	base := filepath.Join(casRoot, "sha256")
	for prefix := range allowed {
		if len(prefix) != 2 {
			continue
		}
		dir := filepath.Join(base, prefix)
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			digest := strings.ToLower(e.Name())
			if !validDigest(digest) {
				continue
			}
			if !contains(desiredAgents(digest, pool, factor), targetAgent) {
				continue
			}
			path := filepath.Join(dir, e.Name())
			st, err := e.Info()
			if err != nil || !st.Mode().IsRegular() {
				continue
			}
			h, err := hashFile(path)
			if err != nil {
				return m, err
			}
			if h != digest {
				return m, fmt.Errorf("CAS object %s digest mismatch", digest)
			}
			hdr := &tar.Header{Name: "objects/" + digest, Mode: 0600, Size: st.Size(), Typeflag: tar.TypeReg}
			if err := tw.WriteHeader(hdr); err != nil {
				return m, err
			}
			f, err := os.Open(path)
			if err != nil {
				return m, err
			}
			_, cp := io.Copy(tw, f)
			_ = f.Close()
			if cp != nil {
				return m, cp
			}
			m.Objects = append(m.Objects, Object{Digest: digest, Bytes: st.Size()})
		}
	}
	sort.Slice(m.Objects, func(i, j int) bool { return m.Objects[i].Digest < m.Objects[j].Digest })
	b, _ := json.Marshal(m)
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
		return m, err
	}
	if _, err := tw.Write(b); err != nil {
		return m, err
	}
	return m, nil
}

// Extract verifies every object against its content-addressed name and installs
// atomically. Existing valid objects are retained, so retries are idempotent.
func Extract(src io.Reader, casRoot string) (Manifest, error) {
	tr := tar.NewReader(src)
	m := Manifest{Version: 1}
	seen := map[string]Object{}
	if err := os.MkdirAll(casRoot, 0o700); err != nil {
		return m, err
	}
	stage, err := os.MkdirTemp(casRoot, ".cas-sync-")
	if err != nil {
		return m, err
	}
	defer os.RemoveAll(stage)
	for {
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return m, e
		}
		name := filepath.ToSlash(hdr.Name)
		if name == "manifest.json" {
			var wire Manifest
			if err := json.NewDecoder(io.LimitReader(tr, 8<<20)).Decode(&wire); err != nil {
				return m, err
			}
			m = wire
			continue
		}
		if hdr.Typeflag != tar.TypeReg || !strings.HasPrefix(name, "objects/") {
			return m, fmt.Errorf("unsafe CAS sync entry %q", name)
		}
		digest := strings.TrimPrefix(name, "objects/")
		if !validDigest(digest) || strings.Contains(digest, "/") {
			return m, fmt.Errorf("invalid CAS digest entry")
		}
		tmp := filepath.Join(stage, digest)
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return m, err
		}
		h := sha256.New()
		n, cp := io.Copy(io.MultiWriter(f, h), io.LimitReader(tr, hdr.Size+1))
		syncErr := f.Sync()
		closeErr := f.Close()
		if cp != nil {
			return m, cp
		}
		if syncErr != nil {
			return m, syncErr
		}
		if closeErr != nil {
			return m, closeErr
		}
		if n != hdr.Size || hex.EncodeToString(h.Sum(nil)) != digest {
			return m, fmt.Errorf("CAS sync object %s checksum mismatch", digest)
		}
		seen[digest] = Object{Digest: digest, Bytes: n}
	}
	for digest, obj := range seen {
		dst := filepath.Join(casRoot, "sha256", digest[:2], digest)
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return m, err
		}
		if h, err := hashFile(dst); err == nil {
			if h != digest {
				return m, fmt.Errorf("existing CAS object %s corrupt", digest)
			}
			continue
		}
		srcPath := filepath.Join(stage, digest)
		if err := os.Rename(srcPath, dst); err != nil {
			return m, err
		}
		_ = obj
	}
	for _, obj := range m.Objects {
		if got, ok := seen[obj.Digest]; ok && got.Bytes != obj.Bytes {
			return m, fmt.Errorf("CAS sync manifest size mismatch")
		}
		dst := filepath.Join(casRoot, "sha256", obj.Digest[:2], obj.Digest)
		h, err := hashFile(dst)
		if err != nil || h != obj.Digest {
			return m, fmt.Errorf("CAS sync manifest object %s missing after install", obj.Digest)
		}
	}
	return m, nil
}

func desiredAgents(digest string, pool []string, factor int) []string {
	type scored struct {
		id    string
		score string
	}
	xs := make([]scored, 0, len(pool))
	for _, id := range pool {
		sum := sha256.Sum256([]byte(digest + "\x00" + id))
		xs = append(xs, scored{id: id, score: hex.EncodeToString(sum[:])})
	}
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].score == xs[j].score {
			return xs[i].id < xs[j].id
		}
		return xs[i].score > xs[j].score
	})
	if factor > len(xs) {
		factor = len(xs)
	}
	out := make([]string, 0, factor)
	for i := 0; i < factor; i++ {
		out = append(out, xs[i].id)
	}
	return out
}
func contains(xs []string, w string) bool {
	for _, x := range xs {
		if x == w {
			return true
		}
	}
	return false
}
func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func hashFile(path string) (string, error) {
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

// ArchiveDigests writes an explicit set of verified CAS objects. It is used by
// v0.8 for failure-domain-aware erasure shard movement where rendezvous
// placement is not sufficient: the control plane has already selected the exact
// target for each shard index.
func ArchiveDigests(casRoot string, dst io.Writer, digests []string) (Manifest, error) {
	tw := tar.NewWriter(dst)
	defer tw.Close()
	uniq := map[string]struct{}{}
	for _, d := range digests {
		d = strings.ToLower(strings.TrimSpace(d))
		if !validDigest(d) {
			return Manifest{}, fmt.Errorf("invalid CAS digest %q", d)
		}
		uniq[d] = struct{}{}
	}
	ordered := make([]string, 0, len(uniq))
	for d := range uniq {
		ordered = append(ordered, d)
	}
	sort.Strings(ordered)
	m := Manifest{Version: 1}
	for _, digest := range ordered {
		path := filepath.Join(casRoot, "sha256", digest[:2], digest)
		st, err := os.Stat(path)
		if err != nil {
			return m, err
		}
		if !st.Mode().IsRegular() {
			return m, fmt.Errorf("CAS object is not regular: %s", digest)
		}
		h, err := hashFile(path)
		if err != nil {
			return m, err
		}
		if !strings.EqualFold(h, digest) {
			return m, fmt.Errorf("CAS object %s digest mismatch", digest)
		}
		if err := tw.WriteHeader(&tar.Header{Name: "objects/" + digest, Mode: 0600, Size: st.Size(), Typeflag: tar.TypeReg}); err != nil {
			return m, err
		}
		f, err := os.Open(path)
		if err != nil {
			return m, err
		}
		_, cp := io.Copy(tw, f)
		_ = f.Close()
		if cp != nil {
			return m, cp
		}
		m.Objects = append(m.Objects, Object{Digest: digest, Bytes: st.Size()})
	}
	b, _ := json.Marshal(m)
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
		return m, err
	}
	if _, err := tw.Write(b); err != nil {
		return m, err
	}
	return m, nil
}
