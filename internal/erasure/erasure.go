package erasure

import (
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

	"github.com/klauspost/reedsolomon"
)

type Config struct {
	DataShards   int
	ParityShards int
	BlockBytes   int
}

type Shard struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	Version       int       `json:"version"`
	ObjectSHA256  string    `json:"object_sha256"`
	OriginalBytes int64     `json:"original_bytes"`
	DataShards    int       `json:"data_shards"`
	ParityShards  int       `json:"parity_shards"`
	BlockBytes    int       `json:"block_bytes"`
	Blocks        int64     `json:"blocks"`
	CreatedAt     time.Time `json:"created_at"`
	Shards        []Shard   `json:"shards"`
}

func EncodeFile(src, dstDir string, cfg Config) (Manifest, error) {
	if cfg.DataShards < 2 || cfg.ParityShards < 1 || cfg.BlockBytes < 64<<10 {
		return Manifest{}, errors.New("invalid erasure configuration")
	}
	enc, err := reedsolomon.New(cfg.DataShards, cfg.ParityShards)
	if err != nil {
		return Manifest{}, err
	}
	in, err := os.Open(src)
	if err != nil {
		return Manifest{}, err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return Manifest{}, err
	}
	objectHash, err := hashFile(src)
	if err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dstDir), 0o700); err != nil {
		return Manifest{}, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dstDir), ".erasure-")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(stage)
	total := cfg.DataShards + cfg.ParityShards
	outs := make([]*os.File, total)
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("shard-%03d.rs", i)
		outs[i], err = os.OpenFile(filepath.Join(stage, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			closeFiles(outs)
			return Manifest{}, err
		}
	}
	buf := make([]byte, cfg.BlockBytes*cfg.DataShards)
	var blocks int64
	for {
		n, readErr := io.ReadFull(in, buf)
		if readErr == io.EOF {
			break
		}
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			closeFiles(outs)
			return Manifest{}, readErr
		}
		for i := n; i < len(buf); i++ {
			buf[i] = 0
		}
		shards := make([][]byte, total)
		for i := 0; i < cfg.DataShards; i++ {
			start := i * cfg.BlockBytes
			shards[i] = buf[start : start+cfg.BlockBytes]
		}
		for i := cfg.DataShards; i < total; i++ {
			shards[i] = make([]byte, cfg.BlockBytes)
		}
		if err := enc.Encode(shards); err != nil {
			closeFiles(outs)
			return Manifest{}, err
		}
		for i := range shards {
			if _, err := outs[i].Write(shards[i]); err != nil {
				closeFiles(outs)
				return Manifest{}, err
			}
		}
		blocks++
		if readErr == io.ErrUnexpectedEOF {
			break
		}
	}
	for _, f := range outs {
		if err := f.Sync(); err != nil {
			closeFiles(outs)
			return Manifest{}, err
		}
		if err := f.Close(); err != nil {
			return Manifest{}, err
		}
	}
	m := Manifest{Version: 1, ObjectSHA256: objectHash, OriginalBytes: info.Size(), DataShards: cfg.DataShards, ParityShards: cfg.ParityShards, BlockBytes: cfg.BlockBytes, Blocks: blocks, CreatedAt: time.Now().UTC()}
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("shard-%03d.rs", i)
		path := filepath.Join(stage, name)
		h, err := hashFile(path)
		if err != nil {
			return Manifest{}, err
		}
		st, _ := os.Stat(path)
		m.Shards = append(m.Shards, Shard{Index: i, Name: name, Bytes: st.Size(), SHA256: h})
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(stage, "manifest.json"), append(b, '\n'), 0o600); err != nil {
		return Manifest{}, err
	}
	if _, err := os.Stat(dstDir); err == nil {
		old, e := ReadManifest(dstDir)
		if e == nil && old.ObjectSHA256 == m.ObjectSHA256 {
			return old, nil
		}
		return Manifest{}, fmt.Errorf("erasure destination already exists: %s", dstDir)
	} else if !os.IsNotExist(err) {
		return Manifest{}, err
	}
	if err := os.Rename(stage, dstDir); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func ReadManifest(dir string) (Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	return m, nil
}

