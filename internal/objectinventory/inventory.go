package objectinventory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Inventory is a compact Merkle summary of a RepoArk SHA-256 CAS namespace.
// Objects are grouped by the first two digest hex characters. A control plane
// can therefore identify divergent prefixes without receiving the full object
// list from an agent heartbeat.
type Inventory struct {
	Version    int       `json:"version"`
	Root       string    `json:"root"`
	MerkleRoot string    `json:"merkle_root"`
	Objects    int       `json:"objects"`
	Bytes      int64     `json:"bytes"`
	CreatedAt  time.Time `json:"created_at"`
	Segments   []Segment `json:"segments"`
}

type Segment struct {
	Prefix string `json:"prefix"`
	Root   string `json:"root"`
	Count  int    `json:"count"`
	Bytes  int64  `json:"bytes"`
}

type leaf struct {
	digest string
	size   int64
}

func Build(casRoot string) (Inventory, error) {
	inv := Inventory{Version: 1, Root: casRoot, CreatedAt: time.Now().UTC()}
	byPrefix := map[string][]leaf{}
	root := filepath.Join(casRoot, "sha256")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := strings.ToLower(strings.TrimSpace(d.Name()))
		if len(name) != 64 {
			return fmt.Errorf("invalid CAS object name %q", path)
		}
		if _, err := hex.DecodeString(name); err != nil {
			return fmt.Errorf("invalid CAS digest %q: %w", name, err)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		byPrefix[name[:2]] = append(byPrefix[name[:2]], leaf{digest: name, size: info.Size()})
		inv.Objects++
		inv.Bytes += info.Size()
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		inv.MerkleRoot = hashLines(nil)
		return inv, nil
	}
	if err != nil {
		return Inventory{}, err
	}
	prefixes := make([]string, 0, len(byPrefix))
	for p := range byPrefix {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	top := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		leaves := byPrefix[prefix]
		sort.Slice(leaves, func(i, j int) bool { return leaves[i].digest < leaves[j].digest })
		lines := make([]string, 0, len(leaves))
		var bytes int64
		for _, l := range leaves {
			lines = append(lines, fmt.Sprintf("%s:%d", l.digest, l.size))
			bytes += l.size
		}
		root := hashLines(lines)
		inv.Segments = append(inv.Segments, Segment{Prefix: prefix, Root: root, Count: len(leaves), Bytes: bytes})
		top = append(top, fmt.Sprintf("%s:%s:%d:%d", prefix, root, len(leaves), bytes))
	}
	inv.MerkleRoot = hashLines(top)
	return inv, nil
}

func hashLines(lines []string) string {
	h := sha256.New()
	for _, line := range lines {
		_, _ = h.Write([]byte(line))
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// DivergentPrefixes returns CAS digest prefixes whose segment roots differ.
func DivergentPrefixes(a, b Inventory) []string {
	am, bm := map[string]Segment{}, map[string]Segment{}
	for _, s := range a.Segments {
		am[s.Prefix] = s
	}
	for _, s := range b.Segments {
		bm[s.Prefix] = s
	}
	keys := map[string]struct{}{}
	for k := range am {
		keys[k] = struct{}{}
	}
	for k := range bm {
		keys[k] = struct{}{}
	}
	var out []string
	for k := range keys {
		x, xok := am[k]
		y, yok := bm[k]
		if !xok || !yok || x.Root != y.Root || x.Count != y.Count || x.Bytes != y.Bytes {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func EncodeCompact(inv Inventory) string {
	b, _ := json.Marshal(inv)
	return string(b)
}

func DecodeCompact(s string) (Inventory, error) {
	var inv Inventory
	if strings.TrimSpace(s) == "" {
		return inv, nil
	}
	err := json.Unmarshal([]byte(s), &inv)
	return inv, err
}