func Verify(dir string) (Manifest, error) {
	m, err := ReadManifest(dir)
	if err != nil {
		return m, err
	}
	valid := 0
	for _, sh := range m.Shards {
		path := filepath.Join(dir, filepath.Base(sh.Name))
		st, e := os.Stat(path)
		if e != nil {
			continue
		}
		if st.Size() != sh.Bytes {
			continue
		}
		h, e := hashFile(path)
		if e == nil && strings.EqualFold(h, sh.SHA256) {
			valid++
		}
	}
	if valid < m.DataShards {
		return m, fmt.Errorf("only %d/%d required shards verify", valid, m.DataShards)
	}
	return m, nil
}

// Reconstruct restores the original object from any DataShards verified shards.
// Processing is block bounded; memory is O((data+parity)*BlockBytes), not O(file).
func Reconstruct(dir, dst string) error {
	m, err := ReadManifest(dir)
	if err != nil {
		return err
	}
	enc, err := reedsolomon.New(m.DataShards, m.ParityShards)
	if err != nil {
		return err
	}
	total := m.DataShards + m.ParityShards
	ins := make([]*os.File, total)
	available := 0
	byIndex := map[int]Shard{}
	for _, sh := range m.Shards {
		byIndex[sh.Index] = sh
	}
	for i := 0; i < total; i++ {
		sh, ok := byIndex[i]
		if !ok {
			continue
		}
		path := filepath.Join(dir, filepath.Base(sh.Name))
		h, e := hashFile(path)
		if e != nil || !strings.EqualFold(h, sh.SHA256) {
			continue
		}
		f, e := os.Open(path)
		if e == nil {
			ins[i] = f
			available++
		}
	}
	defer closeFiles(ins)
	if available < m.DataShards {
		return fmt.Errorf("insufficient valid shards: %d < %d", available, m.DataShards)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := dst + ".repoark-reconstruct.part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	remaining := m.OriginalBytes
	for block := int64(0); block < m.Blocks; block++ {
		shards := make([][]byte, total)
		for i := 0; i < total; i++ {
			if ins[i] == nil {
				continue
			}
			b := make([]byte, m.BlockBytes)
			if _, e := io.ReadFull(ins[i], b); e != nil {
				return e
			}
			shards[i] = b
		}
		if err := enc.Reconstruct(shards); err != nil {
			return err
		}
		for i := 0; i < m.DataShards && remaining > 0; i++ {
			n := int64(len(shards[i]))
			if n > remaining {
				n = remaining
			}
			if _, err := out.Write(shards[i][:n]); err != nil {
				return err
			}
			remaining -= n
		}
	}
	if remaining != 0 {
		return fmt.Errorf("reconstruction truncated: %d bytes missing", remaining)
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	h, err := hashFile(tmp)
	if err != nil {
		return err
	}
	if !strings.EqualFold(h, m.ObjectSHA256) {
		return errors.New("reconstructed object SHA-256 mismatch")
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	ok = true
	return nil
}

func ProtectPaths(casRoot string, paths []string, minBytes int64, cfg Config) (int, error) {
	seen := map[string]struct{}{}
	var files []string
	for _, root := range paths {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.Mode().IsRegular() && info.Size() >= minBytes {
				files = append(files, path)
			}
			return nil
		})
	}
	sort.Strings(files)
	n := 0
	for _, path := range files {
		digest, err := hashFile(path)
		if err != nil {
			return n, err
		}
		if _, ok := seen[digest]; ok {
			continue
		}
		seen[digest] = struct{}{}
		obj := filepath.Join(casRoot, "sha256", digest[:2], digest)
		if _, err := os.Stat(obj); err != nil {
			continue
		}
		dst := filepath.Join(casRoot, "erasure", digest[:2], digest)
		if _, err := EncodeFile(obj, dst, cfg); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
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
func closeFiles[T interface{ Close() error }](xs []T) {
	for _, f := range xs {
		if any(f) != nil {
			_ = f.Close()
		}
	}
}
